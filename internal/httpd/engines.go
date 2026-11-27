// GET /api/v1/engines (DESIGN.md 20.1): what each engine gives a job, from internal/engines, the
// one statement of it. The job form, project defaults, a Codex job's card and Rescue read it.

package httpd

import (
	"net/http"

	"github.com/jclement/crowsnest/internal/engines"
)

func (s *Server) registerEngineRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/engines", s.requireAuth(http.HandlerFunc(s.handleEngines)))
}

func (s *Server) handleEngines(w http.ResponseWriter, r *http.Request) {
	catalog := s.deps.Engines
	if catalog == nil {
		// No catalog (a test, the demo without one): the table without the machine's facts.
		writeData(w, http.StatusOK, []engines.Engine{engines.ClaudeEngine(true), engines.CodexEngine(true, nil)})
		return
	}
	writeData(w, http.StatusOK, catalog.Engines(r.Context()))
}
