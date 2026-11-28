// Browser routes for line comments on a diff (DESIGN.md 6.21). Like the rest of /changes they need
// a session, and the state-changing ones pass the origin check and need X-Crowsnest. {who} is
// jobs/{id}, projects/{ref} or orchestrator:
//
//	GET    /api/v1/changes/{who}/comments                 the notes with their status and counts
//	POST   /api/v1/changes/{who}/comments                 {path, scope, side, line_start, line_end, body, hash}
//	PATCH  /api/v1/changes/{who}/comments/{cid}           {body}
//	DELETE /api/v1/changes/{who}/comments/{cid}
//	POST   /api/v1/changes/{who}/comments/{cid}/dismiss   hide an outdated note
//	POST   /api/v1/changes/{who}/comments/{cid}/resend    back with the drafts
//	POST   /api/v1/changes/{who}/comments/send            {overall, dry_run, force}
//
// Send composes every pending note into one message (internal/changes) and delivers it: to a job by
// its message path or as a fix job (internal/jobs, comments.go there), to a lead or the orchestrator
// as a prompt. A message over 4 KB goes as an attached file in the agent's inbox, with a line
// pointing at it. Only a delivery that succeeded marks the notes sent.

package httpd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/changes"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
)

// maxInlineComments is the size past which a review message goes as an attached file (a long
// paste into a terminal is slow and hard for the agent to refer back to).
const maxInlineComments = 4 << 10

func (s *Server) registerCommentRoutes(mux *http.ServeMux, path string, ask func(*http.Request) changes.Ask) {
	auth := func(handler http.HandlerFunc) http.Handler { return s.requireAuth(handler) }
	mux.Handle("GET "+path+"/comments", auth(func(w http.ResponseWriter, r *http.Request) { s.handleComments(w, r, ask(r)) }))
	mux.Handle("POST "+path+"/comments", auth(func(w http.ResponseWriter, r *http.Request) { s.handleCommentAdd(w, r, ask(r)) }))
	mux.Handle("POST "+path+"/comments/send", auth(func(w http.ResponseWriter, r *http.Request) { s.handleCommentSend(w, r, ask(r)) }))
	mux.Handle("PATCH "+path+"/comments/{cid}", auth(func(w http.ResponseWriter, r *http.Request) { s.handleCommentEdit(w, r, ask(r)) }))
	mux.Handle("DELETE "+path+"/comments/{cid}", auth(func(w http.ResponseWriter, r *http.Request) {
		s.handleCommentChange(w, r, ask(r), func(ctx context.Context, service *changes.Service, a changes.Ask, id int64) error {
			return service.DeleteComment(ctx, a, id)
		})
	}))
	mux.Handle("POST "+path+"/comments/{cid}/dismiss", auth(func(w http.ResponseWriter, r *http.Request) {
		s.handleCommentChange(w, r, ask(r), func(ctx context.Context, service *changes.Service, a changes.Ask, id int64) error {
			return service.DismissComment(ctx, a, id)
		})
	}))
	mux.Handle("POST "+path+"/comments/{cid}/resend", auth(func(w http.ResponseWriter, r *http.Request) {
		s.handleCommentChange(w, r, ask(r), func(ctx context.Context, service *changes.Service, a changes.Ask, id int64) error {
			return service.ResendComment(ctx, a, id)
		})
	}))
}

func (s *Server) handleComments(w http.ResponseWriter, r *http.Request, ask changes.Ask) {
	service := s.changesService(w)
	if service == nil {
		return
	}
	list, err := service.Comments(r.Context(), ask)
	if err != nil {
		s.writeChangesError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, list)
}

func decodeCommentBody(w http.ResponseWriter, r *http.Request, into any, example string) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(into); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send JSON like "+example+".")
		return false
	}
	return true
}

func (s *Server) handleCommentAdd(w http.ResponseWriter, r *http.Request, ask changes.Ask) {
	service := s.changesService(w)
	if service == nil {
		return
	}
	var body changes.CommentAsk
	if !decodeCommentBody(w, r, &body, `{"path":"a.go","side":"new","line_start":3,"line_end":4,"body":"…","hash":"…"}`) {
		return
	}
	comment, err := service.AddComment(r.Context(), ask, body)
	if err != nil {
		s.writeChangesError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, comment)
}

func commentID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("cid"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, codeNotFound, "No such comment.")
		return 0, false
	}
	return id, true
}

