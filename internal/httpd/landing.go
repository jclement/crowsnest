// Browser routes for landing, throttling, the pause, presence and the digest (DESIGN.md 6.11 to
// 6.14, 7.13). Every route needs a session; the state-changing ones pass the origin check and
// need X-Crowsnest like every other. A land or a bulk discard also needs `"confirm": true` in its
// body (409 CONFIRM_REQUIRED without), so a confirmation the UI shows is also checked here.
//
//	GET  /api/v1/jobs/{id}/review            the Review tab: what the job produced, in reading order
//	GET  /api/v1/jobs/{id}/review/diff?path= one changed file's diff (fetched when opened)
//	POST /api/v1/jobs/{id}/land              {action: merge|pr|discard, confirm: true}
//	POST /api/v1/jobs/{id}/teardown          {confirm: true}: retry a landed job's teardown
//	POST /api/v1/jobs/{id}/start-anyway      a held job starts past the limits
//	GET  /api/v1/sweep                       finished jobs whose worktrees are still around
//	POST /api/v1/sweep/discard               {ids: [...], confirm: true}
//	GET  /api/v1/queue                       running / limit / waiting, for the board's header
//	GET  /api/v1/pause, POST /api/v1/pause {until, reason}, POST /api/v1/resume
//	GET  /api/v1/throttle, PUT /api/v1/throttle, DELETE /api/v1/throttle (back to the flags)
//	GET  /api/v1/system/heavy                heavy slots: running and queued, by job
//	GET  /api/v1/presence, POST /api/v1/presence (heartbeat), POST /api/v1/presence/gone,
//	PUT  /api/v1/presence/dnd {until}
//	GET  /api/v1/digests, GET/PUT /api/v1/digest/settings, POST /api/v1/digest/now

package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/digest"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/pause"
	"github.com/jclement/crowsnest/internal/presence"
	"github.com/jclement/crowsnest/internal/throttle"
)

// commitPattern is a full commit id (SHA-1 or SHA-256).
var commitPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// landTimeout bounds a land action: a merge, or a push and a pull request over the network.
const landTimeout = 5 * time.Minute

func (s *Server) registerLandingRoutes(mux *http.ServeMux) {
	auth := func(handler http.HandlerFunc) http.Handler { return s.requireAuth(handler) }
	mux.Handle("GET /api/v1/jobs/{id}/review", auth(s.handleReview))
	mux.Handle("GET /api/v1/jobs/{id}/review/diff", auth(s.handleReviewDiff))
	mux.Handle("POST /api/v1/jobs/{id}/land", auth(s.handleLand))
	mux.Handle("POST /api/v1/jobs/{id}/teardown", auth(s.handleTeardown))
	mux.Handle("POST /api/v1/jobs/{id}/start-anyway", auth(s.handleJobAction(func(ctx context.Context, service *jobs.Service, id string) (jobs.Job, error) {
		return service.StartAnyway(ctx, id)
	})))
	mux.Handle("GET /api/v1/sweep", auth(s.handleSweep))
	mux.Handle("POST /api/v1/sweep/discard", auth(s.handleSweepDiscard))
	mux.Handle("GET /api/v1/queue", auth(s.handleQueue))
	mux.Handle("GET /api/v1/pause", auth(s.handlePauseState))
	mux.Handle("POST /api/v1/pause", auth(s.handlePause))
	mux.Handle("POST /api/v1/resume", auth(s.handleResume))
	mux.Handle("GET /api/v1/throttle", auth(s.handleThrottle))
	mux.Handle("PUT /api/v1/throttle", auth(s.handleThrottleSave))
	mux.Handle("DELETE /api/v1/throttle", auth(s.handleThrottleReset))
	mux.Handle("GET /api/v1/system/heavy", auth(s.handleHeavy))
	mux.Handle("GET /api/v1/presence", auth(s.handlePresence))
	mux.Handle("POST /api/v1/presence", auth(s.handleHeartbeat))
	mux.Handle("POST /api/v1/presence/gone", auth(s.handlePresenceGone))
	mux.Handle("PUT /api/v1/presence/dnd", auth(s.handleDND))
	mux.Handle("GET /api/v1/digests", auth(s.handleDigests))
	mux.Handle("GET /api/v1/digest/settings", auth(s.handleDigestSettings))
	mux.Handle("PUT /api/v1/digest/settings", auth(s.handleDigestSettingsSave))
	mux.Handle("POST /api/v1/digest/now", auth(s.handleDigestNow))
}

