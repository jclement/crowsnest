package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/update"
)

// fakeUpdater is a scripted Updater.
type fakeUpdater struct {
	available *update.Release
	applyErr  error
	applied   atomic.Int32
	restarted atomic.Int32
}

func (f *fakeUpdater) Available() (update.Release, bool) {
	if f.available == nil {
		return update.Release{}, false
	}
	return *f.available, true
}

func (f *fakeUpdater) Apply(context.Context) (update.Release, error) {
	f.applied.Add(1)
	if f.applyErr != nil {
		return update.Release{}, f.applyErr
	}
	return update.Release{TagName: "v10.0.0", Version: "10.0.0"}, nil
}

func (f *fakeUpdater) Restart() { f.restarted.Add(1) }

func TestHealthReportsAnAvailableUpdate(t *testing.T) {
	h := newHarness(t, harnessOptions{updater: &fakeUpdater{available: &update.Release{
		TagName: "v10.0.0", Version: "10.0.0", URL: "https://github.com/jclement/crowsnest/releases/tag/v10.0.0",
	}}})
	response := h.do(request(http.MethodGet, "/api/v1/health", ""))
	var envelope struct{ Data HealthResponse }
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	want := HealthResponse{Status: "ok", Version: "v9.9.9-test", UpdateAvailable: true, LatestVersion: "v10.0.0",
		ReleaseURL: "https://github.com/jclement/crowsnest/releases/tag/v10.0.0"}
	if envelope.Data != want {
		t.Fatalf("health %+v, want %+v", envelope.Data, want)
	}
}

func TestAdminUpdateNeedsASession(t *testing.T) {
	updater := &fakeUpdater{}
	h := newHarness(t, harnessOptions{updater: updater})
	if response := h.do(request(http.MethodPost, "/api/v1/admin/update", "")); response.Code != http.StatusUnauthorized {
		t.Fatalf("status %d without a session", response.Code)
	}
	cross := withCookie(request(http.MethodPost, "/api/v1/admin/update", ""), h.login())
	cross.Header.Set("Origin", "https://evil.example")
	if response := h.do(cross); response.Code != http.StatusForbidden {
		t.Fatalf("status %d from another origin", response.Code)
	}
	if updater.applied.Load() != 0 {
		t.Fatal("an update ran for a request that was refused")
	}
}

func TestAdminUpdateInstallsAndRestarts(t *testing.T) {
	updater := &fakeUpdater{}
	h := newHarness(t, harnessOptions{updater: updater})
	response := h.do(withCookie(request(http.MethodPost, "/api/v1/admin/update", ""), h.login()))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body)
	}
	var envelope struct{ Data AdminUpdateResponse }
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data != (AdminUpdateResponse{Updated: true, Version: "v10.0.0", Restarting: true}) {
		t.Fatalf("response %+v", envelope.Data)
	}
	deadline := time.Now().Add(5 * time.Second)
	for updater.restarted.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if updater.restarted.Load() != 1 {
		t.Fatal("the server did not restart after a successful update")
	}
}

func TestAdminUpdateRefusalsAndFailures(t *testing.T) {
	cases := []struct {
		name     string
		updater  Updater
		status   int
		contains string
	}{
		{"no updater", nil, http.StatusConflict, "Self-update is off"},
		{"refused", &fakeUpdater{applyErr: update.ErrDocker}, http.StatusConflict, "pull a new image"},
		{"current", &fakeUpdater{applyErr: &update.Refusal{Reason: "crowsnest v9.9.9 is already the latest release"}}, http.StatusConflict, "already the latest"},
		// A failure's detail stays in the log; the response says what to do.
		{"failed", &fakeUpdater{applyErr: errors.New("GET https://internal/x: verification failed: bad SCT")}, http.StatusBadGateway, "nothing was changed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{updater: tc.updater})
			response := h.do(withCookie(request(http.MethodPost, "/api/v1/admin/update", ""), h.login()))
			if response.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			body := decodeError(t, response)
			if !strings.Contains(body.Message, tc.contains) {
				t.Fatalf("message %q lacks %q", body.Message, tc.contains)
			}
			if strings.Contains(body.Message, "bad SCT") {
				t.Fatal("internal error text leaked into the response")
			}
			if fake, ok := tc.updater.(*fakeUpdater); ok && fake.restarted.Load() != 0 {
				t.Fatal("restarted after a refused or failed update")
			}
		})
	}
}
