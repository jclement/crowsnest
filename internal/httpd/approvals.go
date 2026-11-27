// Approvals in the browser API, and the notification action endpoint (DESIGN.md 8.7).
//
//	GET  /api/v1/approvals?status=pending|all   the list, newest first (pending by default)
//	GET  /api/v1/approvals/{id}                  one
//	POST /api/v1/approvals/{id}/decide           {decision: allow|deny, note, confirm}
//	POST /api/v1/push/action                     {token, action}: a notification's button
//
// The first three need the session like every browser route (and Origin and X-Crowsnest on the
// POST). /push/action is the one route that does not: the service worker calls it with the
// single-use, item-scoped action token the push carried (internal/actions), never the cookie. It
// still passes the host and origin checks and needs X-Crowsnest, so a cross-site page cannot
// call it, and the token is the only authority it accepts: it can answer one item, once, with one
// of the actions it was minted for, within minutes. An elevated or dangerous approval is never
// given a token, and the approvals service refuses a notification's answer for one regardless.
//
// There is no "always allow" here or anywhere in the browser: widening an agent's permanent
// permissions is a terminal action.

package httpd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/actions"
	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/push"
	"github.com/jclement/crowsnest/internal/questions"
)

// DecideRequest is the body of POST /approvals/{id}/decide.
type DecideRequest struct {
	Decision string `json:"decision"` // allow | deny
	Note     string `json:"note"`
	// Confirm is the explicit second confirmation a dangerous allow needs.
	Confirm bool `json:"confirm"`
	// Remember, on an allow, also stores a job rule with this pattern (DESIGN.md 8.7, Policy).
	Remember *RememberRequest `json:"remember,omitempty"`
}

// RememberRequest is "Allow and remember for this job": the pattern the human confirmed.
type RememberRequest struct {
	Pattern string `json:"pattern"`
}

// ActionRequest is the body of POST /push/action.
type ActionRequest struct {
	Token  string `json:"token"`
	Action string `json:"action"`
}

// ActionResult is what /push/action answers: a sentence for the result notification.
type ActionResult struct {
	Item    string `json:"item"`
	Message string `json:"message"`
}

func (s *Server) registerApprovalRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/approvals", s.requireAuth(http.HandlerFunc(s.handleApprovalList)))
	mux.Handle("GET /api/v1/approvals/{id}", s.requireAuth(http.HandlerFunc(s.handleApprovalGet)))
	mux.Handle("POST /api/v1/approvals/{id}/decide", s.requireAuth(http.HandlerFunc(s.handleApprovalDecide)))
	mux.HandleFunc("POST /api/v1/push/action", s.handlePushAction)
}

func (s *Server) approvalService(w http.ResponseWriter) *approvals.Service {
	if s.deps.Approvals == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Approvals are not available on this server.")
	}
	return s.deps.Approvals
}

func (s *Server) handleApprovalList(w http.ResponseWriter, r *http.Request) {
	service := s.approvalService(w)
	if service == nil {
		return
	}
	var statuses []approvals.Status
	switch status := r.URL.Query().Get("status"); status {
	case "", "pending":
		statuses = []approvals.Status{approvals.StatusPending}
	case "all":
	default:
		statuses = []approvals.Status{approvals.Status(status)}
	}
	list, err := service.List(r.Context(), statuses, 200)
	if err != nil {
		s.deps.Logger.Error("listing approvals", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "Could not list approvals; the server log has the detail.")
		return
	}
	writeData(w, http.StatusOK, list)
}

func (s *Server) handleApprovalGet(w http.ResponseWriter, r *http.Request) {
	service := s.approvalService(w)
	if service == nil {
		return
	}
	approval, err := service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeApprovalError(w, err)
		return
	}
	writeData(w, http.StatusOK, approval)
}

