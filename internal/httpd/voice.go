// Talk-mode endpoints (DESIGN.md sections 10 and 13). Every route answers 404 when no ElevenLabs
// key is configured, so a browser cannot tell a disabled feature from a missing one and no
// handler below ever runs without the voice service.
//
//	POST /voice/stt              audio body (a MediaRecorder blob, up to five minutes) → {text}
//	GET  /voice/tts?text_id=     the audio of a server-held text, streamed as it is generated
//	POST /voice/read             {question_id} → {text_id}: a question's title and options, to hear
//	POST /voice/reply            {target, message_id} → {text_id, text}: a reply's SAY line, to hear
//	GET  /voice/settings         the voice, speed and model in force, and where each came from
//	PUT  /voice/settings         {voice_id, speed, model} → the same as GET
//	GET  /voice/voices           the ElevenLabs account's voices (cached for five minutes)
//	POST /voice/preview          {voice_id, speed, model} → {text_id, playback_rate}: a sample
//
// The voice prompt itself goes through POST /agents/{target}/prompt with "voice": true, which adds
// the voice header (conversation.go).

package httpd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/voice"
)

// codeVoiceUpstream marks a failure on ElevenLabs' side, so the UI can say "the voice service"
// rather than "the server".
const codeVoiceUpstream = "VOICE_UPSTREAM"

// VoiceHeader opens every prompt that came from speech (DESIGN.md 7.1), on a line of its own.
const VoiceHeader = "[crowsnest voice: transcribed speech, may contain mis-hearings]"

// sttUploadTimeout bounds reading one recording. The server has no overall read timeout (event
// streams and terminals stay open for hours), so without this a client that stops sending would
// hold its connection forever; with it, five minutes of audio (at most MaxRecordingBytes) has two
// minutes to arrive, which is under 0.6 Mbit/s for the biggest upload.
const sttUploadTimeout = 2 * time.Minute

func (s *Server) registerVoiceRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/voice/stt", s.requireAuth(s.requireVoice(s.handleSTT)))
	mux.Handle("GET /api/v1/voice/tts", s.requireAuth(s.requireVoice(s.handleTTS)))
	mux.Handle("POST /api/v1/voice/read", s.requireAuth(s.requireVoice(s.handleReadAloud)))
	mux.Handle("POST /api/v1/voice/reply", s.requireAuth(s.requireVoice(s.handleReplySay)))
	mux.Handle("GET /api/v1/voice/settings", s.requireAuth(s.requireVoice(s.handleVoiceSettings)))
	mux.Handle("PUT /api/v1/voice/settings", s.requireAuth(s.requireVoice(s.handleSaveVoiceSettings)))
	mux.Handle("GET /api/v1/voice/voices", s.requireAuth(s.requireVoice(s.handleVoices)))
	mux.Handle("POST /api/v1/voice/preview", s.requireAuth(s.requireVoice(s.handleVoicePreview)))
	mux.Handle("GET /api/v1/voice/agents", s.requireAuth(s.requireVoice(s.handleAgentVoices)))
	mux.Handle("PUT /api/v1/voice/agents/{agent}", s.requireAuth(s.requireVoice(s.handleSetAgentVoice)))
}

// requireVoice answers 404 when talk mode is off.
func (s *Server) requireVoice(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Voice == nil {
			writeError(w, http.StatusNotFound, codeNotFound, "Talk mode is off: start crowsnest with an ElevenLabs API key to use it.")
			return
		}
		next(w, r)
	})
}

// voiceSession is the rate-limit key for a request: a digest of the session cookie, or the
// client IP when there is no login (--no-auth). The digest keeps the token itself out of memory
// that outlives the request.
func voiceSession(r *http.Request) string {
	if token := sessionToken(r); token != "" {
		sum := sha256.Sum256([]byte(token))
		return hex.EncodeToString(sum[:8])
	}
	return "ip:" + clientIP(r)
}

// ---- Speech to text ------------------------------------------------------------------

// STTResponse is the body of POST /voice/stt.
type STTResponse struct {
	Text string `json:"text"`
}

func (s *Server) handleSTT(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	if !voice.AcceptedAudio[voice.BaseType(contentType)] {
		writeError(w, http.StatusUnsupportedMediaType, codeValidation, "Send the recording as audio/webm, audio/ogg or audio/mp4, as the browser's recorder makes it.")
		return
	}
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(sttUploadTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.internalError(w, r, "setting the upload deadline", err)
		return
	}
	audio, err := io.ReadAll(http.MaxBytesReader(w, r.Body, voice.MaxRecordingBytes))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		writeError(w, http.StatusRequestEntityTooLarge, codeValidation, "That recording is too long; keep it under five minutes.")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, codeValidation, "The recording did not arrive; try again.")
		return
	case len(audio) == 0:
		writeError(w, http.StatusBadRequest, codeValidation, "The recording was empty; hold the button while you speak.")
		return
	}
	text, err := s.deps.Voice.Transcribe(r.Context(), voiceSession(r), audio, contentType)
	if err != nil {
		s.voiceError(w, r, "transcribing", err)
		return
	}
	s.deps.Logger.Info("transcribed speech", "bytes", len(audio), "type", voice.BaseType(contentType), "chars", len(text))
	writeData(w, http.StatusOK, STTResponse{Text: text})
}

