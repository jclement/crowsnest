// project_list and project_get over the socket (DESIGN.md 21.2): readable by any caller, a 404
// for an unknown project, and the id that job_create(project=…) resolves.

package agentapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubProjects struct{}

func (stubProjects) ProjectsText(context.Context) (string, error) {
	return "# Projects\n- alpha\n", nil
}

func (stubProjects) ProjectText(_ context.Context, ref string) (string, error) {
	if ref != "alpha" && ref != "7" {
		return "", ErrNoProject
	}
	return "# Project alpha\n", nil
}

func (stubProjects) ProjectID(_ context.Context, ref string) (int64, error) {
	if ref != "alpha" && ref != "7" {
		return 0, ErrNoProject
	}
	return 7, nil
}

func TestProjectRoutes(t *testing.T) {
	server := NewServer(ServerOptions{Projects: stubProjects{}})
	get := func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		server.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder
	}
	if r := get("/api/v1/projects"); r.Code != 200 || !strings.Contains(r.Body.String(), "alpha") {
		t.Fatalf("list %d %s", r.Code, r.Body)
	}
	if r := get("/api/v1/projects/alpha"); r.Code != 200 || !strings.Contains(r.Body.String(), `"id":7`) {
		t.Fatalf("get %d %s", r.Code, r.Body)
	}
	if r := get("/api/v1/projects/nope"); r.Code != 404 {
		t.Fatalf("unknown %d", r.Code)
	}
	if r := httptest.NewRecorder(); true {
		NewServer(ServerOptions{}).handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
		if r.Code != 503 {
			t.Fatalf("no projects: %d", r.Code)
		}
	}
}