func (s *Server) handleCommentEdit(w http.ResponseWriter, r *http.Request, ask changes.Ask) {
	var body struct {
		Body string `json:"body"`
	}
	if !decodeCommentBody(w, r, &body, `{"body":"…"}`) {
		return
	}
	s.handleCommentChange(w, r, ask, func(ctx context.Context, service *changes.Service, a changes.Ask, id int64) error {
		return service.EditComment(ctx, a, id, body.Body)
	})
}

// handleCommentChange runs one change on comment {cid} and answers with the subject's notes.
func (s *Server) handleCommentChange(w http.ResponseWriter, r *http.Request, ask changes.Ask, change func(context.Context, *changes.Service, changes.Ask, int64) error) {
	service := s.changesService(w)
	if service == nil {
		return
	}
	id, ok := commentID(w, r)
	if !ok {
		return
	}
	if err := change(r.Context(), service, ask, id); err != nil {
		s.writeChangesError(w, r, err)
		return
	}
	s.handleComments(w, r, ask)
}

// commentSendBody is POST …/comments/send.
type commentSendBody struct {
	Overall string `json:"overall"`
	// DryRun answers with the composed message and sends nothing (the preview).
	DryRun bool `json:"dry_run"`
	// Force types even over a draft someone left in the agent's input box (a message path only).
	// With every note already in a send waiting for the job, it lets that send type over a draft.
	Force bool `json:"force"`
	// SendID is the browser's id for this send: a request repeated with it (a lost answer) gets
	// the first one's result instead of a second send (R9-C5).
	SendID string `json:"send_id"`
}

// CommentSendResult is what a send answers.
type CommentSendResult struct {
	Message string   `json:"message"`
	Count   int      `json:"count"`
	Hidden  int      `json:"hidden"`
	Mixed   []string `json:"mixed,omitempty"`
	// Sent is false for a dry run. How is typed, queued, waiting or fix_job; Why says what holds a
	// waiting one; FixJob is the job making the changes for a finished job. A waiting send's notes
	// are marked sent only once it is typed.
	Sent     bool       `json:"sent"`
	How      string     `json:"how,omitempty"`
	Why      string     `json:"why,omitempty"`
	FixJob   string     `json:"fix_job,omitempty"`
	Attached string     `json:"attached,omitempty"`
	At       *time.Time `json:"at,omitempty"`
}

// handleCommentSend sends a subject's due notes (R9-C5): the notes are claimed at the versions
// composed before anything is delivered, so two sends never carry the same note; delivery settles
// the claim (sent at the version carried, or due again); a job that may not continue yet keeps the
// claim in its feedback batch until the batch is typed.
func (s *Server) handleCommentSend(w http.ResponseWriter, r *http.Request, ask changes.Ask) {
	service := s.changesService(w)
	if service == nil {
		return
	}
	var body commentSendBody
	if r.ContentLength != 0 && !decodeCommentBody(w, r, &body, `{"overall":"…"}`) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), jobActionTimeout)
	defer cancel()
	if body.DryRun {
		out, err := service.Outgoing(ctx, ask, body.Overall)
		if err != nil {
			s.writeChangesError(w, r, err)
			return
		}
		writeData(w, http.StatusOK, CommentSendResult{Message: out.Body, Count: len(out.IDs), Hidden: out.Hidden, Mixed: out.Mixed})
		return
	}
	if prior, ok := s.commentSends.get(body.SendID); ok {
		writeData(w, http.StatusOK, prior)
		return
	}
	claim := "send:" + newSendID(body.SendID)
	out, err := service.Claim(ctx, ask, body.Overall, claim)
	if err != nil {
		if body.Force && s.forceWaitingComments(ctx, w, r, ask) {
			return
		}
		s.writeChangesError(w, r, err)
		return
	}
	result := CommentSendResult{Message: out.Body, Count: len(out.IDs), Hidden: out.Hidden, Mixed: out.Mixed}
	var outcome changes.SendOutcome
	switch out.Target.Kind {
	case changes.KindJob:
		outcome = s.sendCommentsToJob(ctx, w, r, out, body.Force, &result)
	default:
		outcome = s.sendCommentsToLead(ctx, w, r, out, body.Force, &result)
	}
	settleCtx := context.WithoutCancel(ctx)
	if outcome != changes.SendDelivered {
		if err := service.Settle(settleCtx, claim, outcome); err != nil {
			s.deps.Logger.Warn("handing back unsent comments", "subject", out.Target.Subject, "err", err)
		}
		return // the error answer is written
	}
	if result.How != jobs.CommentWaiting {
		if err := service.Settle(settleCtx, claim, changes.SendDelivered); err != nil {
			s.internalError(w, r, "recording sent comments", err)
			return
		}
	}
	now := time.Now().UTC()
	result.Sent, result.At = true, &now
	s.commentSends.put(body.SendID, result)
	s.deps.Journal.Add(r.Context(), journal.KindSend, fmt.Sprintf("You sent %d review comment(s) on %s's diff (%s)", result.Count, out.Target.Label, result.How),
		map[string]any{"subject": out.Target.Subject, "comments": result.Count, "how": result.How, "fix_job": result.FixJob})
	writeData(w, http.StatusOK, result)
}