func decodeSmall(w http.ResponseWriter, r *http.Request, into any, example string) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(into); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send JSON like "+example+".")
		return false
	}
	return true
}

func confirmRequired(w http.ResponseWriter) {
	writeError(w, http.StatusConflict, "CONFIRM_REQUIRED", "Confirm it explicitly: this changes branches and removes a worktree.")
}

// ---- Review and Land ----------------------------------------------------------------------

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	view, err := service.ReviewOf(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, view)
}

func (s *Server) handleReviewDiff(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	diff, err := service.FileDiff(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, diff)
}

type landBody struct {
	Action  string `json:"action"`
	Confirm bool   `json:"confirm"`
	// Override lands a revision crowsnest's evidence does not support (stale, failed or missing
	// check, or a base that moved on: DESIGN.md 6.15). Only ever sent by an explicit second
	// confirmation in the app.
	Override bool `json:"override"`
	// ExpectedHead is the commit the Review tab showed (R5-S4): required to merge or push, so
	// what lands is what was reviewed.
	ExpectedHead string `json:"expected_head"`
}

func (s *Server) handleLand(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var body landBody
	if !decodeSmall(w, r, &body, `{"action":"merge","confirm":true}`) {
		return
	}
	if !body.Confirm {
		confirmRequired(w)
		return
	}
	if body.Action != jobs.LandDiscard && !commitPattern.MatchString(body.ExpectedHead) {
		writeError(w, http.StatusBadRequest, codeValidation, "Send expected_head: the commit the Review tab showed (what lands is what you reviewed).")
		return
	}
	if s.landUnviewed(r.Context(), w, r.PathValue("id"), body.Action) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), landTimeout)
	defer cancel()
	result, err := service.LandWith(ctx, r.PathValue("id"), body.Action, "you", jobs.LandFlags{Override: body.Override, Head: body.ExpectedHead})
	var evidence *jobs.EvidenceError
	if errors.As(err, &evidence) {
		writeError(w, http.StatusConflict, "EVIDENCE_"+strings.ToUpper(evidence.Code), evidence.Message)
		return
	}
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, result)
}

func (s *Server) handleTeardown(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var body landBody
	if !decodeSmall(w, r, &body, `{"confirm":true}`) {
		return
	}
	if !body.Confirm {
		confirmRequired(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), landTimeout)
	defer cancel()
	result, err := service.Teardown(ctx, r.PathValue("id"))
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, result)
}

func (s *Server) handleSweep(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	items, err := service.Sweep(r.Context())
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeList(w, items, ListMeta{Total: len(items)})
}

type sweepDiscardBody struct {
	IDs     []string `json:"ids"`
	Confirm bool     `json:"confirm"`
}

// sweepOutcome is one job's result in a bulk discard.
type sweepOutcome struct {
	ID       string `json:"id"`
	OK       bool   `json:"ok"`
	TornDown bool   `json:"torn_down"`
	Error    string `json:"error,omitempty"`
}

func (s *Server) handleSweepDiscard(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	var body sweepDiscardBody
	if !decodeSmall(w, r, &body, `{"ids":["j_3"],"confirm":true}`) {
		return
	}
	if !body.Confirm {
		confirmRequired(w)
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > 100 {
		writeError(w, http.StatusBadRequest, codeValidation, "Name between 1 and 100 jobs to discard.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), landTimeout)
	defer cancel()
	out := make([]sweepOutcome, 0, len(body.IDs))
	for _, id := range body.IDs {
		result, err := service.Teardown(ctx, id)
		outcome := sweepOutcome{ID: id, OK: err == nil, TornDown: result.TornDown}
		if err != nil {
			outcome.Error = err.Error()
		} else if result.TeardownError != "" {
			outcome.Error = result.TeardownError
		}
		out = append(out, outcome)
	}
	writeList(w, out, ListMeta{Total: len(out)})
}

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	service := s.jobService(w)
	if service == nil {
		return
	}
	line, err := service.Queue(r.Context())
	if err != nil {
		s.writeJobError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, line)
}

// ---- Pause --------------------------------------------------------------------------------

func (s *Server) pauseSwitch(w http.ResponseWriter) *pause.Switch {
	if s.deps.Pause == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "The pause switch is not available on this server.")
	}
	return s.deps.Pause
}

