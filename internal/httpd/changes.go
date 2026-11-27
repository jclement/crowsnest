// Browser routes for a diff at any time and its viewed marks (DESIGN.md 6.20). Every route needs a
// session; the state-changing ones pass the origin check and need X-Crowsnest like every other.
// {who} is jobs/{id}, projects/{ref} or orchestrator:
//
//	GET    /api/v1/changes/{who}?scope=&base=&refresh=1   the files of one comparison, with marks
//	GET    /api/v1/changes/{who}/file?path=&scope=&context=&ws=ignore&since=1
//	PUT    /api/v1/changes/{who}/viewed   {scope, files: [{path, hash}], all}
//	DELETE /api/v1/changes/{who}/viewed   {scope, paths}   (no paths: every mark in scope)
//	GET    /api/v1/changes/projects/{ref}/jobs             the project's checkouts with diffstats
//	GET    /api/v1/changes/progress                        jobs' viewed counts (no git is run)
//	POST   /api/v1/changes/jobs/{id}/demo-touch            --demo only: the demo job edits a file now

package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/jclement/crowsnest/internal/changes"
)

func (s *Server) registerChangesRoutes(mux *http.ServeMux) {
	auth := func(handler http.HandlerFunc) http.Handler { return s.requireAuth(handler) }
	for _, prefix := range []struct {
		path string
		ask  func(*http.Request) changes.Ask
	}{
		{"/api/v1/changes/jobs/{id}", func(r *http.Request) changes.Ask { return changes.Ask{Kind: changes.KindJob, Ref: r.PathValue("id")} }},
		{"/api/v1/changes/projects/{ref}", func(r *http.Request) changes.Ask {
			return changes.Ask{Kind: changes.KindProject, Ref: r.PathValue("ref"), Base: r.URL.Query().Get("base")}
		}},
		{"/api/v1/changes/orchestrator", func(r *http.Request) changes.Ask {
			return changes.Ask{Kind: changes.KindOrchestrator, Base: r.URL.Query().Get("base")}
		}},
	} {
		ask := prefix.ask
		mux.Handle("GET "+prefix.path, auth(func(w http.ResponseWriter, r *http.Request) { s.handleChangesSummary(w, r, ask(r)) }))
		mux.Handle("GET "+prefix.path+"/file", auth(func(w http.ResponseWriter, r *http.Request) { s.handleChangesFile(w, r, ask(r)) }))
		mux.Handle("PUT "+prefix.path+"/viewed", auth(func(w http.ResponseWriter, r *http.Request) { s.handleChangesMark(w, r, ask(r)) }))
		mux.Handle("DELETE "+prefix.path+"/viewed", auth(func(w http.ResponseWriter, r *http.Request) { s.handleChangesUnmark(w, r, ask(r)) }))
	}
	mux.Handle("GET /api/v1/changes/projects/{ref}/jobs", auth(s.handleProjectChanges))
	mux.Handle("GET /api/v1/changes/progress", auth(s.handleChangesProgress))
	mux.Handle("POST /api/v1/changes/jobs/{id}/demo-touch", auth(s.handleDemoTouch))
}

func (s *Server) changesService(w http.ResponseWriter) *changes.Service {
	if s.deps.Changes == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "The diff view is not available.")
		return nil
	}
	return s.deps.Changes
}

func (s *Server) writeChangesError(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := changes.AsError(err); ok {
		switch e.Kind {
		case "invalid":
			writeError(w, http.StatusBadRequest, codeValidation, e.Message)
		case "not_found":
			writeError(w, http.StatusNotFound, codeNotFound, e.Message)
		case "gone":
			writeError(w, http.StatusGone, "GONE", e.Message)
		case "stale":
			writeError(w, http.StatusConflict, "STALE", e.Message)
		default:
			writeError(w, http.StatusBadRequest, codeValidation, e.Message)
		}
		return
	}
	s.internalError(w, r, "reading a diff", err)
}

func (s *Server) handleChangesSummary(w http.ResponseWriter, r *http.Request, ask changes.Ask) {
	service := s.changesService(w)
	if service == nil {
		return
	}
	summary, err := service.Summary(r.Context(), ask, r.URL.Query().Get("scope"), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		s.writeChangesError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, summary)
}

