// Tests for the talk-mode routes against a fake ElevenLabs (voicetest): 404 when voice is off,
// the API key sent upstream and never echoed, upload caps and types, rate limits, the error
// mapping, text ids, read-aloud, the voice settings, voices and previews, a reply's SAY line on
// demand, and the voice header on prompts.

package httpd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
	"github.com/jclement/crowsnest/internal/voice"
	"github.com/jclement/crowsnest/internal/voice/voicetest"
)

// voiceHarness: auth on, talk mode on against a fake ElevenLabs, one open question.
func voiceHarness(t *testing.T) (*harness, *voicetest.Server, *voice.Service, *http.Cookie) {
	t.Helper()
	fake := voicetest.New(t, "ship the release")
	client, err := voice.NewClient(voice.ClientOptions{BaseURL: fake.URL, APIKey: voicetest.Key, VoiceID: "voice123"})
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := voice.NewService(voice.Options{Client: client, Settings: db, DefaultVoiceSource: "default"})
	inbox := questions.New(questions.Options{Store: db})
	if _, err := inbox.Ask(context.Background(), questions.AskInput{
		Title: "Ship the migration tonight?", Kind: questions.KindChoice, Recommend: "yes",
		Options: []questions.Option{{Key: "yes", Label: "Ship it"}, {Key: "no", Label: "Wait for Monday"}},
	}, questions.Caller{}); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, harnessOptions{voice: service, questions: inbox})
	return h, fake, service, h.login()
}

func audioRequest(contentType string, body []byte) *http.Request {
	r := request(http.MethodPost, "/api/v1/voice/stt", "")
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Type", contentType)
	return r
}

// assertNoKey fails if the API key appears anywhere a client or the log could see it.
func assertNoKey(t *testing.T, h *harness, responses ...string) {
	t.Helper()
	for _, text := range append(responses, h.logs.String()) {
		if strings.Contains(text, voicetest.Key) || strings.Contains(text, "bad key") {
			t.Fatalf("the API key or upstream error text leaked: %s", text)
		}
	}
}

func TestVoiceRoutesAre404WhenOff(t *testing.T) {
	h := newHarness(t, harnessOptions{noAuth: true})
	for _, r := range []*http.Request{
		audioRequest("audio/webm", []byte("a")),
		request(http.MethodGet, "/api/v1/voice/tts?text_id=t_1", ""),
		request(http.MethodPost, "/api/v1/voice/read", `{"question_id":"q_1"}`),
		request(http.MethodPost, "/api/v1/voice/reply", `{"target":"orchestrator","message_id":"m"}`),
		request(http.MethodGet, "/api/v1/voice/settings", ""),
		request(http.MethodPut, "/api/v1/voice/settings", `{}`),
		request(http.MethodGet, "/api/v1/voice/voices", ""),
		request(http.MethodPost, "/api/v1/voice/preview", `{}`),
	} {
		if response := h.do(r); response.Code != http.StatusNotFound {
			t.Errorf("%s %s with voice off: %d", r.Method, r.URL.Path, response.Code)
		}
	}
}

func TestVoiceRoutesNeedASessionAndSameOrigin(t *testing.T) {
	h, _, _, cookie := voiceHarness(t)
	if response := h.do(audioRequest("audio/webm", []byte("a"))); response.Code != http.StatusUnauthorized {
		t.Errorf("no session: %d", response.Code)
	}
	r := withCookie(audioRequest("audio/webm", []byte("a")), cookie)
	r.Header.Set("Origin", "http://evil.example")
	if response := h.do(r); response.Code != http.StatusForbidden {
		t.Errorf("cross-site upload: %d", response.Code)
	}
}

func TestSTTTranscribesWithTheKeyAndNeverEchoesIt(t *testing.T) {
	h, fake, _, cookie := voiceHarness(t)
	for _, contentType := range []string{"audio/webm;codecs=opus", "audio/mp4"} {
		response := h.do(withCookie(audioRequest(contentType, []byte("fake-opus")), cookie))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", contentType, response.Code, response.Body)
		}
		if got := decodeData[STTResponse](t, response.Body.Bytes()); got.Text != "ship the release" {
			t.Fatalf("text %q", got.Text)
		}
		assertNoKey(t, h, response.Body.String())
	}
	for _, sent := range fake.Requests() {
		if sent.APIKey != voicetest.Key || sent.ModelID != voice.STTModel {
			t.Fatalf("upstream request %+v", sent)
		}
	}
}

