// The talk-mode service: what the HTTP handlers, the agent socket's `say` and the reply watcher
// call. It owns the ElevenLabs client, the speakable texts, the talk presence and the per-session
// rate limits, and publishes `say` events for the browsers to play.

package voice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
)

// Caps on what one browser session can spend. A minute of speech is a few requests at most, so
// these only bite on a stuck client or someone scripting the API with a stolen cookie.
const (
	// MaxRecordingBytes caps an upload. 60 s of speech is well under 1 MB in either browser's
	// default encoding (Opus in WebM, AAC in MP4, both at most 256 kbit/s); 2 MB leaves room
	// without letting an upload run to many minutes of billed transcription.
	MaxRecordingBytes = 2 << 20
	// MaxRecordingSeconds is the length the web app stops recording at.
	MaxRecordingSeconds = 60
	// sttPerMinute and ttsPerMinute are per browser session.
	sttPerMinute = 12
	ttsPerMinute = 30
	rateWindow   = time.Minute
)

// ErrLimited is returned when a session has used its quota for the window.
var ErrLimited = errors.New("voice: rate limited")

// ErrUnknownText is returned for a text id that does not exist or has expired.
var ErrUnknownText = errors.New("voice: unknown or expired text id")

// AcceptedAudio are the recording types /voice/stt takes: what MediaRecorder produces in Chrome
// and Firefox (WebM or Ogg with Opus) and Safari (MP4 with AAC), all of which ElevenLabs lists as
// supported input, so nothing is transcoded.
var AcceptedAudio = map[string]bool{
	"audio/webm": true, "audio/ogg": true, "audio/mp4": true, "audio/x-m4a": true, "audio/aac": true,
	"audio/mpeg": true, "audio/wav": true,
}

// Event sources and kinds of a `say` event.
const (
	SourceSay   = "say"   // an agent called `say` / `crowsnest say`
	SourceReply = "reply" // the orchestrator's final reply of a turn
	KindSpeak   = "speak" // fetch /voice/tts?text_id= and play it
	KindChime   = "chime" // a reply with no SAY line: play the short local chime
)

// SayEvent is the data of a `say` hub event.
type SayEvent struct {
	Kind   string    `json:"kind"`              // speak or chime
	Source string    `json:"source"`            // say or reply
	TextID string    `json:"text_id,omitempty"` // speak only
	Text   string    `json:"text,omitempty"`    // speak only: shown as a caption
	PaneID string    `json:"pane_id,omitempty"` // who said it
	At     time.Time `json:"at"`
}

// Service is talk mode. Build it with NewService.
type Service struct {
	client *Client
	texts  *Texts
	talk   *Talk
	hub    *hub.Hub
	logger *slog.Logger
	now    func() time.Time

	mu    sync.Mutex
	usage map[string][]time.Time // "<kind>:<session key>" → request times in the window
}

// Options configure a Service. Client is required.
type Options struct {
	Client *Client
	Hub    *hub.Hub // nil: say events are dropped
	Logger *slog.Logger
	Now    func() time.Time
}

// NewService returns a Service.
func NewService(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{
		client: opts.Client, texts: NewTexts(opts.Now), talk: NewTalk(opts.Now), hub: opts.Hub,
		logger: opts.Logger, now: opts.Now, usage: map[string][]time.Time{},
	}
}

// ---- Speaking ------------------------------------------------------------------------

// Say makes sentence speakable and tells the browsers. It is the agent socket's `say` and the
// target of a parsed SAY line; it returns the text id.
func (s *Service) Say(sentence, source, paneID string) string {
	sentence = cleanForSpeech(sentence)
	if sentence == "" {
		return ""
	}
	id := s.texts.Add(sentence)
	s.publish(SayEvent{Kind: KindSpeak, Source: source, TextID: id, Text: sentence, PaneID: paneID, At: s.now().UTC()})
	return id
}

// Chime tells the browsers a reply arrived with nothing to say aloud.
func (s *Service) Chime(paneID string) {
	s.publish(SayEvent{Kind: KindChime, Source: SourceReply, PaneID: paneID, At: s.now().UTC()})
}

// Speakable stores text for one browser to fetch (the Inbox's "Read aloud") without announcing
// it to every browser, and returns its id.
func (s *Service) Speakable(text string) string {
	return s.texts.Add(strings.TrimSpace(text))
}

