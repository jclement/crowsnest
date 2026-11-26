// Tests for GET /fleet, GET /agents/{target}/screen and POST /agents/{target}/keys against the
// fake herdr, including the screen-hash binding that protects dialog answers.

package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
)

const dialogScreen = "\x1b[1mBash command\x1b[22m\n\n   rm -rf dist\n\n Do you want to proceed?\n ❯ 1. Yes\n   2. No\n\n Esc to cancel\n"

// herdrHarness is a no-auth harness backed by a fake with one blocked agent ("builder") and a
// plain shell pane.
func herdrHarness(t *testing.T) (*harness, *fake.Fake, string) {
	t.Helper()
	f := fake.New()
	_, _, agentPane := f.AddWorkspace("crowsnest", "/src")
	if err := f.AddAgent(agentPane, "builder", "claude", herdr.StatusBlocked, "sess-1"); err != nil {
		t.Fatal(err)
	}
	_, shellPane := f.AddTab("w1", "shell", "/src")
	if err := f.SetScreen(agentPane, fleet.StripANSI(dialogScreen), dialogScreen); err != nil {
		t.Fatal(err)
	}
	cache := fleet.New(fleet.Options{Client: f, Session: "crowsnest-test"})
	h := newHarness(t, harnessOptions{noAuth: true, herdr: f, fleet: cache})
	return h, f, shellPane
}

func decodeData[T any](t *testing.T, body []byte) T {
	t.Helper()
	var envelope struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("not a data envelope: %s", body)
	}
	return envelope.Data
}

func TestFleetEndpoint(t *testing.T) {
	h, _, _ := herdrHarness(t)
	response := h.do(request(http.MethodGet, "/api/v1/fleet", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /fleet = %d %s", response.Code, response.Body)
	}
	got := decodeData[fleet.Fleet](t, response.Body.Bytes())
	if got.Session != "crowsnest-test" || len(got.Workspaces) != 1 || len(got.Agents) != 1 {
		t.Fatalf("fleet = %+v", got)
	}
	if agent := got.Agents[0]; agent.Name != "builder" || !agent.HasDialog || !agent.NeedsYou {
		t.Fatalf("agent = %+v", agent)
	}
}

func TestFleetEndpointWithoutHerdr(t *testing.T) {
	h := newHarness(t, harnessOptions{noAuth: true})
	response := h.do(request(http.MethodGet, "/api/v1/fleet", ""))
	if response.Code != http.StatusServiceUnavailable || decodeError(t, response).Code != codeUnavailable {
		t.Fatalf("GET /fleet without herdr = %d %s", response.Code, response.Body)
	}
}

func TestHerdrRoutesRequireAuth(t *testing.T) {
	f := fake.New()
	h := newHarness(t, harnessOptions{herdr: f, fleet: fleet.New(fleet.Options{Client: f})})
	for _, r := range []*http.Request{
		request(http.MethodGet, "/api/v1/fleet", ""),
		request(http.MethodGet, "/api/v1/agents/builder/screen", ""),
		request(http.MethodPost, "/api/v1/agents/builder/keys", `{"keys":["esc"]}`),
	} {
		if response := h.do(r); response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d", r.Method, r.URL.Path, response.Code)
		}
	}
}