// ---- Text to speech ------------------------------------------------------------------

func (s *Server) handleTTS(w http.ResponseWriter, r *http.Request) {
	textID := r.URL.Query().Get("text_id")
	if textID == "" || len(textID) > 64 {
		writeError(w, http.StatusBadRequest, codeValidation, "text_id is required.")
		return
	}
	audio, err := s.deps.Voice.Speech(r.Context(), voiceSession(r), textID)
	if err != nil {
		s.voiceError(w, r, "speaking", err)
		return
	}
	defer func() { _ = audio.Close() }()
	header := w.Header()
	header.Set("Content-Type", audio.ContentType)
	header.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	s.streamAudio(w, audio)
}

// streamAudio copies audio to the browser, flushing each chunk so playback starts while
// ElevenLabs is still generating.
func (s *Server) streamAudio(w http.ResponseWriter, audio io.Reader) {
	controller := http.NewResponseController(w)
	buf := make([]byte, 16<<10)
	for {
		n, err := audio.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return // the browser hung up; Close cancels the upstream request
			}
			_ = controller.Flush()
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
				s.deps.Logger.Warn("speech stream ended early") // the cause may name the upstream; not logged
			}
			return
		}
	}
}

// ---- Read aloud ----------------------------------------------------------------------

// ReadAloudRequest is the body of POST /voice/read.
type ReadAloudRequest struct {
	QuestionID string `json:"question_id"`
}

// ReadAloudResponse carries the id to fetch from /voice/tts.
type ReadAloudResponse struct {
	TextID string `json:"text_id"`
}

func (s *Server) handleReadAloud(w http.ResponseWriter, r *http.Request) {
	if s.deps.Questions == nil {
		writeHerdrUnavailable(w)
		return
	}
	var body ReadAloudRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil || body.QuestionID == "" {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"question_id":"q_7"}.`)
		return
	}
	question, err := s.deps.Questions.Get(r.Context(), body.QuestionID)
	if err != nil {
		if errors.Is(err, questions.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "No question "+body.QuestionID+".")
			return
		}
		s.internalError(w, r, "reading a question aloud", err)
		return
	}
	writeData(w, http.StatusOK, ReadAloudResponse{TextID: s.deps.Voice.Speakable(spokenQuestion(question))})
}

// spokenQuestion is how a question sounds: its title, then its options in order.
func spokenQuestion(question questions.Question) string {
	var spoken strings.Builder
	spoken.WriteString(strings.TrimRight(question.Title, " .?!"))
	spoken.WriteString(".")
	if len(question.Options) > 0 {
		spoken.WriteString(" Options:")
		for i, option := range question.Options {
			spoken.WriteString(" " + strconv.Itoa(i+1) + ", " + strings.TrimRight(option.Label, " .") + ".")
		}
	}
	text := spoken.String()
	if len(text) > 2000 {
		text = text[:2000]
	}
	return text
}

// ---- Errors --------------------------------------------------------------------------

// voiceError maps a talk-mode failure to a message that says what to do. ElevenLabs' own error
// text is never passed on (voice.APIError does not even hold it).
func (s *Server) voiceError(w http.ResponseWriter, r *http.Request, doing string, err error) {
	switch {
	case errors.Is(err, voice.ErrUnknownText):
		writeError(w, http.StatusNotFound, codeNotFound, "That reply is no longer available to play.")
		return
	case errors.Is(err, voice.ErrBadSettings):
		writeError(w, http.StatusBadRequest, codeValidation, strings.TrimPrefix(err.Error(), voice.ErrBadSettings.Error()+": "))
		return
	case errors.Is(err, voice.ErrLimited):
		w.Header().Set("Retry-After", strconv.Itoa(int(voice.RetryAfter().Seconds())))
		writeError(w, http.StatusTooManyRequests, codeRateLimited, "Too many voice requests in the last minute; wait a moment and try again.")
		return
	}
	s.deps.Logger.Warn("voice request failed", "doing", doing, "path", r.URL.Path, "err", err) // APIError: status only
	switch {
	case errors.Is(err, voice.ErrUnauthorized):
		writeError(w, http.StatusBadGateway, codeVoiceUpstream, "ElevenLabs refused the API key; check ELEVENLABS_API_KEY on the server.")
	case errors.Is(err, voice.ErrRateLimited):
		w.Header().Set("Retry-After", "30")
		writeError(w, http.StatusServiceUnavailable, codeVoiceUpstream, "ElevenLabs is rate limiting this account; try again in a minute.")
	case errors.Is(err, voice.ErrRejected):
		writeError(w, http.StatusUnprocessableEntity, codeVoiceUpstream, "ElevenLabs could not process that; try recording again.")
	default:
		writeError(w, http.StatusBadGateway, codeVoiceUpstream, "Could not reach ElevenLabs; try again shortly.")
	}
}