// forceWaitingComments is Send anyway for notes already waiting in a job's feedback queue: the
// waiting send may now type over a draft (R9-C14). It answers and reports true when it applied.
func (s *Server) forceWaitingComments(ctx context.Context, w http.ResponseWriter, r *http.Request, ask changes.Ask) bool {
	service := s.changesService(w)
	jobsService := s.deps.Jobs
	if service == nil || jobsService == nil {
		return false
	}
	list, err := service.Comments(ctx, ask)
	if err != nil || list.Kind != changes.KindJob || list.Waiting == 0 {
		return false
	}
	forced, err := jobsService.ForceWaitingComments(ctx, list.JobID)
	if err != nil || forced == 0 {
		return false
	}
	writeData(w, http.StatusOK, CommentSendResult{Count: forced, Sent: true, How: jobs.CommentWaiting,
		Why: "the waiting comments will be typed over the draft in the agent's input box once the job may continue"})
	return true
}

// sendCommentsToJob delivers to a job. On failure it writes the error response and says what
// became of the notes (returned: provably not delivered; uncertain: it may have been typed).
func (s *Server) sendCommentsToJob(ctx context.Context, w http.ResponseWriter, r *http.Request, out changes.Outgoing, force bool, result *CommentSendResult) changes.SendOutcome {
	service := s.jobService(w)
	if service == nil {
		return changes.SendReturned
	}
	id := out.Target.JobID
	plan, err := service.PlanReviewComments(ctx, id)
	if err != nil {
		s.writeJobError(w, r, err)
		return changes.SendReturned
	}
	text := out.Body
	var delivered []attach.Attachment
	if plan.Route != jobs.CommentFixJob && len(text) > maxInlineComments {
		var ok bool
		if delivered, ok = s.attachComments(ctx, w, r, out, s.inboxForJob(ctx, id), result); !ok {
			return changes.SendReturned
		}
		text = attachedCommentsText(len(out.IDs), delivered)
	}
	delivery, err := service.SendReviewComments(ctx, id, jobs.CommentSend{Body: text, Count: len(out.IDs), Force: force, Ref: out.Claim})
	if errors.Is(err, jobs.ErrMaybeTyped) {
		writeError(w, http.StatusBadGateway, codeConflict, "crowsnest lost track of typing the comments; they may or may not have arrived. They are shown as unsent: send them again if the agent did not get them.")
		return changes.SendUncertain
	}
	if err != nil {
		outcome := promptOutcome(err)
		if outcome == changes.SendReturned {
			s.takeBack(ctx, delivered, err)
		}
		s.writeJobError(w, r, err)
		return outcome
	}
	result.How, result.Why, result.FixJob = delivery.How, delivery.Why, delivery.FixJob
	return changes.SendDelivered
}

// promptOutcome says, from a failed prompt's error, whether anything may have been typed: a
// refusal before typing (a dialog, a draft, no agent, a validation error) provably typed nothing.
func promptOutcome(err error) changes.SendOutcome {
	var blocked *orch.BlockedError
	var draft *orch.DraftError
	var invalid *jobs.ValidationError
	switch {
	case errors.As(err, &blocked), errors.As(err, &draft), errors.As(err, &invalid), errors.Is(err, orch.ErrNotTyped),
		errors.Is(err, orch.ErrEmptyPrompt), errors.Is(err, orch.ErrPromptTooLong), errors.Is(err, jobs.ErrNoAgent), errors.Is(err, jobs.ErrNotFound):
		return changes.SendReturned
	}
	return changes.SendUncertain
}

