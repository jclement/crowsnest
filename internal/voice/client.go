// Package voice is crowsnest's talk mode (DESIGN.md section 13): an ElevenLabs client for
// speech-to-text and text-to-speech, the server-held texts the browser may ask to hear, the
// talk-mode presence the browsers report, and the parser that turns the orchestrator's `SAY:`
// line into speech.
//
// The ElevenLabs API key lives only in this package's Client. It is sent as the `xi-api-key`
// header and nowhere else: never in a URL, a log line, an error message or a response. Errors
// from ElevenLabs are reduced to a status class (see APIError) before they leave this file,
// because an upstream error body is text crowsnest does not control.
//
// The endpoints and shapes here were taken from the ElevenLabs API reference on 2026-11-26
// (https://elevenlabs.io/docs/api-reference): POST /v1/speech-to-text (multipart, `model_id` and
// `file`, transcript in `text`) and POST /v1/text-to-speech/{voice_id}/stream (JSON `text` and
// `model_id`, `output_format` as a query parameter, the audio streamed back).
package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

// Model and format choices. Each is a named constant so a change is one line and shows in review.
const (
	// STTModel is ElevenLabs' batch transcription model. scribe_v2 is the model the
	// speech-to-text reference's own example uses; the models page also lists a newer scribe_v3
	// and deprecates scribe_v1. A human with a key should confirm scribe_v3 and switch if it
	// transcribes better (REPORT.md, "check with a real key").
	STTModel = "scribe_v2"
	// TTSModel is the low-latency text-to-speech model: a spoken reply should start quickly, and
	// the sentences are short. eleven_flash_v2_5 is current (the turbo models are deprecated).
	TTSModel = "eleven_flash_v2_5"
	// TTSOutputFormat is MP3, which every browser's <audio> plays (Safari has no Opus-in-Ogg).
	// mp3_44100_128 is the API's default and needs no paid tier.
	TTSOutputFormat = "mp3_44100_128"
	// TTSContentType is what an mp3 stream is served as when ElevenLabs does not say.
	TTSContentType = "audio/mpeg"
)

// Timeouts. Transcribing a minute of audio takes a few seconds; the TTS timeout covers only the
// response headers, because the body is streamed to the browser for as long as it takes.
const (
	sttTimeout       = 45 * time.Second
	ttsHeaderTimeout = 20 * time.Second
	// maxSTTResponse caps the transcript JSON we read back (words and timings included).
	maxSTTResponse = 4 << 20
)

// APIError is a failed call to ElevenLabs. It carries the HTTP status (0 when ElevenLabs could
// not be reached) and never the response body or the request, so printing it is always safe.
type APIError struct {
	Status int
	// Doing names the call: "transcribing" or "speaking".
	Doing string
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("elevenlabs %s: no response", e.Doing)
	}
	return fmt.Sprintf("elevenlabs %s: HTTP %d", e.Doing, e.Status)
}

// Classes of failure the HTTP layer turns into user-facing messages. Use errors.Is.
var (
	ErrUnauthorized = errors.New("elevenlabs refused the API key")         // 401, 403
	ErrRateLimited  = errors.New("elevenlabs is rate limiting")            // 429
	ErrRejected     = errors.New("elevenlabs could not process the input") // 400, 413, 415, 422
	ErrUnreachable  = errors.New("elevenlabs did not answer")              // network, timeout, 5xx
)

// Is maps the status onto the classes above.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
	case ErrRateLimited:
		return e.Status == http.StatusTooManyRequests
	case ErrRejected:
		switch e.Status {
		case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
			return true
		}
	case ErrUnreachable:
		return e.Status == 0 || e.Status >= 500
	}
	return false
}

// Client calls ElevenLabs. Build it with NewClient; it is safe for concurrent use.
type Client struct {
	baseURL string
	apiKey  string
	voiceID string
	http    *http.Client
}

// ClientOptions configure a Client. APIKey and VoiceID are required; BaseURL defaults to the
// production API, and HTTP to a client with no overall timeout (each call sets its own).
type ClientOptions struct {
	BaseURL string
	APIKey  string
	VoiceID string
	HTTP    *http.Client
}

// NewClient returns a client, or an error when the key or voice is missing.
func NewClient(opts ClientOptions) (*Client, error) {
	if opts.APIKey == "" {
		return nil, errors.New("voice: no ElevenLabs API key")
	}
	if opts.VoiceID == "" || strings.ContainsAny(opts.VoiceID, "/?#") {
		return nil, errors.New("voice: the ElevenLabs voice id must be a plain id")
	}
	if opts.BaseURL == "" {
		opts.BaseURL = "https://api.elevenlabs.io"
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{}
	}
	return &Client{baseURL: strings.TrimRight(opts.BaseURL, "/"), apiKey: opts.APIKey, voiceID: opts.VoiceID, http: opts.HTTP}, nil
}

// ---- Speech to text ------------------------------------------------------------------

// sttResponse is the part of ElevenLabs' transcription result crowsnest uses.
type sttResponse struct {
	Text string `json:"text"`
}