func (s *Service) publish(event SayEvent) {
	if s.hub != nil {
		s.hub.Publish(hub.Event{Type: "say", Data: event})
	}
}

// Audio is a text's speech, streaming or from the cache. The caller must Close it.
type Audio struct {
	io.Reader
	ContentType string
	close       func() error
}

// Close releases the upstream stream, if any.
func (a *Audio) Close() error { return a.close() }

// Speech returns the audio for textID, charged to session. A text fetched before comes from
// the cache; otherwise ElevenLabs streams it, and a stream read to the end is kept for replays.
func (s *Service) Speech(ctx context.Context, session, textID string) (*Audio, error) {
	text, ok := s.texts.Get(textID)
	if !ok {
		return nil, ErrUnknownText
	}
	if audio, contentType, ok := s.texts.cachedAudio(textID); ok {
		return &Audio{Reader: bytes.NewReader(audio), ContentType: contentType, close: func() error { return nil }}, nil
	}
	if !s.allow("tts", session, ttsPerMinute) {
		return nil, ErrLimited
	}
	speech, err := s.client.Speak(ctx, text.Text)
	if err != nil {
		return nil, err
	}
	recorder := &cappedRecorder{limit: maxCachedAudio}
	reader := &teeUntilEOF{source: speech.Body, copy: recorder, done: func() {
		if !recorder.overflow {
			s.texts.storeAudio(textID, recorder.buf.Bytes(), speech.ContentType)
		}
	}}
	return &Audio{Reader: reader, ContentType: speech.ContentType, close: speech.Close}, nil
}

// ---- Listening -----------------------------------------------------------------------

// Transcribe turns one recording into text, charged to session.
func (s *Service) Transcribe(ctx context.Context, session string, audio []byte, contentType string) (string, error) {
	if !s.allow("stt", session, sttPerMinute) {
		return "", ErrLimited
	}
	return s.client.Transcribe(ctx, audio, contentType)
}

// ---- Talk presence -------------------------------------------------------------------

// Beat records a browser's talk-mode heartbeat and returns whether talk mode is on anywhere.
func (s *Service) Beat(clientID string, on bool) bool { return s.talk.Beat(clientID, on) }

// TalkOn reports whether any browser has talk mode on. The poke header (M8) and the voice
// prompt header carry it as `talk=on|off`.
func (s *Service) TalkOn() bool { return s.talk.On() }

// ---- Rate limit ----------------------------------------------------------------------

// allow counts one request of kind for session and reports whether it is within perMinute.
func (s *Service) allow(kind, session string, perMinute int) bool {
	key := kind + ":" + session
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	recent := s.usage[key][:0]
	for _, at := range s.usage[key] {
		if now.Sub(at) < rateWindow {
			recent = append(recent, at)
		}
	}
	if len(recent) >= perMinute {
		s.usage[key] = recent
		return false
	}
	s.usage[key] = append(recent, now)
	// Forget idle sessions so the map does not grow with every login.
	if len(s.usage) > 256 {
		for other, times := range s.usage {
			if len(times) == 0 || now.Sub(times[len(times)-1]) >= rateWindow {
				delete(s.usage, other)
			}
		}
	}
	return true
}

// RetryAfter is how long a limited session should wait, for the Retry-After header.
func RetryAfter() time.Duration { return rateWindow }

// ---- Stream helpers ------------------------------------------------------------------

// cappedRecorder keeps up to limit bytes and notes when more were offered.
type cappedRecorder struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (c *cappedRecorder) Write(p []byte) (int, error) {
	if c.overflow || c.buf.Len()+len(p) > c.limit {
		c.overflow = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

// teeUntilEOF copies what is read into copy, and calls done once the source ends cleanly.
type teeUntilEOF struct {
	source io.Reader
	copy   io.Writer
	done   func()
	ended  bool
}

func (t *teeUntilEOF) Read(p []byte) (int, error) {
	n, err := t.source.Read(p)
	if n > 0 {
		_, _ = t.copy.Write(p[:n])
	}
	if errors.Is(err, io.EOF) && !t.ended {
		t.ended = true
		t.done()
	}
	return n, err
}
