// Tests for /instructions and /agents-instructions (DESIGN.md 21.3): who may call them, the
// shapes, and how the service's refusals map to statuses.

package httpd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/instructions"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/store"
)

func newInstructionsHarness(t *testing.T) (*harness, *store.Store, string) {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rolesDir := t.TempDir()
	service := instructions.New(instructions.Options{Store: db, RolesDir: rolesDir})
	return newHarness(t, harnessOptions{instructions: service}), db, rolesDir
}

// dataOf decodes a response's data envelope (decodeData, from agents_test.go, takes bytes).
func dataOf[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	return decodeData[T](t, response.Body.Bytes())
}

func TestInstructionsAuthorization(t *testing.T) {
	h, _, _ := newInstructionsHarness(t)
	cookie := h.login()
	cases := []struct {
		name   string
		method string
		path   string
		body   string
		cookie bool
		csrf   bool
		want   int
	}{
		{"list without a session", http.MethodGet, "/api/v1/instructions", "", false, false, http.StatusUnauthorized},
		{"one layer without a session", http.MethodGet, "/api/v1/instructions/global/-", "", false, false, http.StatusUnauthorized},
		{"agent view without a session", http.MethodGet, "/api/v1/agents-instructions/j_1", "", false, false, http.StatusUnauthorized},
		{"save without a session", http.MethodPut, "/api/v1/instructions/global/-", `{"body_md":"x"}`, false, true, http.StatusUnauthorized},
		{"save without the CSRF header", http.MethodPut, "/api/v1/instructions/global/-", `{"body_md":"x"}`, true, false, http.StatusForbidden},
		{"reset without the CSRF header", http.MethodDelete, "/api/v1/instructions/role/builder", "", true, false, http.StatusForbidden},
		{"restore without the CSRF header", http.MethodPost, "/api/v1/instructions/global/-/restore", `{"revision":1}`, true, false, http.StatusForbidden},
		{"list with a session", http.MethodGet, "/api/v1/instructions", "", true, false, http.StatusOK},
		{"save with both", http.MethodPut, "/api/v1/instructions/global/-", `{"body_md":"x"}`, true, true, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := request(tc.method, tc.path, tc.body)
			if !tc.csrf {
				r.Header.Del("X-Crowsnest")
			}
			if tc.cookie {
				r = withCookie(r, cookie)
			}
			if response := h.do(r); response.Code != tc.want {
				t.Fatalf("got %d, want %d: %s", response.Code, tc.want, response.Body)
			}
		})
	}
}

func TestInstructionsRoutes(t *testing.T) {
	h, db, rolesDir := newInstructionsHarness(t)
	cookie := h.login()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		return h.do(withCookie(request(method, path, body), cookie))
	}

	listing := dataOf[instructions.Listing](t, do(http.MethodGet, "/api/v1/instructions", ""))
	if listing.Global.Source != instructions.SourceNone || len(listing.Roles) != 4 || listing.Roles[0].Key != "builder" ||
		listing.Roles[0].Source != instructions.SourceBuiltin || listing.Roles[0].BuiltinMD == "" || listing.MaxBytes != instructions.MaxBodyBytes {
		t.Fatalf("listing %+v", listing)
	}

	response := do(http.MethodPut, "/api/v1/instructions/role/builder", `{"body_md":"# Role: builder\n\nMine."}`)
	detail := dataOf[instructions.Detail](t, response)
	if response.Code != http.StatusOK || detail.Layer.Source != instructions.SourceDB || detail.Layer.Revision != 1 || len(detail.Revisions) != 1 {
		t.Fatalf("PUT role: %d %s", response.Code, response.Body)
	}
	response = do(http.MethodDelete, "/api/v1/instructions/role/builder", "")
	if detail = dataOf[instructions.Detail](t, response); detail.Layer.Source != instructions.SourceBuiltin || len(detail.Revisions) != 2 {
		t.Fatalf("DELETE role: %d %s", response.Code, response.Body)
	}
	response = do(http.MethodPost, "/api/v1/instructions/role/builder/restore", `{"revision":1}`)
	if detail = dataOf[instructions.Detail](t, response); detail.Layer.Source != instructions.SourceDB || detail.Layer.Revision != 3 {
		t.Fatalf("restore: %d %s", response.Code, response.Body)
	}
	if detail = dataOf[instructions.Detail](t, do(http.MethodGet, "/api/v1/instructions/role/builder", "")); len(detail.Revisions) != 3 {
		t.Fatalf("GET role: %+v", detail)
	}

	if err := os.WriteFile(filepath.Join(rolesDir, "tester.md"), []byte("# From the file"), 0o600); err != nil {
		t.Fatal(err)
	}
	errorsCases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"a role from a file", http.MethodPut, "/api/v1/instructions/role/tester", `{"body_md":"x"}`, http.StatusConflict},
		{"an unknown role", http.MethodPut, "/api/v1/instructions/role/deployer", `{"body_md":"x"}`, http.StatusNotFound},
		{"an unknown scope", http.MethodGet, "/api/v1/instructions/team/x", "", http.StatusBadRequest},
		{"global with a key", http.MethodPut, "/api/v1/instructions/global/x", `{"body_md":"x"}`, http.StatusBadRequest},
		{"no body_md", http.MethodPut, "/api/v1/instructions/global/-", `{}`, http.StatusBadRequest},
		{"a misspelt field", http.MethodPut, "/api/v1/instructions/global/-", `{"body":"x"}`, http.StatusBadRequest},
		{"too long", http.MethodPut, "/api/v1/instructions/global/-", `{"body_md":"` + strings.Repeat("x", instructions.MaxBodyBytes+1) + `"}`, http.StatusBadRequest},
		{"a missing project", http.MethodPut, "/api/v1/instructions/project/42", `{"body_md":"x"}`, http.StatusNotFound},
		{"restore with no revision", http.MethodPost, "/api/v1/instructions/global/-/restore", `{}`, http.StatusBadRequest},
		{"restore a revision never kept", http.MethodPost, "/api/v1/instructions/global/-/restore", `{"revision":9}`, http.StatusNotFound},
		{"a bad agent", http.MethodGet, "/api/v1/agents-instructions/nobody", "", http.StatusBadRequest},
	}
	for _, tc := range errorsCases {
		t.Run(tc.name, func(t *testing.T) {
			if response := do(tc.method, tc.path, tc.body); response.Code != tc.want {
				t.Fatalf("got %d, want %d: %s", response.Code, tc.want, response.Body)
			}
		})
	}

	// An agent's view: what it was started with, changed after a save, and files by path only.
	service := instructions.New(instructions.Options{Store: db, RolesDir: rolesDir})
	if _, err := service.JobLayers(context.Background(), jobs.Job{ID: "j_7", Role: "builder", BriefMD: "Do it."}, true); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordLoaded(context.Background(), "j_7", instructions.LoadedInput{SessionID: "s", FilePath: "/home/me/CLAUDE.md", MemoryType: "User", LoadReason: "session_start"}); err != nil {
		t.Fatal(err)
	}
	do(http.MethodPut, "/api/v1/instructions/global/-", `{"body_md":"Never push."}`)
	view := dataOf[instructions.AgentInstructions](t, do(http.MethodGet, "/api/v1/agents-instructions/j_7", ""))
	if !view.Changed || len(view.Changes) != 1 || view.Changes[0].Scope != instructions.ScopeGlobal || len(view.Layers) != 3 ||
		len(view.Loaded) != 1 || view.Loaded[0].Path != "/home/me/CLAUDE.md" || view.SkillsNote == "" {
		t.Fatalf("agent view %+v", view)
	}
}