// newSendID is the browser's send id when it gave a usable one, else a fresh random one.
func newSendID(given string) string {
	if len(given) >= 8 && len(given) <= 64 && strings.Trim(given, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") == "" {
		return given
	}
	raw := make([]byte, 12)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

// sendResults remembers the last few sends' results by the browser's send id, so a repeated
// request answers what the first one did (an in-process memory: a restart forgets it, and the
// claim sweep marks a send lost in a crash as uncertain instead).
type sendResults struct {
	mu    sync.Mutex
	order []string
	byID  map[string]CommentSendResult
}

// maxSendResults is how many sends are remembered.
const maxSendResults = 128

func (m *sendResults) get(id string) (CommentSendResult, bool) {
	if id == "" {
		return CommentSendResult{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result, ok := m.byID[id]
	return result, ok
}

func (m *sendResults) put(id string, result CommentSendResult) {
	if id == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byID == nil {
		m.byID = map[string]CommentSendResult{}
	}
	if _, ok := m.byID[id]; !ok {
		m.order = append(m.order, id)
	}
	m.byID[id] = result
	for len(m.order) > maxSendResults {
		delete(m.byID, m.order[0])
		m.order = m.order[1:]
	}
}

// sendCommentsToLead delivers to a project's lead or to the orchestrator as a prompt (a human's
// chat to them; the pause is for automation and does not hold it).
func (s *Server) sendCommentsToLead(ctx context.Context, w http.ResponseWriter, r *http.Request, out changes.Outgoing, force bool, result *CommentSendResult) changes.SendOutcome {
	if s.deps.Herdr == nil {
		writeHerdrUnavailable(w)
		return changes.SendReturned
	}
	var pane, agent string
	var inbox attach.Inbox
	switch out.Target.Kind {
	case changes.KindProject:
		var lead interface {
			Pane() string
			AgentName() string
		}
		if s.deps.Leads != nil {
			if running := s.deps.Leads.Get(out.Target.ProjectID); running != nil {
				lead = running.Agent
			}
		}
		if lead == nil || lead.Pane() == "" {
			writeError(w, http.StatusConflict, codeConflict, "The project's lead is not running; start it, then send the comments.")
			return changes.SendReturned
		}
		pane, agent = lead.Pane(), lead.AgentName()
		inbox = attach.Layout{StateDir: filepath.Join(s.deps.AttachLayout.StateDir, "leads", out.Target.Project)}.Orchestrator()
	default:
		if s.deps.Orchestrator == nil || s.deps.Orchestrator.Status().PaneID == "" {
			writeError(w, http.StatusConflict, codeConflict, "The orchestrator is not running; start it, then send the comments.")
			return changes.SendReturned
		}
		pane, agent, inbox = s.deps.Orchestrator.Status().PaneID, s.deps.Orchestrator.AgentName(), s.deps.AttachLayout.Orchestrator()
	}
	text := out.Body
	var delivered []attach.Attachment
	if len(text) > maxInlineComments {
		var ok bool
		if delivered, ok = s.attachComments(ctx, w, r, out, inbox, result); !ok {
			return changes.SendReturned
		}
		text = attachedCommentsText(len(out.IDs), delivered)
	}
	sent, err := orch.Prompt(ctx, s.deps.Herdr, pane, orch.PromptRequest{Text: text, ForceOverDraft: force, ExpectAgent: agent})
	if err != nil {
		outcome := promptOutcome(err)
		if outcome == changes.SendReturned {
			s.takeBack(ctx, delivered, err)
		}
		s.promptError(w, r, err)
		return outcome
	}
	if sent.Uncertain {
		// Handed to herdr and the answer lost: it may have been typed. The notes are due again,
		// marked so, and the human decides.
		writeError(w, http.StatusBadGateway, codeConflict, "crowsnest lost track of typing the comments; they may or may not have arrived. They are shown as unsent: send them again if the agent did not get them.")
		return changes.SendUncertain
	}
	result.How = "typed"
	if sent.Queued {
		result.How = "queued"
	}
	return changes.SendDelivered
}

// attachComments stores the composed message as review-comments.md and copies it into inbox.
func (s *Server) attachComments(ctx context.Context, w http.ResponseWriter, r *http.Request, out changes.Outgoing, inbox attach.Inbox, result *CommentSendResult) ([]attach.Attachment, bool) {
	service := s.attachments(w)
	if service == nil {
		return nil, false
	}
	upload, err := service.Upload(ctx, "review-comments.md", strings.NewReader(out.Body+"\n"))
	if err != nil {
		s.writeAttachmentError(w, r, err)
		return nil, false
	}
	delivered, ok := s.deliverAttachments(w, r, []string{upload.ID}, inbox, attach.RoleMessage, "")
	if ok && len(delivered) > 0 {
		result.Attached = delivered[0].Name
	}
	return delivered, ok
}

// attachedCommentsText is the line typed in place of a long review message.
func attachedCommentsText(count int, delivered []attach.Attachment) string {
	noun := "comments"
	if count == 1 {
		noun = "comment"
	}
	return attach.WithLines(fmt.Sprintf("Review comments on your changes: %d %s from the diff view, in the attached file. Read it, address each one, and say what you changed for each.", count, noun), delivered)
}