func TestSTTRefusesBadUploads(t *testing.T) {
	h, fake, _, cookie := voiceHarness(t)
	cases := []struct {
		name, contentType string
		body              []byte
		want              int
	}{
		{"not audio", "text/plain", []byte("hi"), http.StatusUnsupportedMediaType},
		{"no type", "", []byte("hi"), http.StatusUnsupportedMediaType},
		{"empty", "audio/webm", nil, http.StatusBadRequest},
		{"too big", "audio/webm", make([]byte, voice.MaxRecordingBytes+1), http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		if response := h.do(withCookie(audioRequest(c.contentType, c.body), cookie)); response.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.name, response.Code, c.want, response.Body)
		}
	}
	if n := len(fake.Requests()); n != 0 {
		t.Fatalf("refused uploads reached ElevenLabs %d times", n)
	}
}

func TestSTTUpstreamErrorsAreMappedWithoutUpstreamText(t *testing.T) {
	h, fake, _, cookie := voiceHarness(t)
	cases := map[int]int{
		http.StatusUnauthorized:        http.StatusBadGateway,
		http.StatusTooManyRequests:     http.StatusServiceUnavailable,
		http.StatusUnprocessableEntity: http.StatusUnprocessableEntity,
		http.StatusInternalServerError: http.StatusBadGateway,
	}
	for upstream, want := range cases {
		fake.FailWith(upstream)
		response := h.do(withCookie(audioRequest("audio/webm", []byte("a")), cookie))
		if response.Code != want || decodeError(t, response).Code != codeVoiceUpstream {
			t.Errorf("upstream %d: %d %s", upstream, response.Code, response.Body)
		}
		assertNoKey(t, h, response.Body.String())
	}
}

func TestSTTIsRateLimited(t *testing.T) {
	h, _, _, cookie := voiceHarness(t)
	var last *http.Response
	for range 20 {
		last = h.do(withCookie(audioRequest("audio/webm", []byte("a")), cookie)).Result()
		if last.StatusCode == http.StatusTooManyRequests {
			break
		}
	}
	if last.StatusCode != http.StatusTooManyRequests || last.Header.Get("Retry-After") == "" {
		t.Fatalf("never limited: %d", last.StatusCode)
	}
}

func TestTTSServesOnlyServerHeldTexts(t *testing.T) {
	h, fake, service, cookie := voiceHarness(t)
	id := service.Say("Tests pass.", voice.SourceSay, "w1:p1")
	response := h.do(withCookie(request(http.MethodGet, "/api/v1/voice/tts?text_id="+id, ""), cookie))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "audio/mpeg" || response.Body.String() != string(voicetest.Audio) {
		t.Fatalf("tts: %d %q %q", response.Code, response.Header().Get("Content-Type"), response.Body)
	}
	if got := fake.Requests(); len(got) != 1 || !strings.Contains(got[0].Body, `"text":"Tests pass."`) {
		t.Fatalf("upstream %+v", got)
	}
	for query, want := range map[string]int{"?text_id=t_000000000000000000000000": 404, "": 400, "?text=say+anything": 400} {
		if got := h.do(withCookie(request(http.MethodGet, "/api/v1/voice/tts"+query, ""), cookie)); got.Code != want {
			t.Errorf("%q: %d, want %d", query, got.Code, want)
		}
	}
	fake.FailWith(http.StatusUnauthorized)
	failed := h.do(withCookie(request(http.MethodGet, "/api/v1/voice/tts?text_id="+service.Say("Again.", voice.SourceSay, ""), ""), cookie))
	if failed.Code != http.StatusBadGateway {
		t.Fatalf("upstream 401: %d", failed.Code)
	}
	assertNoKey(t, h, failed.Body.String(), response.Body.String())
}