func (s *Server) handlePauseState(w http.ResponseWriter, r *http.Request) {
	if sw := s.pauseSwitch(w); sw != nil {
		writeData(w, http.StatusOK, sw.State(r.Context()))
	}
}

type pauseBody struct {
	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	sw := s.pauseSwitch(w)
	if sw == nil {
		return
	}
	var body pauseBody
	if !decodeSmall(w, r, &body, `{"until":"2026-11-26T22:00:00Z","reason":"…"}`) {
		return
	}
	state, err := sw.Pause(r.Context(), "you", body.Until, body.Reason)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	writeData(w, http.StatusOK, state)
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	sw := s.pauseSwitch(w)
	if sw == nil {
		return
	}
	state, err := sw.Resume(r.Context(), "you")
	if err != nil {
		s.internalError(w, r, "resuming", err)
		return
	}
	writeData(w, http.StatusOK, state)
}

// ---- Throttle and heavy -------------------------------------------------------------------

// throttleView is GET /throttle.
type throttleView struct {
	Settings throttle.Settings    `json:"settings"`
	Defaults throttle.Settings    `json:"defaults"`
	Load     throttle.LoadState   `json:"load"`
	Queue    *jobs.QueueLine      `json:"queue,omitempty"`
	Heavy    throttle.HeavyStatus `json:"heavy"`
}

func (s *Server) throttleOf(w http.ResponseWriter) *throttle.Throttle {
	if s.deps.Throttle == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Throttling is not available on this server.")
	}
	return s.deps.Throttle
}

func (s *Server) handleThrottle(w http.ResponseWriter, r *http.Request) {
	th := s.throttleOf(w)
	if th == nil {
		return
	}
	view := throttleView{Settings: th.Settings(r.Context()), Defaults: th.Defaults(), Load: th.Load(r.Context()), Heavy: s.heavyStatus()}
	if s.deps.Jobs != nil {
		if line, err := s.deps.Jobs.Queue(r.Context()); err == nil {
			view.Queue = &line
		}
	}
	writeData(w, http.StatusOK, view)
}

func (s *Server) handleThrottleSave(w http.ResponseWriter, r *http.Request) {
	th := s.throttleOf(w)
	if th == nil {
		return
	}
	var body throttle.Settings
	if !decodeSmall(w, r, &body, `{"max_running":3,"project_limits":{"2":1},"load_per_core":2}`) {
		return
	}
	saved, err := th.Save(r.Context(), body)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	s.publishThrottle()
	writeData(w, http.StatusOK, saved)
}

func (s *Server) handleThrottleReset(w http.ResponseWriter, r *http.Request) {
	th := s.throttleOf(w)
	if th == nil {
		return
	}
	settings, err := th.Reset(r.Context())
	if err != nil {
		s.internalError(w, r, "resetting the throttle", err)
		return
	}
	s.publishThrottle()
	writeData(w, http.StatusOK, settings)
}

func (s *Server) publishThrottle() {
	if s.deps.Hub != nil {
		s.deps.Hub.Publish(hub.Event{Type: "throttle", Data: map[string]any{"at": time.Now().UTC()}})
	}
}

func (s *Server) heavyStatus() throttle.HeavyStatus {
	if s.deps.HeavyDir == "" {
		return throttle.HeavyStatus{Running: []throttle.HeavyOwner{}, Waiting: []throttle.HeavyOwner{}}
	}
	status := throttle.ReadHeavyStatus(s.deps.HeavyDir, 0)
	status.Deferred = s.deps.HeavyDeferred
	return status
}

func (s *Server) handleHeavy(w http.ResponseWriter, _ *http.Request) {
	writeData(w, http.StatusOK, s.heavyStatus())
}

// ---- Presence -----------------------------------------------------------------------------

func (s *Server) presenceOf(w http.ResponseWriter) *presence.Service {
	if s.deps.Presence == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Presence is not available on this server.")
	}
	return s.deps.Presence
}

