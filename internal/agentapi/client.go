// The client the agent-side commands (`crowsnest ask|notify|say|mcp`) use to reach the socket.
// It reads its configuration from the environment crowsnest gives every agent it starts:
// CROWSNEST_SOCKET (where) and CROWSNEST_TOKEN (who is asking; the server maps it to the pane
// and job). It sends no pane id of its own: the server would not believe one (R1b N2, N7).

package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/questions"
)

// Environment variables the client reads.
const (
	EnvSocket = "CROWSNEST_SOCKET"
	EnvToken  = agentid.EnvToken
	// EnvJob is the job a job agent holds: only the default job id for the job verbs' paths.
	// The server decides what the caller may touch from its token, not from this.
	EnvJob = "CROWSNEST_JOB"
)

// pollSeconds is the long-poll window a waiting client asks for; it polls again until settled.
const pollSeconds = 90

// ErrNoSocket means CROWSNEST_SOCKET is not set.
var ErrNoSocket = errors.New("CROWSNEST_SOCKET is not set: run this inside a pane crowsnest started, or set CROWSNEST_SOCKET to the agent socket path that `crowsnest serve` logs at start")

// ServerDownError means nothing answered on the socket.
type ServerDownError struct {
	Path string
	Err  error
}

func (e *ServerDownError) Error() string {
	return fmt.Sprintf("crowsnest is not answering on %s (is `crowsnest serve` running?): %v", e.Path, e.Err)
}

func (e *ServerDownError) Unwrap() error { return e.Err }

// APIError is an error answer from the server; Message is meant for the agent.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return e.Message }

// Client talks to the agent socket.
type Client struct {
	socket string
	token  string
	job    string // default job id for the job verbs (CROWSNEST_JOB, --job, job_id)
	http   *http.Client
}

// NewClient returns a client for the socket at path, identifying itself with token ("" for an
// unidentified caller).
func NewClient(path, token string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", path)
		},
		MaxIdleConns: 2,
	}
	return &Client{socket: path, token: token, http: &http.Client{Transport: transport}}
}

// NewClientFromEnv builds a client from CROWSNEST_SOCKET and CROWSNEST_TOKEN, read through
// getenv. It returns ErrNoSocket when the socket is not named.
func NewClientFromEnv(getenv func(string) string) (*Client, error) {
	path := getenv(EnvSocket)
	if path == "" {
		return nil, ErrNoSocket
	}
	client := NewClient(path, getenv(EnvToken))
	client.job = getenv(EnvJob)
	return client, nil
}

// Socket is the socket path the client uses.
func (c *Client) Socket() string { return c.socket }

// Health checks the server answers.
func (c *Client) Health(ctx context.Context) (HealthResponse, error) {
	var out HealthResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/health", nil, &out)
	return out, err
}

// Ask posts a question.
func (c *Client) Ask(ctx context.Context, input questions.AskInput) (questions.Question, error) {
	var out questions.Question
	err := c.do(ctx, http.MethodPost, "/api/v1/questions", input, &out)
	return out, err
}

// Get reads a question; with wait > 0 it long-polls up to that long for it to settle.
func (c *Client) Get(ctx context.Context, id string, wait time.Duration) (questions.Question, error) {
	path := "/api/v1/questions/" + url.PathEscape(id)
	if wait > 0 {
		path += "?wait=" + strconv.Itoa(int(wait/time.Second))
	}
	var out questions.Question
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// WaitSettled long-polls until the question is no longer open or ctx ends. A poll that fails
// because the socket dropped or crowsnest answered 5xx (a restart, say) is retried with backoff
// for up to waitRetryBudget; the question itself is never asked again (R1b-C06).
func (c *Client) WaitSettled(ctx context.Context, id string) (questions.Question, error) {
	var failingSince time.Time
	backoff := waitRetryMin
	for {
		question, err := c.Get(ctx, id, pollSeconds*time.Second)
		switch {
		case err == nil && question.Status != questions.StatusOpen:
			return question, nil
		case err == nil:
			failingSince, backoff = time.Time{}, waitRetryMin
		case !retryable(err) || ctx.Err() != nil:
			return question, err
		case failingSince.IsZero():
			failingSince = time.Now()
		case time.Since(failingSince) > waitRetryBudget:
			return question, err
		}
		if err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return question, ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, waitRetryMax)
	}
}

// Retries of a failing long-poll: from waitRetryMin doubling to waitRetryMax, for at most
// waitRetryBudget in a row (long enough for crowsnest to restart).
const (
	waitRetryMin    = 500 * time.Millisecond
	waitRetryMax    = 10 * time.Second
	waitRetryBudget = 2 * time.Minute
)

// retryable is a transport failure or a server error: the poll may work next time.
func retryable(err error) bool {
	var down *ServerDownError
	var apiErr *APIError
	return errors.As(err, &down) || (errors.As(err, &apiErr) && apiErr.Status >= 500)
}

// Cancel withdraws a question the caller asked.
func (c *Client) Cancel(ctx context.Context, id string) (questions.Question, error) {
	var out questions.Question
	err := c.do(ctx, http.MethodDelete, "/api/v1/questions/"+url.PathEscape(id), nil, &out)
	return out, err
}

// Say asks for a sentence to be spoken in talk mode.
func (c *Client) Say(ctx context.Context, text string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/say", SayRequest{Text: text}, nil)
}

// do sends one request and decodes the envelope's data into out (when not nil).
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding the request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://crowsnest"+path, reader)
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		request.Header.Set(agentid.HeaderToken, c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &ServerDownError{Path: c.socket, Err: err}
	}
	defer func() { _ = response.Body.Close() }()
	return decodeResponse(response, out)
}

func decodeResponse(response *http.Response, out any) error {
	payload, err := io.ReadAll(io.LimitReader(response.Body, questions.MaxRequestBytes))
	if err != nil {
		return fmt.Errorf("reading crowsnest's answer: %w", err)
	}
	if response.StatusCode >= 300 {
		var envelope struct {
			Error struct{ Message, Code string } `json:"error"`
		}
		_ = json.Unmarshal(payload, &envelope)
		if envelope.Error.Message == "" {
			envelope.Error.Message = fmt.Sprintf("crowsnest answered HTTP %d", response.StatusCode)
		}
		return &APIError{Status: response.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message}
	}
	if out == nil {
		return nil
	}
	envelope := struct {
		Data any `json:"data"`
	}{Data: out}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("decoding crowsnest's answer: %w", err)
	}
	return nil
}