// A five-minute recording at the rate the web app asks for, or at three times it (Safari's AAC
// may ignore the asked rate), fits under the cap.
func TestSTTTakesFiveMinutes(t *testing.T) {
	h, fake, _, cookie := voiceHarness(t)
	fiveMinutes := voice.MaxRecordingSeconds * voice.RecordingBitsPerSecond * 3 / 8
	if voice.MaxRecordingSeconds != 300 || fiveMinutes > voice.MaxRecordingBytes {
		t.Fatalf("cap %d bytes is under five minutes at 192 kbit/s (%d)", voice.MaxRecordingBytes, fiveMinutes)
	}
	response := h.do(withCookie(audioRequest("audio/mp4", make([]byte, fiveMinutes)), cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("five minutes of audio: %d %s", response.Code, response.Body)
	}
	if got := fake.Requests()[0].FileBytes; got != fiveMinutes {
		t.Fatalf("upstream got %d bytes", got)
	}
}

func TestVoiceSettingsRoundTrip(t *testing.T) {
	h, fake, _, cookie := voiceHarness(t)
	get := func() VoiceSettingsResponse {
		response := h.do(withCookie(request(http.MethodGet, "/api/v1/voice/settings", ""), cookie))
		if response.Code != http.StatusOK {
			t.Fatalf("GET settings: %d %s", response.Code, response.Body)
		}
		return decodeData[VoiceSettingsResponse](t, response.Body.Bytes())
	}
	if got := get(); got.VoiceID != "voice123" || got.VoiceSource != "default" || got.Speed != 1 || got.Model != voice.ModelFast ||
		got.SpeedMin != 0.8 || got.SpeedMax != 1.5 || len(got.Models) != 2 || got.PlaybackRate != 1 {
		t.Fatalf("defaults %+v", got)
	}
	put := h.do(withCookie(request(http.MethodPut, "/api/v1/voice/settings", `{"voice_id":"`+voicetest.VoiceBrian+`","speed":1.5,"model":"quality"}`), cookie))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", put.Code, put.Body)
	}
	if got := get(); got.VoiceID != voicetest.VoiceBrian || got.VoiceSource != "settings" || got.Speed != 1.5 || got.PlaybackRate != 1.25 || got.Model != voice.ModelQuality {
		t.Fatalf("after PUT %+v", got)
	}
	for _, bad := range []string{`{"voice_id":"../x"}`, `{"speed":3}`, `{"model":"turbo"}`, `not json`} {
		if response := h.do(withCookie(request(http.MethodPut, "/api/v1/voice/settings", bad), cookie)); response.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, response.Code)
		}
	}
	if len(fake.Requests()) != 0 {
		t.Fatal("settings called ElevenLabs")
	}
}

func TestVoicesAndPreview(t *testing.T) {
	h, fake, _, cookie := voiceHarness(t)
	response := h.do(withCookie(request(http.MethodGet, "/api/v1/voice/voices", ""), cookie))
	voices := decodeData[[]voice.VoiceInfo](t, response.Body.Bytes())
	if response.Code != http.StatusOK || len(voices) != 2 || voices[1].Name != "Brian" {
		t.Fatalf("voices %d %+v", response.Code, voices)
	}
	preview := h.do(withCookie(request(http.MethodPost, "/api/v1/voice/preview", `{"voice_id":"`+voicetest.VoiceBrian+`","speed":1.3}`), cookie))
	got := decodeData[VoicePreviewResponse](t, preview.Body.Bytes())
	if preview.Code != http.StatusOK || got.TextID == "" || got.PlaybackRate != 1.083 {
		t.Fatalf("preview %d %+v", preview.Code, got)
	}
	if audio := h.do(withCookie(request(http.MethodGet, "/api/v1/voice/tts?text_id="+got.TextID, ""), cookie)); audio.Code != http.StatusOK {
		t.Fatalf("preview audio: %d", audio.Code)
	}
	last := fake.Requests()[len(fake.Requests())-1]
	if last.Path != "/v1/text-to-speech/"+voicetest.VoiceBrian+"/stream" || !strings.Contains(last.Body, `"speed":1.2`) {
		t.Fatalf("preview upstream %+v", last)
	}
	fake.FailWith(http.StatusUnauthorized)
	h2, fake2, _, cookie2 := voiceHarness(t)
	fake2.FailWith(http.StatusUnauthorized)
	failed := h2.do(withCookie(request(http.MethodGet, "/api/v1/voice/voices", ""), cookie2))
	if failed.Code != http.StatusBadGateway {
		t.Fatalf("voices with a refused key: %d", failed.Code)
	}
	assertNoKey(t, h2, failed.Body.String())
}

