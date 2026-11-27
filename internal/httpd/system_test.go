// The System, Settings and first-run routes against the real internal/system service, a
// scripted machine (doctor.DemoEnv) and an in-memory database.

package httpd

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/system"
)

func newSystemHarness(t *testing.T, state string) (*harness, *store.Store) {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	home := t.TempDir()
	load := func(stored map[string]string) (config.Config, error) {
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.RegisterFlags(flags)
		return config.Load(flags, config.Options{
			Stored: stored, Getenv: func(string) (string, bool) { return "", false },
			HomeDir: func() (string, error) { return home, nil }, Interfaces: func() ([]config.Interface, error) { return nil, nil },
		})
	}
	cfg, err := load(nil)
	if err != nil {
		t.Fatal(err)
	}
	service := system.New(system.Options{Config: cfg, Version: "test", KV: db, Reload: load, Env: doctor.DemoEnv(state), Logger: slog.New(slog.DiscardHandler)})
	return newHarness(t, harnessOptions{system: service}), db
}

func decodeSystemData(t *testing.T, body []byte, into any) {
	t.Helper()
	envelope := struct{ Data json.RawMessage }{}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(envelope.Data, into); err != nil {
		t.Fatal(err)
	}
}

func TestSystemReportAndDiagnostics(t *testing.T) {
	h, _ := newSystemHarness(t, doctor.DemoHerdrMissing)
	cookie := h.login()
	response := h.do(withCookie(request(http.MethodGet, "/api/v1/system?recheck=1", ""), cookie))
	if response.Code != http.StatusOK {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
	var report doctor.Report
	decodeSystemData(t, response.Body.Bytes(), &report)
	if report.Verdict != doctor.StatusFail || report.Live == nil || !report.Live.Running {
		t.Fatalf("report %+v", report)
	}
	text := h.do(withCookie(request(http.MethodGet, "/api/v1/system/diagnostics", ""), cookie))
	if !strings.HasPrefix(text.Header().Get("Content-Type"), "text/plain") || !strings.Contains(text.Body.String(), "brew install herdr") {
		t.Fatalf("diagnostics %q: %s", text.Header().Get("Content-Type"), text.Body)
	}
	if code := h.do(request(http.MethodGet, "/api/v1/system", "")).Code; code != http.StatusUnauthorized {
		t.Fatalf("without a session: %d", code)
	}
}

func TestSettingsRoutes(t *testing.T) {
	h, db := newSystemHarness(t, doctor.DemoAllGreen)
	cookie := h.login()
	put := func(name, body string) *http.Response {
		recorder := h.do(withCookie(request(http.MethodPut, "/api/v1/settings/"+name, body), cookie))
		return recorder.Result()
	}
	if code := put("poke-interval", `{"value":"5m"}`).StatusCode; code != http.StatusOK {
		t.Fatalf("a valid change: %d", code)
	}
	if stored, _ := db.GetKV(context.Background(), "settings.poke-interval"); stored != "5m" {
		t.Fatalf("stored %q", stored)
	}
	if code := put("permission-mode", `{"value":"yolo"}`).StatusCode; code != http.StatusBadRequest {
		t.Fatalf("an invalid value: %d", code)
	}
	for _, name := range []string{"bind", "no-auth", "session", "data-dir"} {
		if code := put(name, `{"value":"x"}`).StatusCode; code != http.StatusConflict {
			t.Fatalf("%s: %d", name, code)
		}
	}
	list := h.do(withCookie(request(http.MethodGet, "/api/v1/settings", ""), cookie))
	var page system.SettingsPage
	decodeSystemData(t, list.Body.Bytes(), &page)
	if len(page.Settings) == 0 || page.BindCommand == "" {
		t.Fatalf("page %+v", page)
	}
}

func TestOnboardingRoutesAndMe(t *testing.T) {
	h, db := newSystemHarness(t, doctor.DemoAllGreen)
	cookie := h.login()
	if err := system.MarkOnboardingPending(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var me MeResponse
	decodeSystemData(t, h.do(withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), cookie)).Body.Bytes(), &me)
	if !me.Features.Onboarding {
		t.Fatal("me should say the checklist is unfinished")
	}
	start := h.do(withCookie(request(http.MethodPost, "/api/v1/onboarding/start", `{}`), cookie))
	if start.Code != http.StatusOK {
		t.Fatalf("start: %d %s", start.Code, start.Body)
	}
	var after MeResponse
	decodeSystemData(t, h.do(withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), cookie)).Body.Bytes(), &after)
	if after.Features.Onboarding {
		t.Fatal("still on the checklist after Start")
	}
	// The shell route accepts only a command the report offers, and needs a shell opener.
	if code := h.do(withCookie(request(http.MethodPost, "/api/v1/onboarding/shell", `{"command":"rm -rf /"}`), cookie)).Code; code != http.StatusServiceUnavailable && code != http.StatusBadRequest {
		t.Fatalf("an arbitrary command: %d", code)
	}
	if code := h.do(withCookie(request(http.MethodPost, "/api/v1/system/demo-state", `{"state":"ok"}`), cookie)).Code; code != http.StatusNotFound {
		t.Fatalf("demo-state outside --demo: %d", code)
	}
}
