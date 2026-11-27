// The attention queue and the Now view (DESIGN.md 22):
//
//	GET  /attention                     the queue: {items, counts, revision, at, partial?}
//	POST /attention/snooze   {id, until?}  hide an item from the count (10 minutes), every device
//	POST /attention/unsnooze {id}          bring it back
//	POST /attention/dismiss  {id}          a failed job leaves the queue (it stays on the board)
//	GET  /now                           {rows, counts}: one row per project, unfiled, orchestrator
//	GET  /now/since?browser=<id>        what finished, failed or asked since this browser left
//	POST /now/beat {browser}            this browser is in use (opens a new window after a gap)
//	POST /now/seen {browser}            "Mark all seen"
//
// Every count the web app shows (badge, title, tray, palette, Inbox, Now) is counts.total from
// the same snapshot, which the `attention` event also carries.

package httpd

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jclement/crowsnest/internal/attention"
)

// NowSources are what the Now view's rows and "since you left" are read from; serve builds them
// because only it holds the leads, the orchestrator and the spend meter.
type NowSources struct {
	Overview attention.OverviewOptions
	Since    func(r *http.Request) (attention.SinceSources, error)
}

func (s *Server) registerAttentionRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/attention", s.requireAuth(http.HandlerFunc(s.handleAttention)))
	mux.Handle("POST /api/v1/attention/snooze", s.requireAuth(http.HandlerFunc(s.handleAttentionSnooze)))
	mux.Handle("POST /api/v1/attention/unsnooze", s.requireAuth(http.HandlerFunc(s.handleAttentionUnsnooze)))
	mux.Handle("POST /api/v1/attention/dismiss", s.requireAuth(http.HandlerFunc(s.handleAttentionDismiss)))
	mux.Handle("GET /api/v1/now", s.requireAuth(http.HandlerFunc(s.handleNow)))
	mux.Handle("GET /api/v1/now/since", s.requireAuth(http.HandlerFunc(s.handleNowSince)))
	mux.Handle("POST /api/v1/now/beat", s.requireAuth(http.HandlerFunc(s.handleNowBeat)))
	mux.Handle("POST /api/v1/now/seen", s.requireAuth(http.HandlerFunc(s.handleNowSeen)))
}

// attentionReady answers 503 when the service is not wired.
func (s *Server) attentionReady(w http.ResponseWriter) bool {
	if s.deps.Attention == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "The attention queue is not available in this server.")
		return false
	}
	return true
}

func (s *Server) handleAttention(w http.ResponseWriter, r *http.Request) {
	if !s.attentionReady(w) {
		return
	}
	writeData(w, http.StatusOK, s.deps.Attention.Snapshot(r.Context()))
}

type attentionItemRequest struct {
	ID    string     `json:"id"`
	Until *time.Time `json:"until,omitempty"`
}

func (s *Server) handleAttentionSnooze(w http.ResponseWriter, r *http.Request) {
	var request attentionItemRequest
	if !s.attentionReady(w) || !decodeStrict(w, r, 2<<10, &request) {
		return
	}
	var until time.Time
	if request.Until != nil {
		until = *request.Until
	}
	snapshot, err := s.deps.Attention.Snooze(r.Context(), request.ID, until)
	s.attentionAnswer(w, r, snapshot, err)
}

func (s *Server) handleAttentionUnsnooze(w http.ResponseWriter, r *http.Request) {
	var request attentionItemRequest
	if !s.attentionReady(w) || !decodeStrict(w, r, 2<<10, &request) {
		return
	}
	snapshot, err := s.deps.Attention.Unsnooze(r.Context(), request.ID)
	s.attentionAnswer(w, r, snapshot, err)
}

func (s *Server) handleAttentionDismiss(w http.ResponseWriter, r *http.Request) {
	var request attentionItemRequest
	if !s.attentionReady(w) || !decodeStrict(w, r, 2<<10, &request) {
		return
	}
	snapshot, err := s.deps.Attention.Dismiss(r.Context(), request.ID)
	s.attentionAnswer(w, r, snapshot, err)
}

func (s *Server) attentionAnswer(w http.ResponseWriter, r *http.Request, snapshot attention.Snapshot, err error) {
	switch {
	case err == nil:
		writeData(w, http.StatusOK, snapshot)
	case errors.Is(err, attention.ErrUnknownItem):
		writeError(w, http.StatusNotFound, codeNotFound, "That item is no longer waiting on you; it was resolved meanwhile.")
	case errors.Is(err, attention.ErrNotDismissable):
		writeError(w, http.StatusConflict, codeConflict, "Only a failed job can be dismissed; answer or resolve the others.")
	default:
		s.internalError(w, r, "changing an attention item", err)
	}
}

// nowResponse is GET /now.
type nowResponse struct {
	Rows   []attention.Row  `json:"rows"`
	Counts attention.Counts `json:"counts"`
}

func (s *Server) handleNow(w http.ResponseWriter, r *http.Request) {
	if !s.attentionReady(w) {
		return
	}
	rows, err := s.deps.Attention.Overview(r.Context(), s.deps.Now.Overview)
	if err != nil {
		s.internalError(w, r, "building the Now view", err)
		return
	}
	if rows == nil {
		rows = []attention.Row{}
	}
	writeData(w, http.StatusOK, nowResponse{Rows: rows, Counts: s.deps.Attention.Snapshot(r.Context()).Counts})
}

func (s *Server) handleNowSince(w http.ResponseWriter, r *http.Request) {
	if !s.attentionReady(w) {
		return
	}
	var src attention.SinceSources
	if s.deps.Now.Since != nil {
		var err error
		if src, err = s.deps.Now.Since(r); err != nil {
			s.internalError(w, r, "reading what happened since this browser left", err)
			return
		}
	}
	since, err := s.deps.Attention.SinceFor(r.Context(), r.URL.Query().Get("browser"), src)
	if errors.Is(err, attention.ErrBadBrowser) {
		writeError(w, http.StatusBadRequest, codeValidation, "browser must be the id this browser keeps (8 to 64 letters, digits, _ or -).")
		return
	}
	if err != nil {
		s.internalError(w, r, "reading since you left", err)
		return
	}
	writeData(w, http.StatusOK, since)
}

type browserRequest struct {
	Browser string `json:"browser"`
}

func (s *Server) handleNowBeat(w http.ResponseWriter, r *http.Request) {
	if s.attentionReady(w) {
		s.browserCall(w, r, s.deps.Attention.Beat)
	}
}

func (s *Server) handleNowSeen(w http.ResponseWriter, r *http.Request) {
	if s.attentionReady(w) {
		s.browserCall(w, r, s.deps.Attention.MarkSeen)
	}
}

// browserCall decodes {browser} and applies call to it.
func (s *Server) browserCall(w http.ResponseWriter, r *http.Request, call func(context.Context, string) error) {
	var request browserRequest
	if !decodeStrict(w, r, 1<<10, &request) {
		return
	}
	err := call(r.Context(), request.Browser)
	if errors.Is(err, attention.ErrBadBrowser) {
		writeError(w, http.StatusBadRequest, codeValidation, "browser must be the id this browser keeps (8 to 64 letters, digits, _ or -).")
		return
	}
	if err != nil {
		s.internalError(w, r, "recording this browser", err)
		return
	}
	writeData(w, http.StatusOK, map[string]bool{"ok": true})
}
