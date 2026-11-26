// The talk-mode service: what the HTTP handlers, the agent socket's `say` and the reply watcher
// call. It owns the ElevenLabs client, the speakable texts, the voice settings and the
// per-session rate limits, and publishes `say` events for the browsers to play.
//
// Nothing here knows which browsers are listening. A `say` event costs nothing: speech is
// synthesised only when a browser with speak mode on (or a Play button) fetches the text's
// audio, and a finished synthesis is cached, so nobody listening means no ElevenLabs spend.

package voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
)

// Caps on what one browser session can spend. A recording is one request however long it is, so
// these only bite on a stuck client or someone scripting the API with a stolen cookie.
const (
	// MaxRecordingSeconds is the length the web app stops recording at (Jeff: "max time = 5
	// minutes for voice input"). The web app's MAX_RECORDING_MS must match.
	MaxRecordingSeconds = 300
	// RecordingBitsPerSecond is the bit rate the web app asks MediaRecorder for.
	RecordingBitsPerSecond = 64_000
	// MaxRecordingBytes caps an upload: five minutes at up to 192 kbit/s, three times the rate
	// asked for, plus 1 MiB for the container. Chrome's Opus in WebM keeps to the asked rate;
	// Safari's AAC in MP4 may not (it has been seen to record at 128 kbit/s or more whatever it
	// is asked), so the cap is sized for that rather than for Chrome. Five minutes at 64 kbit/s is
	// 2.4 MB; at 192 it is 7.2 MB.
	MaxRecordingBytes = MaxRecordingSeconds*192_000/8 + 1<<20
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
	client   *Client
	texts    *Texts
	settings SettingsStore
	// voiceSource names where the default voice came from (config's source), for the sheet.
	voiceSource string
	hub         *hub.Hub
	logger      *slog.Logger
	now         func() time.Time

	mu    sync.Mutex
	usage map[string][]time.Time // "<kind>:<session key>" → request times in the window

	voicesMu  sync.Mutex
	voices    []VoiceInfo
	voicesAt  time.Time
	voicesErr error
}

// Options configure a Service. Client is required.
type Options struct {
	Client   *Client
	Settings SettingsStore // nil: settings cannot be changed; the defaults are used
	// DefaultVoiceSource names where --elevenlabs-voice's value came from ("default", the flag,
	// the environment variable or the config file), shown by the settings sheet.
	DefaultVoiceSource string
	Hub                *hub.Hub // nil: say events are dropped
	Logger             *slog.Logger
	Now                func() time.Time
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
		client: opts.Client, texts: NewTexts(opts.Now), settings: opts.Settings, voiceSource: opts.DefaultVoiceSource, hub: opts.Hub,
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
	// PlaybackRate is the speed the browser should play it at, beyond what ElevenLabs was asked.
	PlaybackRate float64
	close        func() error
}

// Close releases the upstream stream, if any.
func (a *Audio) Close() error { return a.close() }

// Speech returns the audio for textID, charged to session, in the voice, model and speed in
// force (or the text's own override, for a preview). A text fetched before in the same variant
// comes from the cache; otherwise ElevenLabs streams it, and a stream read to the end is kept for
// replays. PlaybackRate is what the browser should play it at (settings.go).
func (s *Service) Speech(ctx context.Context, session, textID string) (*Audio, error) {
	text, ok := s.texts.Get(textID)
	if !ok {
		return nil, ErrUnknownText
	}
	how := s.Settings(ctx)
	if text.Override != nil {
		how = *text.Override
	}
	options := how.SpeakOptions()
	variant := fmt.Sprintf("%s|%s|%.2f", options.VoiceID, options.ModelID, options.Speed)
	if audio, contentType, ok := s.texts.cachedAudio(textID, variant); ok {
		return &Audio{Reader: bytes.NewReader(audio), ContentType: contentType, PlaybackRate: how.PlaybackRate(), close: func() error { return nil }}, nil
	}
	if !s.allow("tts", session, ttsPerMinute) {
		return nil, ErrLimited
	}
	speech, err := s.client.Speak(ctx, text.Text, options)
	if err != nil {
		return nil, err
	}
	recorder := &cappedRecorder{limit: maxCachedAudio}
	reader := &teeUntilEOF{source: speech.Body, copy: recorder, done: func() {
		if !recorder.overflow {
			s.texts.storeAudio(textID, variant, recorder.buf.Bytes(), speech.ContentType)
		}
	}}
	return &Audio{Reader: reader, ContentType: speech.ContentType, PlaybackRate: how.PlaybackRate(), close: speech.Close}, nil
}

// DefaultVoice is the configured voice id, used when no voice is stored.
func (s *Service) DefaultVoice() string { return s.client.DefaultVoice() }

// DefaultVoiceSource names where the default voice was configured.
func (s *Service) DefaultVoiceSource() string {
	if s.voiceSource == "" {
		return "default"
	}
	return s.voiceSource
}

// PreviewSentence is what a voice preview says.
const PreviewSentence = "This is how crowsnest will sound. Tests pass, and I opened the pull request."

// Preview makes a speakable text that plays the sample sentence the way how says, for the
// settings sheet's Preview button, and returns its id.
func (s *Service) Preview(how Effective) string {
	return s.texts.AddWithOverride(PreviewSentence, how)
}

// ReplySay makes an assistant reply's SAY line speakable on demand (Chat's play button) and
// returns its id and sentence, or ok false when the reply has nothing to say aloud.
func (s *Service) ReplySay(reply string) (id, sentence string, ok bool) {
	result := ParseSay(reply)
	if result.Kind != SaySpeak {
		return "", "", false
	}
	return s.texts.Add(result.Sentence), result.Sentence, true
}

// ---- Voices --------------------------------------------------------------------------

// voicesTTL is how long the account's voice list is reused: it changes when someone adds a voice
// on ElevenLabs' site, which is rare, and the sheet is opened often.
const voicesTTL = 5 * time.Minute

// Voices lists the account's voices, from a cache of at most voicesTTL. A failure is cached for
// a few seconds only, so a retry soon after works.
func (s *Service) Voices(ctx context.Context) ([]VoiceInfo, error) {
	s.voicesMu.Lock()
	defer s.voicesMu.Unlock()
	age := s.now().Sub(s.voicesAt)
	if s.voicesErr == nil && s.voices != nil && age < voicesTTL {
		return s.voices, nil
	}
	if s.voicesErr != nil && age < 5*time.Second {
		return nil, s.voicesErr
	}
	voices, err := s.client.Voices(ctx)
	s.voices, s.voicesErr, s.voicesAt = voices, err, s.now()
	if voices == nil && err == nil {
		s.voices = []VoiceInfo{}
	}
	return s.voices, err
}

// ---- Listening -----------------------------------------------------------------------

// Transcribe turns one recording into text, charged to session.
func (s *Service) Transcribe(ctx context.Context, session string, audio []byte, contentType string) (string, error) {
	if !s.allow("stt", session, sttPerMinute) {
		return "", ErrLimited
	}
	return s.client.Transcribe(ctx, audio, contentType)
}

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
