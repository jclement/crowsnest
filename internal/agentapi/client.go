// The client the agent-side commands (`crowsnest ask|notify|say|mcp`) use to reach the socket.
// It reads its configuration from the environment crowsnest gives every pane it starts:
// CROWSNEST_SOCKET (where), HERDR_PANE_ID and CROWSNEST_JOB (who is asking).

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

	"github.com/jclement/crowsnest/internal/questions"
)

// Environment variables the client reads.
const (
	EnvSocket = "CROWSNEST_SOCKET"
	EnvPane   = "HERDR_PANE_ID"
	EnvJob    = "CROWSNEST_JOB"
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
	caller questions.Caller
	http   *http.Client
}

// NewClient returns a client for the socket at path, identifying itself as caller.
func NewClient(path string, caller questions.Caller) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", path)
		},
		MaxIdleConns: 2,
	}
	return &Client{socket: path, caller: caller, http: &http.Client{Transport: transport}}
}

// NewClientFromEnv builds a client from CROWSNEST_SOCKET, HERDR_PANE_ID and CROWSNEST_JOB, read
// through getenv. It returns ErrNoSocket when the socket is not named.
func NewClientFromEnv(getenv func(string) string) (*Client, error) {
	path := getenv(EnvSocket)
	if path == "" {
		return nil, ErrNoSocket
	}
	return NewClient(path, questions.Caller{PaneID: getenv(EnvPane), JobID: getenv(EnvJob)}), nil
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

// WaitSettled long-polls until the question is no longer open or ctx ends.
func (c *Client) WaitSettled(ctx context.Context, id string) (questions.Question, error) {
	for {
		question, err := c.Get(ctx, id, pollSeconds*time.Second)
		if err != nil || question.Status != questions.StatusOpen {
			return question, err
		}
		if err := ctx.Err(); err != nil {
			return question, err
		}
	}
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
	if c.caller.PaneID != "" {
		request.Header.Set(HeaderPane, c.caller.PaneID)
	}
	if c.caller.JobID != "" {
		request.Header.Set(HeaderJob, c.caller.JobID)
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
