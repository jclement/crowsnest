// Package voicetest is a fake ElevenLabs API for tests: an httptest server that answers the
// endpoints crowsnest calls (speech-to-text, text-to-speech streaming and the voice list),
// checks the API key header, records what it was sent, and can be told to fail with a status. The voice package's tests and the HTTP handler tests both use it.
package voicetest

import (
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Key is the API key the fake accepts. It is deliberately distinctive, so a test can search any
// response or log for it and be sure it was never echoed.
const Key = "sk_fake_elevenlabs_key_7f3a9c"

// Audio is the "mp3" the fake streams back.
var Audio = []byte("ID3\x04fake-mp3-audio-frames")

// Request is one call the fake received.
type Request struct {
	Path        string
	Query       string
	APIKey      string
	ContentType string // of the request, or of the uploaded file for STT
	ModelID     string
	FileBytes   int
	Body        string // TTS JSON body
}

// Server is the fake. Set Status to make every call fail with it (it answers with an error body
// that quotes the key, to prove crowsnest never passes an upstream body on).
type Server struct {
	*httptest.Server
	mu         sync.Mutex
	requests   []Request
	status     int
	transcript string
}

// New starts a fake that transcribes everything as transcript.
func New(t *testing.T, transcript string) *Server {
	t.Helper()
	fake := &Server{transcript: transcript}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.Close)
	return fake
}

// FailWith makes later calls answer status (0 restores success).
func (f *Server) FailWith(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

// Requests returns what the fake received so far.
func (f *Server) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

func (f *Server) serve(w http.ResponseWriter, r *http.Request) {
	record := Request{Path: r.URL.Path, Query: r.URL.RawQuery, APIKey: r.Header.Get("xi-api-key"), ContentType: r.Header.Get("Content-Type")}
	switch {
	case r.URL.Path == "/v1/speech-to-text":
		f.readForm(r, &record)
	case strings.HasPrefix(r.URL.Path, "/v1/text-to-speech/"):
		body, _ := io.ReadAll(r.Body)
		record.Body = string(body)
	}
	f.mu.Lock()
	f.requests = append(f.requests, record)
	status, transcript := f.status, f.transcript
	f.mu.Unlock()

	switch {
	case record.APIKey != Key:
		writeError(w, http.StatusUnauthorized)
	case status != 0:
		writeError(w, status)
	case r.URL.Path == "/v1/speech-to-text":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"language_code":"en","language_probability":0.99,"text":"`+transcript+`","words":[]}`)
	case strings.HasPrefix(r.URL.Path, "/v1/text-to-speech/") && strings.HasSuffix(r.URL.Path, "/stream"):
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(Audio)
	case r.URL.Path == "/v2/voices":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, voicesPage(r.URL.Query().Get("next_page_token")))
	default:
		http.NotFound(w, r)
	}
}

// readForm pulls model_id and the file out of a multipart STT upload.
func (f *Server) readForm(r *http.Request, record *Request) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	for {
		part, err := reader.NextPart()
		if err != nil {
			return
		}
		data, _ := io.ReadAll(part)
		switch part.FormName() {
		case "model_id":
			record.ModelID = string(data)
		case "file":
			record.ContentType, record.FileBytes = part.Header.Get("Content-Type"), len(data)
		}
	}
}

// writeError answers like an upstream that echoes the credential it was sent, the worst case.
func writeError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"detail":{"status":"error","message":"bad key `+Key+`"}}`)
}

// Voices are the fake account's voices, over two pages so paging is exercised.
const (
	VoiceRachel = "21m00Tcm4TlvDq8ikWAM"
	VoiceBrian  = "nPczCjzI2devNBz1zQrb"
)

func voicesPage(token string) string {
	if token == "page2" {
		return `{"voices":[{"voice_id":"` + VoiceBrian + `","name":"Brian","category":"premade","labels":{"accent":"american","gender":"male"},"preview_url":"https://example.invalid/b.mp3"}],"has_more":false,"next_page_token":null}`
	}
	return `{"voices":[{"voice_id":"` + VoiceRachel + `","name":"Rachel","category":"premade","description":"Calm","labels":{"accent":"american","gender":"female","use_case":"narration"}}],"has_more":true,"next_page_token":"page2","total_count":2}`
}
