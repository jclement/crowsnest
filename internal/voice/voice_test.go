package voice_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/transcript"
	"github.com/jclement/crowsnest/internal/voice"
	"github.com/jclement/crowsnest/internal/voice/voicetest"
)

func newClient(t *testing.T, fake *voicetest.Server, key string) *voice.Client {
	t.Helper()
	client, err := voice.NewClient(voice.ClientOptions{BaseURL: fake.URL, APIKey: key, VoiceID: "voice123"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// ---- Client ----------------------------------------------------------------------------

func TestTranscribeSendsKeyModelAndFileType(t *testing.T) {
	fake := voicetest.New(t, "hello there")
	client := newClient(t, fake, voicetest.Key)
	for _, contentType := range []string{"audio/webm;codecs=opus", "audio/mp4"} {
		text, err := client.Transcribe(context.Background(), []byte("audio-bytes"), contentType)
		if err != nil || text != "hello there" {
			t.Fatalf("%s: %q %v", contentType, text, err)
		}
	}
	requests := fake.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests: %+v", requests)
	}
	for i, want := range []string{"audio/webm;codecs=opus", "audio/mp4"} {
		got := requests[i]
		if got.APIKey != voicetest.Key || got.ModelID != voice.STTModel || got.ContentType != want || got.FileBytes != len("audio-bytes") {
			t.Errorf("request %d: %+v", i, got)
		}
	}
}

func TestSpeakStreamsAudio(t *testing.T) {
	fake := voicetest.New(t, "")
	client := newClient(t, fake, voicetest.Key)
	speech, err := client.Speak(context.Background(), "All green.")
	if err != nil {
		t.Fatal(err)
	}
	audio, _ := io.ReadAll(speech.Body)
	_ = speech.Close()
	if string(audio) != string(voicetest.Audio) || speech.ContentType != "audio/mpeg" {
		t.Fatalf("audio %q type %q", audio, speech.ContentType)
	}
	got := fake.Requests()[0]
	if got.Path != "/v1/text-to-speech/voice123/stream" || got.Query != "output_format="+voice.TTSOutputFormat || got.APIKey != voicetest.Key {
		t.Fatalf("request: %+v", got)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(got.Body), &body); err != nil || body["text"] != "All green." || body["model_id"] != voice.TTSModel {
		t.Fatalf("body %s (%v)", got.Body, err)
	}
}

func TestErrorsCarryStatusOnlyNeverTheKeyOrBody(t *testing.T) {
	cases := []struct {
		status int
		class  error
	}{
		{http.StatusUnauthorized, voice.ErrUnauthorized},
		{http.StatusForbidden, voice.ErrUnauthorized},
		{http.StatusTooManyRequests, voice.ErrRateLimited},
		{http.StatusUnprocessableEntity, voice.ErrRejected},
		{http.StatusBadRequest, voice.ErrRejected},
		{http.StatusInternalServerError, voice.ErrUnreachable},
	}
	for _, c := range cases {
		fake := voicetest.New(t, "x")
		fake.FailWith(c.status)
		client := newClient(t, fake, voicetest.Key)
		_, sttErr := client.Transcribe(context.Background(), []byte("a"), "audio/webm")
		_, ttsErr := client.Speak(context.Background(), "a")
		for _, err := range []error{sttErr, ttsErr} {
			if !errors.Is(err, c.class) {
				t.Errorf("%d: %v is not %v", c.status, err, c.class)
			}
			if strings.Contains(err.Error(), voicetest.Key) || strings.Contains(err.Error(), "bad key") {
				t.Errorf("%d: error leaks upstream text: %v", c.status, err)
			}
		}
	}
}

func TestWrongKeyIsUnauthorized(t *testing.T) {
	fake := voicetest.New(t, "x")
	_, err := newClient(t, fake, "sk_wrong").Transcribe(context.Background(), []byte("a"), "audio/webm")
	if !errors.Is(err, voice.ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
}

func TestUnreachableServer(t *testing.T) {
	client, _ := voice.NewClient(voice.ClientOptions{BaseURL: "http://127.0.0.1:1", APIKey: voicetest.Key, VoiceID: "v"})
	_, err := client.Transcribe(context.Background(), []byte("a"), "audio/webm")
	if !errors.Is(err, voice.ErrUnreachable) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("got %v", err)
	}
}

func TestNewClientRefusesMissingKeyOrOddVoice(t *testing.T) {
	if _, err := voice.NewClient(voice.ClientOptions{VoiceID: "v"}); err == nil {
		t.Error("no key accepted")
	}
	if _, err := voice.NewClient(voice.ClientOptions{APIKey: "k", VoiceID: "../x?y"}); err == nil {
		t.Error("path-like voice accepted")
	}
}

// R2-L4: the key never follows a redirect, and a plain-http base off loopback is refused.
func TestKeyNeverFollowsARedirect(t *testing.T) {
	stolen := make(chan string, 4)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stolen <- r.Header.Get("xi-api-key")
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	client, err := voice.NewClient(voice.ClientOptions{BaseURL: redirector.URL, APIKey: voicetest.Key, VoiceID: "v"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Transcribe(context.Background(), []byte("audio"), "audio/webm"); err == nil {
		t.Fatal("a redirect was taken as success")
	}
	select {
	case key := <-stolen:
		t.Fatalf("the redirect target received a request (key %q)", key)
	default:
	}
	if _, err := voice.NewClient(voice.ClientOptions{BaseURL: "http://api.elevenlabs.io", APIKey: "k", VoiceID: "v"}); err == nil {
		t.Fatal("a plain-http base URL off loopback was accepted")
	}
}

// ---- SAY parsing -----------------------------------------------------------------------

func TestParseSay(t *testing.T) {
	cases := []struct {
		name, reply string
		kind        voice.SayKind
		sentence    string
	}{
		{"spoken", "SAY: Tests pass; PR is open.\n\nDetail here.", voice.SaySpeak, "Tests pass; PR is open."},
		{"leading blank lines", "\n\n  SAY:   All   done.  \n\nmore", voice.SaySpeak, "All done."},
		{"markdown stripped", "SAY: **Merged** `main` now.\n\nx", voice.SaySpeak, "Merged main now."},
		{"silent dash", "SAY: -\n\nNothing to hear.", voice.SaySilent, ""},
		{"silent em dash", "SAY: —", voice.SaySilent, ""},
		{"noop", "noop", voice.SaySilent, ""},
		{"noop with space", "  noop\n", voice.SaySilent, ""},
		{"no say line", "Here is the answer.\n\nSAY: not first", voice.SayNone, ""},
		{"empty say", "SAY:\n\nbody", voice.SayNone, ""},
		{"lower case is not the contract", "say: hello", voice.SayNone, ""},
		{"say only", "SAY: Short.", voice.SaySpeak, "Short."},
		{"empty", "", voice.SayNone, ""},
		{"noop in a sentence", "noop, nothing due", voice.SayNone, ""},
	}
	for _, c := range cases {
		got := voice.ParseSay(c.reply)
		if got.Kind != c.kind || got.Sentence != c.sentence {
			t.Errorf("%s: got %+v, want %s %q", c.name, got, c.kind, c.sentence)
		}
	}
}

func TestParseSayCapsLength(t *testing.T) {
	long := "SAY: " + strings.Repeat("word ", 200)
	got := voice.ParseSay(long)
	if got.Kind != voice.SaySpeak || len([]rune(got.Sentence)) > voice.MaxSpokenRunes+1 || !strings.HasSuffix(got.Sentence, "…") {
		t.Fatalf("got %d runes: %q", len([]rune(got.Sentence)), got.Sentence)
	}
}

// ---- Texts and talk --------------------------------------------------------------------

func TestTextsExpireAndAreUnguessable(t *testing.T) {
	now := time.Now()
	texts := voice.NewTexts(func() time.Time { return now })
	a, b := texts.Add("one"), texts.Add("two")
	if a == b || !strings.HasPrefix(a, "t_") || len(a) != 26 {
		t.Fatalf("ids %q %q", a, b)
	}
	if got, ok := texts.Get(a); !ok || got.Text != "one" {
		t.Fatal("missing text")
	}
	if _, ok := texts.Get("t_000000000000000000000000"); ok {
		t.Fatal("made-up id found")
	}
	now = now.Add(2 * time.Hour)
	if _, ok := texts.Get(a); ok {
		t.Fatal("expired text still served")
	}
}

func TestTextsAreBounded(t *testing.T) {
	texts := voice.NewTexts(nil)
	first := texts.Add("first")
	for range 200 {
		texts.Add("more")
	}
	if _, ok := texts.Get(first); ok {
		t.Fatal("oldest text was not evicted")
	}
}

func TestTalkPresenceExpires(t *testing.T) {
	now := time.Now()
	talk := voice.NewTalk(func() time.Time { return now })
	if talk.On() {
		t.Fatal("on with nobody")
	}
	if !talk.Beat("tab-aaaaaaaa", true) || !talk.On() {
		t.Fatal("beat did not turn talk on")
	}
	talk.Beat("tab-bbbbbbbb", true)
	if !talk.Beat("tab-aaaaaaaa", false) {
		t.Fatal("one tab leaving turned talk off for the other")
	}
	now = now.Add(voice.TalkExpiry + time.Second)
	if talk.On() {
		t.Fatal("a silent tab kept talk on")
	}
	if voice.TalkFlag(true) != "talk=on" || voice.TalkFlag(false) != "talk=off" {
		t.Fatal("talk flag words")
	}
}

func TestValidClientID(t *testing.T) {
	for id, want := range map[string]bool{"abcdefgh": true, "tab_1234-XYZ": true, "short": false, "has space x": false, strings.Repeat("a", 65): false} {
		if voice.ValidClientID(id) != want {
			t.Errorf("%q: want %v", id, want)
		}
	}
}

// ---- Service ---------------------------------------------------------------------------

func newService(t *testing.T, fake *voicetest.Server, now func() time.Time) (*voice.Service, *hub.Subscription) {
	t.Helper()
	events := hub.New()
	sub := events.Subscribe(16)
	t.Cleanup(sub.Close)
	return voice.NewService(voice.Options{Client: newClient(t, fake, voicetest.Key), Hub: events, Now: now}), sub
}

func nextSay(t *testing.T, sub *hub.Subscription) voice.SayEvent {
	t.Helper()
	select {
	case event := <-sub.Events():
		if event.Type != "say" {
			t.Fatalf("event %s", event.Type)
		}
		return event.Data.(voice.SayEvent)
	case <-time.After(2 * time.Second):
		t.Fatal("no say event")
	}
	return voice.SayEvent{}
}

func TestSayPublishesATextIDAndSpeechIsCachedForReplays(t *testing.T) {
	fake := voicetest.New(t, "")
	service, sub := newService(t, fake, nil)
	id := service.Say("  Build   is green. ", voice.SourceSay, "w1:p1")
	event := nextSay(t, sub)
	if event.Kind != voice.KindSpeak || event.TextID != id || event.Text != "Build is green." || event.PaneID != "w1:p1" {
		t.Fatalf("event %+v", event)
	}
	for range 2 {
		audio, err := service.Speech(context.Background(), "s1", id)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(audio)
		_ = audio.Close()
		if string(got) != string(voicetest.Audio) {
			t.Fatalf("audio %q", got)
		}
	}
	if n := len(fake.Requests()); n != 1 {
		t.Fatalf("ElevenLabs called %d times; the replay should come from the cache", n)
	}
	if _, err := service.Speech(context.Background(), "s1", "t_nope"); !errors.Is(err, voice.ErrUnknownText) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestTranscribeIsRateLimitedPerSession(t *testing.T) {
	fake := voicetest.New(t, "hi")
	now := time.Now()
	service, _ := newService(t, fake, func() time.Time { return now })
	var limited int
	for range 20 {
		if _, err := service.Transcribe(context.Background(), "s1", []byte("a"), "audio/webm"); errors.Is(err, voice.ErrLimited) {
			limited++
		}
	}
	if limited == 0 || limited == 20 {
		t.Fatalf("limited %d of 20", limited)
	}
	if _, err := service.Transcribe(context.Background(), "s2", []byte("a"), "audio/webm"); err != nil {
		t.Fatalf("another session was limited too: %v", err)
	}
	now = now.Add(time.Minute + time.Second)
	if _, err := service.Transcribe(context.Background(), "s1", []byte("a"), "audio/webm"); err != nil {
		t.Fatalf("still limited after the window: %v", err)
	}
}

// ---- Reply watcher ---------------------------------------------------------------------

type fakeConversation struct {
	mu       sync.Mutex
	messages []transcript.Message
	source   string
}

func (f *fakeConversation) Messages(context.Context, string, string, int) (transcript.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return transcript.Page{Source: f.source, Messages: append([]transcript.Message(nil), f.messages...)}, nil
}

func (f *fakeConversation) set(messages ...transcript.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = messages
}

func TestReplyWatcherSpeaksTheFinalReplyOnce(t *testing.T) {
	fake := voicetest.New(t, "")
	service, sub := newService(t, fake, nil)
	conversation := &fakeConversation{source: "transcript"}
	watcher := voice.NewReplyWatcher(service, conversation, "orchestrator")
	ctx := context.Background()
	later := time.Now().Add(time.Second)
	user := transcript.Message{ID: "m1", Kind: transcript.KindUser, Text: "status?", At: later}

	watcher.Status(ctx, false, "w1:p1")
	// The transcript lands after herdr reports idle: the change event finds it.
	conversation.set(user)
	watcher.Status(ctx, true, "w1:p1")
	conversation.set(user, transcript.Message{ID: "m2", Kind: transcript.KindAssistant, Text: "SAY: All good.\n\nDetail.", At: later})
	watcher.Changed(ctx, "w1:p1")
	event := nextSay(t, sub)
	if event.Kind != voice.KindSpeak || event.Text != "All good." || event.Source != voice.SourceReply {
		t.Fatalf("event %+v", event)
	}
	watcher.Changed(ctx, "w1:p1") // the same reply again: nothing
	watcher.Changed(ctx, "w9:p9") // another pane: nothing

	// Next turn: a reply without SAY chimes; a SAY: - reply is silent.
	watcher.Status(ctx, false, "w1:p1")
	conversation.set(user, transcript.Message{ID: "m3", Kind: transcript.KindAssistant, Text: "Plain answer.", At: later})
	watcher.Status(ctx, true, "w1:p1")
	if event := nextSay(t, sub); event.Kind != voice.KindChime {
		t.Fatalf("event %+v", event)
	}
	watcher.Status(ctx, false, "w1:p1")
	conversation.set(user, transcript.Message{ID: "m4", Kind: transcript.KindAssistant, Text: "SAY: -\n\nx", At: later},
		transcript.Message{ID: "m5", Kind: transcript.KindThinking, At: later})
	watcher.Status(ctx, true, "w1:p1")
	select {
	case event := <-sub.Events():
		t.Fatalf("unexpected %+v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestReplyWatcherIgnoresHistoryAndToolEndings(t *testing.T) {
	fake := voicetest.New(t, "")
	service, sub := newService(t, fake, nil)
	conversation := &fakeConversation{source: "transcript"}
	old := time.Now().Add(-time.Hour)
	conversation.set(transcript.Message{ID: "old", Kind: transcript.KindAssistant, Text: "SAY: Stale.", At: old})
	watcher := voice.NewReplyWatcher(service, conversation, "orchestrator")
	ctx := context.Background()
	watcher.Status(ctx, true, "w1:p1")
	watcher.Status(ctx, false, "w1:p1")
	conversation.set(transcript.Message{ID: "t1", Kind: transcript.KindTool, At: time.Now().Add(time.Second)})
	watcher.Status(ctx, true, "w1:p1")
	select {
	case event := <-sub.Events():
		t.Fatalf("unexpected %+v", event)
	case <-time.After(100 * time.Millisecond):
	}
}