// Transcribe sends one recording and returns its text. contentType is the recording's MIME type
// as the browser's MediaRecorder named it (audio/webm on Chrome, audio/mp4 on Safari); ElevenLabs
// accepts both as they are, so nothing is transcoded.
func (c *Client) Transcribe(ctx context.Context, audio []byte, contentType string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, sttTimeout)
	defer cancel()
	body, formType, err := sttForm(audio, contentType)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/speech-to-text", body)
	if err != nil {
		return "", fmt.Errorf("building the transcription request: %w", err)
	}
	req.Header.Set("Content-Type", formType)
	res, err := c.do(req, "transcribing")
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	var parsed sttResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, maxSTTResponse)).Decode(&parsed); err != nil {
		return "", &APIError{Status: http.StatusBadGateway, Doing: "transcribing"}
	}
	return strings.TrimSpace(parsed.Text), nil
}

// sttForm builds the multipart body: model_id, then the file with its own content type.
func sttForm(audio []byte, contentType string) (*bytes.Buffer, string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("model_id", STTModel); err != nil {
		return nil, "", fmt.Errorf("writing the transcription form: %w", err)
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+recordingName(contentType)+`"`)
	header.Set("Content-Type", contentType)
	part, err := form.CreatePart(header)
	if err != nil {
		return nil, "", fmt.Errorf("writing the transcription form: %w", err)
	}
	if _, err := part.Write(audio); err != nil {
		return nil, "", fmt.Errorf("writing the transcription form: %w", err)
	}
	if err := form.Close(); err != nil {
		return nil, "", fmt.Errorf("writing the transcription form: %w", err)
	}
	return &body, form.FormDataContentType(), nil
}

// recordingName gives the upload a file name whose extension matches its type, for services that
// sniff by name.
func recordingName(contentType string) string {
	switch BaseType(contentType) {
	case "audio/mp4", "audio/x-m4a", "audio/aac":
		return "speech.m4a"
	case "audio/ogg":
		return "speech.ogg"
	case "audio/mpeg":
		return "speech.mp3"
	case "audio/wav":
		return "speech.wav"
	}
	return "speech.webm"
}

// ---- Text to speech ------------------------------------------------------------------

// ttsRequest is the JSON body of a text-to-speech call. Text normalisation is forced on because
// the flash model does not spell out numbers by default, and agents say "3 tests" a lot.
type ttsRequest struct {
	Text                   string `json:"text"`
	ModelID                string `json:"model_id"`
	ApplyTextNormalization string `json:"apply_text_normalization"`
}

// Speech is a streaming audio response. The caller must Close it.
type Speech struct {
	Body        io.ReadCloser
	ContentType string
	cancel      context.CancelFunc
}

// Close ends the stream and releases the request.
func (s *Speech) Close() error {
	err := s.Body.Close()
	s.cancel()
	return err
}

// Speak starts streaming text as audio in the configured voice. It returns once ElevenLabs has
// answered with headers; the audio then arrives through Body as it is generated. ctx bounds the
// whole stream (the browser hanging up cancels it).
func (c *Client) Speak(ctx context.Context, text string) (*Speech, error) {
	payload, err := json.Marshal(ttsRequest{Text: text, ModelID: TTSModel, ApplyTextNormalization: "on"})
	if err != nil {
		return nil, fmt.Errorf("encoding the speech request: %w", err)
	}
	endpoint := c.baseURL + "/v1/text-to-speech/" + url.PathEscape(c.voiceID) + "/stream?output_format=" + TTSOutputFormat
	ctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("building the speech request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", TTSContentType)
	// Only the wait for headers is bounded; the stream itself runs as long as the audio does.
	timer := time.AfterFunc(ttsHeaderTimeout, cancel)
	res, err := c.do(req, "speaking") //nolint:bodyclose // the body is the stream; Speech.Close closes it
	timer.Stop()
	if err != nil {
		cancel()
		return nil, err
	}
	contentType := res.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "audio/") {
		contentType = TTSContentType
	}
	return &Speech{Body: res.Body, ContentType: contentType, cancel: cancel}, nil
}

// ---- Shared ----------------------------------------------------------------------------

// do sends req with the API key and returns a 2xx response, or an APIError that holds only the
// status. A non-2xx body is drained and discarded unread.
func (c *Client) do(req *http.Request, doing string) (*http.Response, error) {
	req.Header.Set("xi-api-key", c.apiKey)
	res, err := c.http.Do(req) //nolint:gosec // the URL is the configured ElevenLabs base, not user input
	if err != nil {
		// The transport's error names the URL (which carries no secret) and the cause; it is not
		// passed on, so a proxy's error text cannot leak through either.
		return nil, &APIError{Status: 0, Doing: doing}
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		_ = res.Body.Close()
		return nil, &APIError{Status: res.StatusCode, Doing: doing}
	}
	return res, nil
}

// BaseType is a MIME type without its parameters, lower-cased: "audio/webm;codecs=opus" →
// "audio/webm".
func BaseType(contentType string) string {
	base, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(strings.TrimSpace(base))
}