func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	if p := s.presenceOf(w); p != nil {
		writeData(w, http.StatusOK, p.State(r.Context()))
	}
}

// heartbeatBody is a browser's heartbeat: nothing but the coarse fields presence needs.
type heartbeatBody struct {
	Client      string `json:"client"`
	Visible     bool   `json:"visible"`
	Focused     bool   `json:"focused"`
	IdleSeconds int    `json:"idle_seconds"`
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	p := s.presenceOf(w)
	if p == nil {
		return
	}
	var body heartbeatBody
	if !decodeSmall(w, r, &body, `{"client":"…","visible":true,"focused":true,"idle_seconds":5}`) {
		return
	}
	idle := min(max(body.IdleSeconds, 0), 86400)
	writeData(w, http.StatusOK, p.Heartbeat(body.Client, presence.Beat{Visible: body.Visible, Focused: body.Focused, IdleFor: time.Duration(idle) * time.Second}))
}

func (s *Server) handlePresenceGone(w http.ResponseWriter, r *http.Request) {
	p := s.presenceOf(w)
	if p == nil {
		return
	}
	var body heartbeatBody
	if !decodeSmall(w, r, &body, `{"client":"…"}`) {
		return
	}
	p.Gone(body.Client)
	writeData(w, http.StatusOK, p.State(r.Context()))
}

type dndBody struct {
	Until time.Time `json:"until"` // zero clears it
}

func (s *Server) handleDND(w http.ResponseWriter, r *http.Request) {
	p := s.presenceOf(w)
	if p == nil {
		return
	}
	var body dndBody
	if !decodeSmall(w, r, &body, `{"until":"2026-11-26T22:00:00Z"}`) {
		return
	}
	state, err := p.SetDND(r.Context(), body.Until)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	writeData(w, http.StatusOK, state)
}

// ---- Digest -------------------------------------------------------------------------------

func (s *Server) digestOf(w http.ResponseWriter) *digest.Service {
	if s.deps.Digest == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "The digest is not available on this server.")
	}
	return s.deps.Digest
}

// digestEntry is one digest in the list.
type digestEntry struct {
	ID         int64     `json:"id"`
	Day        string    `json:"day"`
	At         time.Time `json:"at"`
	Source     string    `json:"source"`
	BodyMD     string    `json:"body_md"`
	QuestionID string    `json:"question_id,omitempty"`
}

func (s *Server) handleDigests(w http.ResponseWriter, r *http.Request) {
	d := s.digestOf(w)
	if d == nil {
		return
	}
	records, err := d.List(r.Context(), 60)
	if err != nil {
		s.internalError(w, r, "listing digests", err)
		return
	}
	out := make([]digestEntry, 0, len(records))
	for _, record := range records {
		entry := digestEntry{ID: record.ID, Day: record.Day, At: record.At.UTC(), Source: record.Source, BodyMD: record.BodyMD}
		if record.QuestionID > 0 {
			entry.QuestionID = "q_" + strconv.FormatInt(record.QuestionID, 10)
		}
		out = append(out, entry)
	}
	writeList(w, out, ListMeta{Total: len(out)})
}

func (s *Server) handleDigestSettings(w http.ResponseWriter, r *http.Request) {
	if d := s.digestOf(w); d != nil {
		writeData(w, http.StatusOK, d.Settings(r.Context()))
	}
}

func (s *Server) handleDigestSettingsSave(w http.ResponseWriter, r *http.Request) {
	d := s.digestOf(w)
	if d == nil {
		return
	}
	var body digest.Settings
	if !decodeSmall(w, r, &body, `{"enabled":true,"at":"17:00"}`) {
		return
	}
	saved, err := d.SaveSettings(r.Context(), body)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	writeData(w, http.StatusOK, saved)
}

func (s *Server) handleDigestNow(w http.ResponseWriter, r *http.Request) {
	d := s.digestOf(w)
	if d == nil {
		return
	}
	result, err := d.Start(context.WithoutCancel(r.Context()))
	if err != nil {
		s.internalError(w, r, "writing the digest", err)
		return
	}
	writeData(w, http.StatusOK, result)
}
