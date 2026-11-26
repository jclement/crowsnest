// Tests for the talk-mode routes against a fake ElevenLabs (voicetest): 404 when voice is off,
// the API key sent upstream and never echoed, upload caps and types, rate limits, the error
// mapping, text ids, the talk heartbeat, read-aloud and the voice header on prompts.

package httpd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
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
	service := voice.NewService(voice.Options{Client: client})
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
		request(http.MethodPost, "/api/v1/voice/talk", `{"client_id":"abcdefgh","on":true}`),
		request(http.MethodPost, "/api/v1/voice/read", `{"question_id":"q_1"}`),
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

func TestTalkHeartbeat(t *testing.T) {
	h, _, service, cookie := voiceHarness(t)
	response := h.do(withCookie(request(http.MethodPost, "/api/v1/voice/talk", `{"client_id":"tab-12345678","on":true}`), cookie))
	got := decodeData[TalkResponse](t, response.Body.Bytes())
	if response.Code != http.StatusOK || !got.Talk || got.HeartbeatSeconds != 15 || got.MaxRecordingSeconds != 60 || !service.TalkOn() {
		t.Fatalf("on: %d %+v", response.Code, got)
	}
	response = h.do(withCookie(request(http.MethodPost, "/api/v1/voice/talk", `{"client_id":"tab-12345678","on":false}`), cookie))
	if got := decodeData[TalkResponse](t, response.Body.Bytes()); got.Talk || service.TalkOn() {
		t.Fatalf("off: %+v", got)
	}
	if bad := h.do(withCookie(request(http.MethodPost, "/api/v1/voice/talk", `{"client_id":"x"}`), cookie)); bad.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", bad.Code)
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

func TestVoicePromptCarriesTheHeaderAndTalkFlag(t *testing.T) {
	c := newConversationHarness(t)
	if off := prompt(c, "orchestrator", `{"text":"status","voice":true}`); off.code != http.StatusNotFound {
		t.Fatalf("voice prompt with talk mode off: %d", off.code)
	}
	client, _ := voice.NewClient(voice.ClientOptions{BaseURL: voicetest.New(t, "").URL, APIKey: voicetest.Key, VoiceID: "v"})
	service := voice.NewService(voice.Options{Client: client})
	c.server.deps.Voice = service
	service.Beat("tab-12345678", true)
	if response := prompt(c, "orchestrator", `{"text":" what is the status ","voice":true}`); response.code != http.StatusOK {
		t.Fatalf("voice prompt: %d %s", response.code, response.body)
	}
	if typed := c.fake.Prompts(); len(typed) != 1 || typed[0].Text != VoiceHeader+" talk=on\nwhat is the status" {
		t.Fatalf("typed %+v", typed)
	}
	if empty := prompt(c, "orchestrator", `{"text":"  ","voice":true}`); empty.code != http.StatusBadRequest {
		t.Fatalf("empty voice prompt: %d", empty.code)
	}
}