func (s *Server) handleChangesFile(w http.ResponseWriter, r *http.Request, ask changes.Ask) {
	service := s.changesService(w)
	if service == nil {
		return
	}
	query := r.URL.Query()
	fileAsk := changes.FileAsk{Path: query.Get("path"), Scope: query.Get("scope"), IgnoreWhitespace: query.Get("ws") == "ignore",
		Since: query.Get("since") == "1"}
	if raw := query.Get("context"); raw != "" {
		context, err := strconv.Atoi(raw)
		if err != nil || context < 0 || context > 100000 {
			writeError(w, http.StatusBadRequest, codeValidation, "context is a number of lines from 0 to 100000.")
			return
		}
		fileAsk.Context = context
	}
	view, err := service.File(r.Context(), ask, fileAsk)
	if err != nil {
		s.writeChangesError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, view)
}

type markBody struct {
	Scope string            `json:"scope"`
	Files []changes.MarkAsk `json:"files"`
	All   bool              `json:"all"`
	Paths []string          `json:"paths"`
}

func decodeMarks(w http.ResponseWriter, r *http.Request, body *markBody) bool {
	if r.ContentLength == 0 {
		return true
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(body); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"scope":"all","files":[{"path":"a.go","hash":"…"}]}.`)
		return false
	}
	if len(body.Files) > 5000 || len(body.Paths) > 5000 {
		writeError(w, http.StatusBadRequest, codeValidation, "At most 5000 files at once.")
		return false
	}
	return true
}

func (s *Server) handleChangesMark(w http.ResponseWriter, r *http.Request, ask changes.Ask) {
	service := s.changesService(w)
	if service == nil {
		return
	}
	var body markBody
	if !decodeMarks(w, r, &body) {
		return
	}
	if len(body.Files) == 0 && !body.All {
		writeError(w, http.StatusBadRequest, codeValidation, "Name the files to mark viewed, or send all: true.")
		return
	}
	summary, err := service.Mark(r.Context(), ask, body.Scope, body.Files, body.All)
	if err != nil {
		s.writeChangesError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, summary)
}

func (s *Server) handleChangesUnmark(w http.ResponseWriter, r *http.Request, ask changes.Ask) {
	service := s.changesService(w)
	if service == nil {
		return
	}
	var body markBody
	if !decodeMarks(w, r, &body) {
		return
	}
	if body.Scope == "" {
		body.Scope = r.URL.Query().Get("scope")
	}
	summary, err := service.Unmark(r.Context(), ask, body.Scope, body.Paths)
	if err != nil {
		s.writeChangesError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, summary)
}

func (s *Server) handleProjectChanges(w http.ResponseWriter, r *http.Request) {
	service := s.changesService(w)
	if service == nil {
		return
	}
	rows, err := service.ProjectChanges(r.Context(), r.PathValue("ref"))
	if err != nil {
		s.writeChangesError(w, r, err)
		return
	}
	writeList(w, rows, ListMeta{Total: len(rows)})
}

func (s *Server) handleChangesProgress(w http.ResponseWriter, r *http.Request) {
	service := s.changesService(w)
	if service == nil {
		return
	}
	progress, err := service.ProgressAll(r.Context())
	if err != nil {
		s.internalError(w, r, "reading diff progress", err)
		return
	}
	writeData(w, http.StatusOK, progress)
}

func (s *Server) handleDemoTouch(w http.ResponseWriter, r *http.Request) {
	if !s.deps.Config.Demo || s.deps.DemoTouch == nil {
		writeError(w, http.StatusNotFound, codeNotFound, "Only the demo has this.")
		return
	}
	if err := s.deps.DemoTouch(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	writeData(w, http.StatusOK, map[string]bool{"touched": true})
}

// landUnviewed is the Land gate's view of the diff: with "require all files viewed before
// Land" on, a merge or push of a job with files not viewed is refused (409 UNVIEWED). Off, or
// with no diff service, it never refuses.
func (s *Server) landUnviewed(ctx context.Context, w http.ResponseWriter, id, action string) bool {
	if s.deps.Changes == nil || action == "discard" || s.deps.RequireViewed == nil || !s.deps.RequireViewed() {
		return false
	}
	unviewed, total, err := s.deps.Changes.Unviewed(ctx, id)
	if err != nil || unviewed == 0 {
		return false
	}
	writeError(w, http.StatusConflict, "UNVIEWED", strconv.Itoa(unviewed)+" of "+strconv.Itoa(total)+
		" files are not marked viewed, and Settings require every file viewed before Land. Mark them on the Review tab, or turn the setting off.")
	return true
}
