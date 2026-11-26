package voice_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	speech, err := client.Speak(context.Background(), "All green.", voice.SpeakOptions{})
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
	var body map[string]any
	if err := json.Unmarshal([]byte(got.Body), &body); err != nil || body["text"] != "All green." || body["model_id"] != voice.TTSModel || body["voice_settings"] != nil {
		t.Fatalf("body %s (%v)", got.Body, err)
	}
}

func TestSpeakWithVoiceModelAndSpeed(t *testing.T) {
	fake := voicetest.New(t, "")
	client := newClient(t, fake, voicetest.Key)
	speech, err := client.Speak(context.Background(), "Hi.", voice.SpeakOptions{VoiceID: "otherVoice99", ModelID: voice.TTSQualityModel, Speed: 1.4})
	if err != nil {
		t.Fatal(err)
	}
	_ = speech.Close()
	got := fake.Requests()[0]
	if got.Path != "/v1/text-to-speech/otherVoice99/stream" {
		t.Fatalf("path %s", got.Path)
	}
	// 1.4 is past what the API takes: it is asked for its maximum, and the browser plays the rest.
	if !strings.Contains(got.Body, `"model_id":"eleven_multilingual_v2"`) || !strings.Contains(got.Body, `"voice_settings":{"speed":1.2}`) {
		t.Fatalf("body %s", got.Body)
	}
}