func (s *Server) handleApprovalDecide(w http.ResponseWriter, r *http.Request) {
	service := s.approvalService(w)
	if service == nil {
		return
	}
	var request DecideRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request); err != nil || (request.Decision != "allow" && request.Decision != "deny") {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON with decision "allow" or "deny", and an optional note.`)
		return
	}
	decision := approvals.Decision{Allow: request.Decision == "allow", Note: request.Note, Confirmed: request.Confirm, Via: approvals.ViaBrowser}
	if request.Remember != nil {
		decision.Remember = &request.Remember.Pattern
	}
	approval, err := service.Decide(r.Context(), r.PathValue("id"), decision)
	if err != nil {
		s.writePolicyError(w, err)
		return
	}
	s.journalDecision(r, approval)
	if request.Remember != nil {
		s.journalRemember(r, approval, request.Remember.Pattern)
	}
	writeData(w, http.StatusOK, approval)
}

func (s *Server) journalDecision(r *http.Request, approval approvals.Approval) {
	verb := "allowed"
	if approval.Status == approvals.StatusDenied {
		verb = "denied"
	}
	s.deps.Journal.Add(r.Context(), journal.KindAnswer, "You "+verb+" "+approval.ID+": "+approval.Summary,
		map[string]any{"approval": approval.ID, "decision": verb, "via": approval.DecidedVia, "risk": approval.Risk, "has_note": approval.Note != ""})
}

func (s *Server) writeApprovalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, approvals.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "No such approval.")
	case errors.Is(err, approvals.ErrNotPending):
		writeError(w, http.StatusConflict, codeConflict, "This approval is no longer waiting: it was answered, expired or the agent moved on.")
	case errors.Is(err, approvals.ErrConfirm):
		writeError(w, http.StatusConflict, "CONFIRM_REQUIRED", "This is a dangerous action. Confirm it explicitly to allow it.")
	case errors.Is(err, approvals.ErrVia):
		writeError(w, http.StatusForbidden, codeForbidden, err.Error())
	default:
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
	}
}

// handlePushAction answers one item from a notification's button, authorised by its token alone.
func (s *Server) handlePushAction(w http.ResponseWriter, r *http.Request) {
	if s.deps.Actions == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Notification actions are not available on this server.")
		return
	}
	var request ActionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send JSON with token and action.")
		return
	}
	item, err := s.deps.Actions.Redeem(r.Context(), request.Token, request.Action)
	if err != nil {
		writeError(w, http.StatusForbidden, codeForbidden, actions.ErrInvalid.Error())
		return
	}
	if request.Action == push.ActionSnooze {
		s.snoozeFromNotification(w, r, item)
		return
	}
	kind, id, _ := strings.Cut(item, ":")
	switch kind {
	case "approval":
		service := s.approvalService(w)
		if service == nil {
			return
		}
		approval, err := service.Decide(r.Context(), id, approvals.Decision{Allow: request.Action == "allow", Via: approvals.ViaNotification})
		if err != nil {
			s.writeApprovalError(w, err)
			return
		}
		s.journalDecision(r, approval)
		verb := "Allowed"
		if approval.Status == approvals.StatusDenied {
			verb = "Denied"
		}
		writeData(w, http.StatusOK, ActionResult{Item: item, Message: verb + ": " + approval.Summary})
	case "question":
		service := s.questionService(w)
		if service == nil {
			return
		}
		// A gate is never answered from a notification (R5-S2, questions.GateShaped, the same rule the
// notices are built with; this endpoint is the authority): no button is offered for one, and
		// a token that somehow names one is refused here too. A gate's context is fixed when it
		// is asked, so reading it first cannot race.
		if asked, err := service.Get(r.Context(), id); err != nil {
			s.writeQuestionError(w, r, err)
			return
		} else if questions.GateShaped(asked.Context, optionKeys(asked.Options)) {
			writeError(w, http.StatusForbidden, codeForbidden, "This question grants something (one of crowsnest's own gates, or an allow or approve); answer it on its card in the app.")
			return
		}
		value, _ := json.Marshal(request.Action)
		question, err := service.Answer(r.Context(), id, questions.AnswerInput{Value: value})
		if err != nil {
			s.writeQuestionError(w, r, err)
			return
		}
		s.deps.Journal.Add(r.Context(), journal.KindAnswer, "You answered "+question.ID+" from a notification: "+question.Title,
			map[string]any{"question": question.ID, "answer_value": request.Action, "via": "notification"})
		writeData(w, http.StatusOK, ActionResult{Item: item, Message: "Answered: " + question.Title})
	default:
		writeError(w, http.StatusForbidden, codeForbidden, actions.ErrInvalid.Error())
	}
}

// notificationSnooze is how long a notification's Snooze hides an item: the queue's own snooze.
const notificationSnooze = 10 * time.Minute

// snoozeFromNotification is a notification's Snooze button: the item leaves the queue (and the
// count) on every device for ten minutes. It decides nothing, so any item may carry it, including
// approvals and questions that may never be answered from a notification (R5-S2, R5-S7).
func (s *Server) snoozeFromNotification(w http.ResponseWriter, r *http.Request, item string) {
	if s.deps.Attention == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Snoozing is not available on this server.")
		return
	}
	if _, err := s.deps.Attention.Snooze(r.Context(), item, time.Now().Add(notificationSnooze)); err != nil {
		writeData(w, http.StatusOK, ActionResult{Item: item, Message: "Nothing to snooze: it was already dealt with."})
		return
	}
	writeData(w, http.StatusOK, ActionResult{Item: item, Message: "Snoozed for 10 minutes. It is still waiting in crowsnest."})
}

// optionKeys lists a question's option keys.
func optionKeys(options []questions.Option) []string {
	keys := make([]string, 0, len(options))
	for _, option := range options {
		keys = append(keys, option.Key)
	}
	return keys
}