func TestReplySayPlaysAPastReplyOnDemand(t *testing.T) {
	c := newConversationHarness(t)
	client, _ := voice.NewClient(voice.ClientOptions{BaseURL: voicetest.New(t, "").URL, APIKey: voicetest.Key, VoiceID: "v1234567"})
	c.server.deps.Voice = voice.NewService(voice.Options{Client: client})
	appendLine := `{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"SAY: Tests pass.\n\nAll 40 of them."}]}}` + "\n" +
		`{"type":"assistant","uuid":"a2","message":{"role":"assistant","content":[{"type":"text","text":"No say line here."}]}}` + "\n"
	file, err := os.OpenFile(c.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(appendLine)
	_ = file.Close()
	var withSay, withoutSay, user string
	for _, message := range c.messages(t, "orchestrator", "").Messages {
		switch {
		case message.Kind == transcript.KindAssistant && strings.HasPrefix(message.Text, "SAY:"):
			withSay = message.ID
		case message.Kind == transcript.KindAssistant:
			withoutSay = message.ID
		case message.Kind == transcript.KindUser:
			user = message.ID
		}
	}
	if withSay == "" || withoutSay == "" || user == "" {
		t.Fatal("transcript not read")
	}
	response := c.do(request(http.MethodPost, "/api/v1/voice/reply", `{"target":"orchestrator","message_id":"`+withSay+`"}`))
	got := decodeData[ReplySayResponse](t, response.Body.Bytes())
	if response.Code != http.StatusOK || got.Text != "Tests pass." || !strings.HasPrefix(got.TextID, "t_") {
		t.Fatalf("reply: %d %+v", response.Code, got)
	}
	for _, id := range []string{withoutSay, user, "nope"} {
		if response := c.do(request(http.MethodPost, "/api/v1/voice/reply", `{"target":"orchestrator","message_id":"`+id+`"}`)); response.Code != http.StatusNotFound {
			t.Errorf("%s: %d", id, response.Code)
		}
	}
	if bad := c.do(request(http.MethodPost, "/api/v1/voice/reply", `{"target":"../x","message_id":"a"}`)); bad.Code != http.StatusBadRequest {
		t.Errorf("bad target: %d", bad.Code)
	}
}

func TestReadAloudSpeaksTitleAndOptions(t *testing.T) {
	h, fake, _, cookie := voiceHarness(t)
	response := h.do(withCookie(request(http.MethodPost, "/api/v1/voice/read", `{"question_id":"q_1"}`), cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("read: %d %s", response.Code, response.Body)
	}
	id := decodeData[ReadAloudResponse](t, response.Body.Bytes()).TextID
	if audio := h.do(withCookie(request(http.MethodGet, "/api/v1/voice/tts?text_id="+id, ""), cookie)); audio.Code != http.StatusOK {
		t.Fatalf("tts: %d", audio.Code)
	}
	body := fake.Requests()[0].Body
	if !strings.Contains(body, "Ship the migration tonight. Options: 1, Ship it. 2, Wait for Monday.") {
		t.Fatalf("spoken %s", body)
	}
	if missing := h.do(withCookie(request(http.MethodPost, "/api/v1/voice/read", `{"question_id":"q_99"}`), cookie)); missing.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", missing.Code)
	}
}

func TestVoicePromptCarriesTheHeader(t *testing.T) {
	c := newConversationHarness(t)
	if off := prompt(c, "orchestrator", `{"text":"status","voice":true}`); off.code != http.StatusNotFound {
		t.Fatalf("voice prompt with talk mode off: %d", off.code)
	}
	client, _ := voice.NewClient(voice.ClientOptions{BaseURL: voicetest.New(t, "").URL, APIKey: voicetest.Key, VoiceID: "v"})
	service := voice.NewService(voice.Options{Client: client})
	c.server.deps.Voice = service
	if response := prompt(c, "orchestrator", `{"text":" what is the status ","voice":true}`); response.code != http.StatusOK {
		t.Fatalf("voice prompt: %d %s", response.code, response.body)
	}
	if typed := c.fake.Prompts(); len(typed) != 1 || typed[0].Text != VoiceHeader+"\nwhat is the status" {
		t.Fatalf("typed %+v", typed)
	}
	if empty := prompt(c, "orchestrator", `{"text":"  ","voice":true}`); empty.code != http.StatusBadRequest {
		t.Fatalf("empty voice prompt: %d", empty.code)
	}
}
