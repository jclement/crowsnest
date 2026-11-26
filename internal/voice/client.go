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
// `file`, transcript in `text`; files up to 5 GB and 15 hours, so crowsnest's own caps are the
// ones that bind), POST /v1/text-to-speech/{voice_id}/stream (JSON `text`, `model_id` and
// `voice_settings.speed`, `output_format` as a query parameter, the audio streamed back) and
// GET /v2/voices (the account's voices, paged). They were then checked once with a real key
// (REPORT.md of the voice-fix branch).
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
	"net/netip"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

// Model and format choices. Each is a named constant so a change is one line and shows in review.
const (
	// STTModel is ElevenLabs' batch transcription model. scribe_v3 is the current one on the
	// models page; checked with a real key on 2026-11-26 (it punctuates where scribe_v2 did not).
	STTModel = "scribe_v3"
	// TTSModel is the default text-to-speech model, the low-latency one: a spoken reply should
	// start quickly, and the sentences are short. Settings can pick the higher-quality model
	// instead (settings.go, TTSModels).
	TTSModel = "eleven_flash_v2_5"
	// TTSOutputFormat is MP3, which every browser's <audio> plays (Safari has no Opus-in-Ogg) and
	// starts playing from the first frames, so playback begins while ElevenLabs is still
	// generating. 64 kbit/s is plenty for one voice and halves the bytes the browser must buffer
	// before it starts, against the API's default of 128.
	TTSOutputFormat = "mp3_44100_64"
	// TTSContentType is what an mp3 stream is served as when ElevenLabs does not say.
	TTSContentType = "audio/mpeg"
)

// Timeouts. Transcribing the longest recording (MaxRecordingSeconds, five minutes) takes
// ElevenLabs some seconds to tens of seconds; three minutes leaves room for a slow day without
// letting a hung upstream hold the request forever. The TTS timeout covers only the response
// headers, because the body is streamed to the browser for as long as it takes.
const (
	sttTimeout       = 3 * time.Minute
	voicesTimeout    = 15 * time.Second
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
	voiceID string // the configured default voice
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
	if !secureBase(opts.BaseURL) {
		return nil, errors.New("voice: the ElevenLabs base URL must be https (plain http only to a loopback address); the key travels with every request")
	}
	// R2-L4: never follow a redirect. Go's client copies custom headers (xi-api-key among them)
	// to the redirect target, and a 307 or 308 resends the body (a recording, or text). The
	// copy keeps the caller's own client untouched.
	client := http.Client{}
	if opts.HTTP != nil {
		client = *opts.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{baseURL: strings.TrimRight(opts.BaseURL, "/"), apiKey: opts.APIKey, voiceID: opts.VoiceID, http: &client}, nil
}

// secureBase reports whether base is https, or http to a loopback host (a local fake in tests).
// config refuses the same at start-up; this keeps the client safe on its own.
func secureBase(base string) bool {
	parsed, err := url.Parse(base)
	if err != nil {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	host := parsed.Hostname()
	addr, err := netip.ParseAddr(host)
	return parsed.Scheme == "http" && (strings.EqualFold(host, "localhost") || (err == nil && addr.IsLoopback()))
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
	Text                   string            `json:"text"`
	ModelID                string            `json:"model_id"`
	ApplyTextNormalization string            `json:"apply_text_normalization"`
	VoiceSettings          *ttsVoiceSettings `json:"voice_settings,omitempty"`
}

// ttsVoiceSettings overrides the voice's stored settings for one call. Only speed is set; the
// rest stay the voice's own (a partial object is accepted, as the real API answered).
type ttsVoiceSettings struct {
	Speed float64 `json:"speed"`
}

// SpeakOptions choose how one text is spoken. Zero fields take the client's defaults.
type SpeakOptions struct {
	VoiceID string  // empty: the configured voice
	ModelID string  // empty: TTSModel
	Speed   float64 // 0 or 1: the voice's own pace; otherwise within APISpeedMin..APISpeedMax
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
func (c *Client) Speak(ctx context.Context, text string, opts SpeakOptions) (*Speech, error) {
	voiceID, model := opts.VoiceID, opts.ModelID
	if voiceID == "" {
		voiceID = c.voiceID
	}
	if model == "" {
		model = TTSModel
	}
	request := ttsRequest{Text: text, ModelID: model, ApplyTextNormalization: "on"}
	if opts.Speed != 0 && opts.Speed != 1 {
		request.VoiceSettings = &ttsVoiceSettings{Speed: clampAPISpeed(opts.Speed)}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encoding the speech request: %w", err)
	}
	endpoint := c.baseURL + "/v1/text-to-speech/" + url.PathEscape(voiceID) + "/stream?output_format=" + TTSOutputFormat
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

// DefaultVoice is the voice id the client was configured with (--elevenlabs-voice).
func (c *Client) DefaultVoice() string { return c.voiceID }

// ---- Voices ----------------------------------------------------------------------------

// VoiceInfo is one voice the account can use, as the settings sheet lists it.
type VoiceInfo struct {
	VoiceID     string            `json:"voice_id"`
	Name        string            `json:"name"`
	Category    string            `json:"category,omitempty"` // premade, cloned, generated, professional
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"` // accent, gender, age, use_case, …
}

// voicesPage is GET /v2/voices's answer, the fields crowsnest reads.
type voicesPage struct {
	Voices        []VoiceInfo `json:"voices"`
	HasMore       bool        `json:"has_more"`
	NextPageToken *string     `json:"next_page_token"`
}

const (
	// voicesPageSize is the API's maximum page.
	voicesPageSize = 100
	// maxVoicePages bounds the walk: 500 voices is more than any account's picker needs.
	maxVoicePages = 5
	// maxVoicesResponse caps one page's JSON.
	maxVoicesResponse = 4 << 20
)

// Voices lists the voices the account can use (its own and the premade ones), sorted by name.
func (c *Client) Voices(ctx context.Context) ([]VoiceInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, voicesTimeout)
	defer cancel()
	var all []VoiceInfo
	token := ""
	for range maxVoicePages {
		query := url.Values{"page_size": {fmt.Sprint(voicesPageSize)}, "sort": {"name"}, "sort_direction": {"asc"}, "include_total_count": {"false"}}
		if token != "" {
			query.Set("next_page_token", token)
		}
		page, err := c.voicesPage(ctx, query)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Voices...)
		if !page.HasMore || page.NextPageToken == nil || *page.NextPageToken == "" {
			break
		}
		token = *page.NextPageToken
	}
	return all, nil
}

func (c *Client) voicesPage(ctx context.Context, query url.Values) (voicesPage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v2/voices?"+query.Encode(), nil)
	if err != nil {
		return voicesPage{}, fmt.Errorf("building the voices request: %w", err)
	}
	res, err := c.do(req, "listing voices")
	if err != nil {
		return voicesPage{}, err
	}
	defer func() { _ = res.Body.Close() }()
	var page voicesPage
	if err := json.NewDecoder(io.LimitReader(res.Body, maxVoicesResponse)).Decode(&page); err != nil {
		return voicesPage{}, &APIError{Status: http.StatusBadGateway, Doing: "listing voices"}
	}
	return page, nil
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
