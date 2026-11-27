// Structured approvals and the guard on the agent socket (DESIGN.md 8.7): what
// `crowsnest hook permission-request` and `crowsnest hook pre-tool-use` call.
//
//	POST /api/v1/approvals            a PermissionRequest hook's input; answers {id, status, hook_output}
//	                                  (hook_output set when a policy rule allowed it at once)
//	GET  /api/v1/approvals/{id}?wait= long-poll (at most 60 s); answers {id, status, hook_output}
//	POST /api/v1/guard/denied         the guard refused a command; journalled on the job
//
// Only an identified caller (a token crowsnest gave an agent) may request an approval, and only
// the pane that requested one may poll it: anyone else gets 403 or 404, and their hook prints
// nothing, which leaves Claude's own terminal dialog. The approval rules (DESIGN.md 8.7, Policy)
// have no route here at all: an agent must not be able to widen its own policy.

package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
)

// maxApprovalWait caps one long-poll.
const maxApprovalWait = 60 * time.Second

// ApprovalReply is what the approval routes answer.
type ApprovalReply struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// HookOutput is what the hook prints once the approval is settled ("" for nothing).
	HookOutput string `json:"hook_output,omitempty"`
}

// permissionRequestInput is the part of a PermissionRequest hook's input crowsnest keeps.
type permissionRequestInput struct {
	SessionID   string          `json:"session_id"`
	Cwd         string          `json:"cwd"`
	ToolName    string          `json:"tool_name"`
	ToolInput   json.RawMessage `json:"tool_input"`
	Suggestions json.RawMessage `json:"permission_suggestions"`
}

// GuardDenial is the body of POST /guard/denied.
type GuardDenial struct {
	Rule      string `json:"rule"`
	Reason    string `json:"reason"`
	Command   string `json:"command"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
}

func (s *Server) registerApprovalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/approvals", s.handleApprovalRequest)
	mux.HandleFunc("GET /api/v1/approvals/{id}", s.handleApprovalWait)
	mux.HandleFunc("POST /api/v1/guard/denied", s.handleGuardDenied)
}

func (s *Server) handleApprovalRequest(w http.ResponseWriter, r *http.Request) {
	if s.approvals == nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "Approvals are not available on this crowsnest.")
		return
	}
	caller := s.callerOf(r)
	if caller.PaneID == "" {
		writeError(w, http.StatusForbidden, codeForbidden, "Only an agent crowsnest started can ask for an approval.")
		return
	}
	var input permissionRequestInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHookBody)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "The hook input must be JSON: "+err.Error())
		return
	}
	jobID, agentName := s.callerJob(r.Context(), caller)
	approval, err := s.approvals.Request(r.Context(), approvals.RequestInput{
		JobID: jobID, PaneID: caller.PaneID, AgentName: agentName, SessionID: input.SessionID, Cwd: input.Cwd,
		ToolName: input.ToolName, ToolInput: input.ToolInput, Suggestions: input.Suggestions, Hooked: true,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	// An approval a policy rule allowed is settled already: the reply carries the allow, so the
	// hook prints it at once without a poll.
	writeData(w, http.StatusCreated, ApprovalReply{ID: approval.ID, Status: string(approval.Status), HookOutput: approvals.HookOutput(approval)})
}

func (s *Server) handleApprovalWait(w http.ResponseWriter, r *http.Request) {
	if s.approvals == nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "Approvals are not available on this crowsnest.")
		return
	}
	caller := s.callerOf(r)
	approval, err := s.approvals.Get(r.Context(), r.PathValue("id"))
	if err != nil || caller.PaneID == "" || approval.PaneID != caller.PaneID {
		writeError(w, http.StatusNotFound, codeNotFound, "No such approval for this agent.")
		return
	}
	wait := time.Duration(0)
	if seconds, err := strconv.Atoi(r.URL.Query().Get("wait")); err == nil && seconds > 0 {
		wait = min(time.Duration(seconds)*time.Second, maxApprovalWait)
	}
	if wait > 0 && approval.Status == approvals.StatusPending {
		approval, err = s.approvals.Wait(r.Context(), approval.ID, wait)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			writeError(w, http.StatusInternalServerError, codeInternal, "crowsnest failed to read the approval.")
			return
		}
	}
	writeData(w, http.StatusOK, ApprovalReply{ID: approval.ID, Status: string(approval.Status), HookOutput: approvals.HookOutput(approval)})
}

func (s *Server) handleGuardDenied(w http.ResponseWriter, r *http.Request) {
	caller := s.callerOf(r)
	var denial GuardDenial
	if !decodeBody(w, r, maxJobBody, &denial) {
		return
	}
	s.logger.Info("the guard refused a command", "pane", caller.PaneID, "job", caller.JobID, "rule", denial.Rule)
	if caller.PaneID == "" {
		writeData(w, http.StatusOK, map[string]bool{"recorded": false})
		return
	}
	if jobID, _ := s.callerJob(r.Context(), caller); jobID != "" && s.jobs != nil {
		_ = s.jobs.Journal(r.Context(), jobID, jobs.EventHook, "The guard refused a command ("+denial.Rule+")",
			map[string]any{"rule": denial.Rule, "reason": oneLine(denial.Reason, 400), "command": oneLine(denial.Command, 400)})
	}
	writeData(w, http.StatusOK, map[string]bool{"recorded": true})
}

// callerJob is the job a caller holds (its token's, else the one in its pane) and its agent name.
func (s *Server) callerJob(ctx context.Context, caller questions.Caller) (string, string) {
	if s.jobs == nil {
		return caller.JobID, ""
	}
	jobID := caller.JobID
	if jobID == "" && caller.PaneID != "" {
		jobID = s.jobs.JobInPane(ctx, caller.PaneID)
	}
	if jobID == "" {
		return "", ""
	}
	job, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		return jobID, ""
	}
	return jobID, job.AgentName
}

// supersedeForHook releases approvals a hook event shows the agent no longer waits on: its turn
// or session ended (a "No" in the terminal ends the turn), or the tool ran (a "Yes").
func (s *Server) supersedeForHook(ctx context.Context, event string, caller questions.Caller, input jobs.HookInput) {
	if s.approvals == nil || caller.PaneID == "" {
		return
	}
	switch event {
	case jobs.HookStop:
		s.approvals.SupersedePane(ctx, caller.PaneID, "The agent's turn ended (answered or interrupted in the terminal).")
	case jobs.HookStopFailure:
		s.approvals.SupersedePane(ctx, caller.PaneID, "The agent's turn ended with an API error.")
	case jobs.HookSessionEnd:
		s.approvals.SupersedePane(ctx, caller.PaneID, "The agent's session ended.")
	case jobs.HookPostToolUse:
		s.approvals.SupersedeRan(ctx, caller.PaneID, input.ToolName, input.ToolInput)
	}
}

func oneLine(text string, max int) string {
	runes := []rune(text)
	if len(runes) > max {
		return string(runes[:max-1]) + "…"
	}
	return text
}