func TestVoicesWalksEveryPage(t *testing.T) {
	fake := voicetest.New(t, "")
	voices, err := newClient(t, fake, voicetest.Key).Voices(context.Background())
	if err != nil || len(voices) != 2 || voices[0].Name != "Rachel" || voices[1].VoiceID != voicetest.VoiceBrian || voices[0].Labels["accent"] != "american" {
		t.Fatalf("voices %+v (%v)", voices, err)
	}
	requests := fake.Requests()
	if len(requests) != 2 || !strings.Contains(requests[1].Query, "next_page_token=page2") || !strings.Contains(requests[0].Query, "page_size=100") {
		t.Fatalf("requests %+v", requests)
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
		_, ttsErr := client.Speak(context.Background(), "a", voice.SpeakOptions{})
		_, voicesErr := client.Voices(context.Background())
		for _, err := range []error{sttErr, ttsErr, voicesErr} {
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

// ---- Texts -----------------------------------------------------------------------------

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
	for i := range 200 {
		texts.Add(fmt.Sprint("more ", i))
	}
	if _, ok := texts.Get(first); ok {
		t.Fatal("oldest text was not evicted")
	}
}

func TestTheSameSentenceKeepsItsID(t *testing.T) {
	texts := voice.NewTexts(nil)
	a := texts.Add("All green.")
	if b := texts.Add("All green."); b != a {
		t.Fatalf("a repeated sentence got a new id: %q %q", a, b)
	}
	preview := texts.AddWithOverride("All green.", voice.Effective{VoiceID: "x"})
	if preview == a {
		t.Fatal("a preview shared an ordinary text's id")
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

// ---- Settings --------------------------------------------------------------------------

// memKV is a settings store in memory.
type memKV struct {
	mu     sync.Mutex
	values map[string]string
}

func (m *memKV) GetKV(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[key]
	if !ok {
		return "", errors.New("not found")
	}
	return value, nil
}

func (m *memKV) SetKV(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values == nil {
		m.values = map[string]string{}
	}
	m.values[key] = value
	return nil
}

func TestSettingsDefaultsSaveAndApplyToSpeech(t *testing.T) {
	fake := voicetest.New(t, "")
	kv := &memKV{}
	service := voice.NewService(voice.Options{Client: newClient(t, fake, voicetest.Key), Settings: kv, DefaultVoiceSource: "CROWSNEST_ELEVENLABS_VOICE"})
	ctx := context.Background()
	got := service.Settings(ctx)
	if got.VoiceID != "voice123" || got.VoiceStored || got.Speed != 1 || got.Model != voice.ModelFast || got.PlaybackRate() != 1 {
		t.Fatalf("defaults %+v", got)
	}
	if service.DefaultVoiceSource() != "CROWSNEST_ELEVENLABS_VOICE" {
		t.Fatalf("source %q", service.DefaultVoiceSource())
	}
	id := service.Say("Hello.", voice.SourceSay, "")
	read := func() *voice.Audio {
		t.Helper()
		audio, err := service.Speech(ctx, "s1", id)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(audio)
		_ = audio.Close()
		return audio
	}
	read()
	saved, err := service.SaveSettings(ctx, voice.Settings{VoiceID: voicetest.VoiceBrian, Speed: 1.5, Model: voice.ModelQuality})
	if err != nil || !saved.VoiceStored || saved.VoiceID != voicetest.VoiceBrian {
		t.Fatalf("saved %+v %v", saved, err)
	}
	// The cached audio was made in the old voice: the next fetch asks again, in the new one.
	audio := read()
	if audio.PlaybackRate != 1.25 {
		t.Fatalf("playback rate %v, want 1.25 (1.5 = the API's 1.2 x 1.25)", audio.PlaybackRate)
	}
	requests := fake.Requests()
	if len(requests) != 2 || requests[1].Path != "/v1/text-to-speech/"+voicetest.VoiceBrian+"/stream" ||
		!strings.Contains(requests[1].Body, `"speed":1.2`) || !strings.Contains(requests[1].Body, voice.TTSQualityModel) {
		t.Fatalf("requests %+v", requests)
	}
	read() // same variant again: cached
	if n := len(fake.Requests()); n != 2 {
		t.Fatalf("ElevenLabs called %d times", n)
	}
	// Back to the default voice.
	if back, _ := service.SaveSettings(ctx, voice.Settings{}); back.VoiceID != "voice123" || back.VoiceStored {
		t.Fatalf("reset %+v", back)
	}
}

func TestSettingsValidation(t *testing.T) {
	for _, bad := range []voice.Settings{
		{VoiceID: "../etc"}, {VoiceID: "a b"}, {Speed: 0.5}, {Speed: 2}, {Model: "turbo"},
	} {
		if err := bad.Validate(); !errors.Is(err, voice.ErrBadSettings) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if err := (voice.Settings{VoiceID: voicetest.VoiceRachel, Speed: 0.8, Model: voice.ModelFast}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSpeedSplitsBetweenTheAPIAndTheBrowser(t *testing.T) {
	cases := []struct{ speed, api, rate float64 }{
		{1, 1, 1}, {0.8, 0.8, 1}, {1.2, 1.2, 1}, {1.5, 1.2, 1.25}, {1.35, 1.2, 1.125},
	}
	for _, c := range cases {
		if got := voice.APISpeed(c.speed); got != c.api {
			t.Errorf("API speed for %v: %v, want %v", c.speed, got, c.api)
		}
		if got := voice.PlaybackRate(c.speed); got != c.rate {
			t.Errorf("playback rate for %v: %v, want %v", c.speed, got, c.rate)
		}
	}
}

func TestUnreadableStoredSettingsAreIgnored(t *testing.T) {
	fake := voicetest.New(t, "")
	kv := &memKV{values: map[string]string{"voice.settings": `{"voice_id":"../../x"}`}}
	service := voice.NewService(voice.Options{Client: newClient(t, fake, voicetest.Key), Settings: kv})
	if got := service.Settings(context.Background()); got.VoiceID != "voice123" {
		t.Fatalf("a bad stored voice was used: %+v", got)
	}
}

func TestVoicesAreCached(t *testing.T) {
	fake := voicetest.New(t, "")
	now := time.Now()
	service, _ := newService(t, fake, func() time.Time { return now })
	for range 3 {
		if voices, err := service.Voices(context.Background()); err != nil || len(voices) != 2 {
			t.Fatalf("%v %v", voices, err)
		}
	}
	if n := len(fake.Requests()); n != 2 { // one walk: two pages
		t.Fatalf("%d requests", n)
	}
	now = now.Add(6 * time.Minute)
	_, _ = service.Voices(context.Background())
	if n := len(fake.Requests()); n != 4 {
		t.Fatalf("cache did not expire: %d requests", n)
	}
}

func TestPreviewAndReplySay(t *testing.T) {
	fake := voicetest.New(t, "")
	service, _ := newService(t, fake, nil)
	ctx := context.Background()
	id := service.Preview(voice.Effective{VoiceID: voicetest.VoiceBrian, Speed: 0.8, Model: voice.ModelFast})
	audio, err := service.Speech(ctx, "s1", id)
	if err != nil {
		t.Fatal(err)
	}
	_ = audio.Close()
	if got := fake.Requests()[0]; got.Path != "/v1/text-to-speech/"+voicetest.VoiceBrian+"/stream" || !strings.Contains(got.Body, voice.PreviewSentence) {
		t.Fatalf("preview request %+v", got)
	}
	if _, _, ok := service.ReplySay("Plain answer."); ok {
		t.Fatal("a reply with no SAY line was made playable")
	}
	replyID, sentence, ok := service.ReplySay("SAY: **All** green.\n\nDetail")
	if !ok || sentence != "All green." {
		t.Fatalf("%q %v", sentence, ok)
	}
	if again, _, _ := service.ReplySay("SAY: All green.\n\nOther detail"); again != replyID {
		t.Fatal("the same sentence got a second id (and would be synthesised twice)")
	}
}