func TestScreenEndpoint(t *testing.T) {
	h, _, shellPane := herdrHarness(t)
	response := h.do(request(http.MethodGet, "/api/v1/agents/builder/screen", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("GET screen = %d %s", response.Code, response.Body)
	}
	screen := decodeData[ScreenResponse](t, response.Body.Bytes())
	if screen.Name != "builder" || screen.Kind != "claude" || screen.Status != herdr.StatusBlocked || screen.PaneID != "w1:p1" {
		t.Fatalf("screen header = %+v", screen)
	}
	if !screen.Dialog.Present || len(screen.Dialog.Options) != 2 || screen.Hash != fleet.ScreenHash(screen.Text) {
		t.Fatalf("screen dialog/hash = %+v", screen)
	}
	if screen.ANSI != dialogScreen || screen.Text == screen.ANSI {
		t.Fatal("screen should carry both the ANSI and the plain text")
	}

	// A pane id that is not an agent still has a screen.
	response = h.do(request(http.MethodGet, "/api/v1/agents/"+shellPane+"/screen", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("GET plain pane screen = %d %s", response.Code, response.Body)
	}
	if plain := decodeData[ScreenResponse](t, response.Body.Bytes()); plain.Kind != "" || plain.Dialog.Present {
		t.Fatalf("plain pane screen = %+v", plain)
	}

	for target, want := range map[string]int{"nobody": http.StatusNotFound, "bad%20name": http.StatusBadRequest} {
		if response := h.do(request(http.MethodGet, "/api/v1/agents/"+target+"/screen", "")); response.Code != want {
			t.Errorf("GET /agents/%s/screen = %d, want %d", target, response.Code, want)
		}
	}
}

func keysBody(t *testing.T, keys []string, hash string) string {
	t.Helper()
	body, err := json.Marshal(KeysRequest{Keys: keys, ScreenHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestKeysBoundToScreenHash(t *testing.T) {
	h, f, _ := herdrHarness(t)
	screen := decodeData[ScreenResponse](t, h.do(request(http.MethodGet, "/api/v1/agents/builder/screen", "")).Body.Bytes())

	// The screen changes after it was shown: the press is refused and nothing is sent.
	if err := f.SetScreen("w1:p1", "something else entirely", ""); err != nil {
		t.Fatal(err)
	}
	response := h.do(request(http.MethodPost, "/api/v1/agents/builder/keys", keysBody(t, []string{"1"}, screen.Hash)))
	if response.Code != http.StatusConflict || decodeError(t, response).Code != codeConflict {
		t.Fatalf("stale press = %d %s", response.Code, response.Body)
	}
	if len(f.SentKeys()) != 0 {
		t.Fatalf("keys were sent despite the conflict: %+v", f.SentKeys())
	}

	// Against the current screen it goes through.
	current := decodeData[ScreenResponse](t, h.do(request(http.MethodGet, "/api/v1/agents/builder/screen", "")).Body.Bytes())
	response = h.do(request(http.MethodPost, "/api/v1/agents/builder/keys", keysBody(t, []string{"1"}, current.Hash)))
	if response.Code != http.StatusOK {
		t.Fatalf("fresh press = %d %s", response.Code, response.Body)
	}
	sent := f.SentKeys()
	if len(sent) != 1 || sent[0].PaneID != "w1:p1" || sent[0].Keys[0] != "1" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestKeysValidation(t *testing.T) {
	h, f, shellPane := herdrHarness(t)
	cases := []struct {
		name string
		body string
		want int
	}{
		{"not json", `keys=1`, http.StatusBadRequest},
		{"no keys", `{"keys":[],"screen_hash":"x"}`, http.StatusBadRequest},
		{"too many", keysBody(t, make([]string, 17), "x"), http.StatusBadRequest},
		{"control character", keysBody(t, []string{"a\nb"}, "x"), http.StatusBadRequest},
		{"digit without a hash", keysBody(t, []string{"1"}, ""), http.StatusBadRequest},
		{"esc without a hash", keysBody(t, []string{"esc"}, ""), http.StatusOK},
		{"ctrl+c without a hash", keysBody(t, []string{"ctrl+c"}, ""), http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := h.do(request(http.MethodPost, "/api/v1/agents/builder/keys", tc.body))
			if response.Code != tc.want {
				t.Fatalf("= %d %s, want %d", response.Code, response.Body, tc.want)
			}
		})
	}
	if got := len(f.SentKeys()); got != 2 {
		t.Fatalf("%d key sends, want 2 (the interrupts)", got)
	}

	// Keys to a plain pane go through pane.send_keys.
	shellScreen := decodeData[ScreenResponse](t, h.do(request(http.MethodGet, "/api/v1/agents/"+shellPane+"/screen", "")).Body.Bytes())
	if response := h.do(request(http.MethodPost, "/api/v1/agents/"+shellPane+"/keys", keysBody(t, []string{"q"}, shellScreen.Hash))); response.Code != http.StatusOK {
		t.Fatalf("keys to a plain pane = %d %s", response.Code, response.Body)
	}
}

func TestKeysRequireOriginHeader(t *testing.T) {
	h, f, _ := herdrHarness(t)
	r := request(http.MethodPost, "/api/v1/agents/builder/keys", keysBody(t, []string{"esc"}, ""))
	r.Header.Del("X-Crowsnest")
	if response := h.do(r); response.Code != http.StatusForbidden {
		t.Fatalf("POST without X-Crowsnest = %d", response.Code)
	}
	if len(f.SentKeys()) != 0 {
		t.Fatal("a cross-site POST must not reach herdr")
	}
}

func TestMeReportsHerdr(t *testing.T) {
	h, _, _ := herdrHarness(t)
	me := decodeData[MeResponse](t, h.do(request(http.MethodGet, "/api/v1/auth/me", "")).Body.Bytes())
	if me.Features.Session != "crowsnest-test" || me.Features.HerdrVersion != "0.9.5-test" {
		t.Fatalf("me = %+v", me)
	}
}

func TestFleetEventsReachSSE(t *testing.T) {
	f := fake.New()
	_, _, pane := f.AddWorkspace("crowsnest", "/src")
	_ = f.AddAgent(pane, "builder", "claude", herdr.StatusIdle, "")
	h := newHarness(t, harnessOptions{noAuth: true, herdr: f})
	cache := fleet.New(fleet.Options{Client: f, Hub: h.hub, Debounce: 5 * time.Millisecond, Poll: time.Hour})
	sub := h.hub.Subscribe(8)
	defer sub.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cache.Run(ctx)
	select {
	case event := <-sub.Events():
		if event.Type != fleet.EventType {
			t.Fatalf("first hub event = %q", event.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no fleet event on the hub")
	}
}
