// Claude Code hooks, server side (DESIGN.md 6.3 and 6.4). `crowsnest hook <event>` forwards the
// hook's stdin JSON over the agent socket; HandleHook decides and returns what the hook prints.
//
// A hook is matched to its job by the job id baked into that job's settings.json (`--job j_N`),
// or, for the orchestrator (whose hooks carry no id), by the calling pane: the job the
// orchestrator holds right now. The hook's session_id is cross-checked against the job's: a
// different one (after /clear or a resume) is adopted and journalled.
//
// Every hook counts as activity for the heartbeat.

package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// HookInput is the union of the hook input fields crowsnest reads (Claude Code 2.1.332; the
// hooks reference lists the rest).
type HookInput struct {
	SessionID        string          `json:"session_id"`
	TranscriptPath   string          `json:"transcript_path"`
	HookEventName    string          `json:"hook_event_name"`
	StopHookActive   bool            `json:"stop_hook_active"`
	LastAssistant    string          `json:"last_assistant_message"`
	Source           string          `json:"source"`            // SessionStart
	NotificationType string          `json:"notification_type"` // Notification
	Message          string          `json:"message"`           // Notification
	Error            string          `json:"error"`             // StopFailure
	ErrorDetails     string          `json:"error_details"`     // StopFailure
	ToolName         string          `json:"tool_name"`         // PostToolUse, PermissionDenied
	ToolInput        json.RawMessage `json:"tool_input"`        // PostToolUse, PermissionDenied
	Reason           string          `json:"reason"`            // PermissionDenied, SessionEnd
}

// Hook events, as `crowsnest hook <event>` names them.
const (
	HookStop             = "stop"
	HookSessionStart     = "session-start"
	HookNotification     = "notification"
	HookStopFailure      = "stop-failure"
	HookPermissionDenied = "permission-denied"
	HookSessionEnd       = "session-end"
	HookPostToolUse      = "post-tool-use"
	// HookInstructionsLoaded is recorded by the agent socket (internal/instructions), not
	// here: it says which instruction files Claude loaded, nothing about the job's state.
	HookInstructionsLoaded = "instructions-loaded"
)

// maxDenials is how many auto-mode denials in a row make a job stuck: permissions.
const maxDenials = 3

// Retry schedules (6.4): a usage limit is retried at 5, 15 and 30 minutes, then hourly; an API
// error at 1, 5 and 15 minutes, then Jeff is told and retries stop.
var (
	limitRetries  = []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute}
	limitEvery    = time.Hour
	serverRetries = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}
)

// HookReply is what the hook prints on stdout ("" prints nothing).
type HookReply struct {
	Stdout string `json:"stdout"`
}

// HandleHook applies one hook event. jobID and callerPane come from the hook's agent token (the
// agent socket resolves it; R1b N2): a job agent's token names its job, the orchestrator's only
// its pane, whose running job is then used. A hook for no job is a no-op (and Stop is allowed).
func (s *Service) HandleHook(ctx context.Context, event, jobID, callerPane string, input HookInput) (HookReply, error) {
	id := s.resolveHookJob(ctx, jobID, callerPane)
	if id == "" || event == HookInstructionsLoaded {
		return HookReply{}, nil
	}
	switch event {
	case HookStop:
		return s.hookStop(ctx, id, input)
	case HookSessionStart:
		return s.hookSessionStart(ctx, id, input)
	case HookPostToolUse:
		_, err := s.RecordToolUse(ctx, id, input.ToolName, input.ToolInput)
		return HookReply{}, err
	case HookNotification, HookStopFailure, HookPermissionDenied, HookSessionEnd:
		_, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
			if State(r.State).Terminal() {
				return nil
			}
			s.touchLocked(r, c, input)
			s.applyHookLocked(r, c, event, input)
			return nil
		})
		return HookReply{}, err
	}
	return HookReply{}, invalid("unknown hook event %q", event)
}

// resolveHookJob finds the job a hook belongs to: the given id, else the live job held in the
// calling pane.
func (s *Service) resolveHookJob(ctx context.Context, jobID, callerPane string) string {
	if jobID != "" {
		if _, err := ParseID(jobID); err == nil {
			return jobID
		}
		return ""
	}
	if callerPane == "" {
		return ""
	}
	return s.jobInPane(ctx, callerPane)
}

// JobInPane is jobInPane for other packages: the live job held in a pane, or "".
func (s *Service) JobInPane(ctx context.Context, paneID string) string {
	return s.jobInPane(ctx, paneID)
}

// jobInPane returns the live job whose agent runs in paneID, preferring a running one, else the
// newest (a job just reserved for the orchestrator is queued with its pane set while its brief is
// typed, R2-C05). It asks the store by pane, not a page of the board (R2-C16).
func (s *Service) jobInPane(ctx context.Context, paneID string) string {
	records, err := s.opts.Store.ListJobsByPane(ctx, paneID, []string{string(StateRunning), string(StateNeedsYou), string(StateBlocked), string(StateQueued)})
	if err != nil || len(records) == 0 {
		return ""
	}
	for _, record := range records {
		if State(record.State) == StateRunning {
			return FormatID(record.ID)
		}
	}
	return FormatID(records[0].ID)
}

// touchLocked records activity and adopts a changed session id.
func (s *Service) touchLocked(r *store.JobRecord, c *change, input HookInput) {
	r.LastActivityAt = s.opts.Now()
	if input.SessionID != "" && r.Engine == EngineClaude && r.AgentName != OrchestratorName && input.SessionID != r.ClaudeSessionID {
		if r.ClaudeSessionID != "" {
			c.event(EventSession, "Claude session changed", map[string]any{"from": r.ClaudeSessionID, "to": input.SessionID})
		}
		r.ClaudeSessionID = input.SessionID
	}
}

// ---- Stop ----------------------------------------------------------------------------------

func (s *Service) hookStop(ctx context.Context, id string, input HookInput) (HookReply, error) {
	var reply HookReply
	_, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		s.touchLocked(r, c, input)
		job := s.toJob(*r, c.questions.Open)
		decision := Decide(&job, c.questions.Open, StopInput{SessionID: input.SessionID, StopHookActive: input.StopHookActive}, s.historyLocked(*r, c), s.opts.Now())
		// The stop is handled here: the herdr-side backstop leaves a stop whose hook ran alone, so
		// it is never nudged twice (internal/orch/backstop.go). Except when the turn was let end
		// only because input is on its way, an answer (R2-C04) or crowsnest's own typing (R3-C05):
		// that proves a send is in flight, not that another turn follows, so the stop stays
		// unhandled and the backstop looks again once the agent has sat idle. An agent that did
		// carry on is working by then, and is left alone.
		if decision.Rule != RuleAnswerIncoming && decision.Rule != RuleInputIncoming {
			r.LastStopAt = s.opts.Now()
		}
		reply = s.applyDecisionLocked(r, c, decision, input)
		return nil
	})
	return reply, err
}

// historyLocked is the nudge history Decide reads, from the row and its question summary.
func (s *Service) historyLocked(r store.JobRecord, c *change) NudgeHistory {
	typing := false
	if s.claimLive(r) {
		switch r.LifecycleOp {
		case opBrief, opDispatch, opRetry, opNudge:
			typing = true
		}
	}
	return NudgeHistory{Streak: r.NudgeStreak, LastNudgeAt: r.LastNudgeAt, LastProgressAt: r.LastProgressAt,
		AnswerIncomingAt: c.questions.AnswerIncomingAt, InputIncoming: typing}
}

// applyDecisionLocked records a Stop decision and returns the hook's output.
func (s *Service) applyDecisionLocked(r *store.JobRecord, c *change, decision Decision, input HookInput) HookReply {
	r.NudgeStreak = decision.Streak
	if decision.Escalate {
		r.Stuck, r.Cause = true, string(CauseUnresponsive)
		r.CauseDetail = fmt.Sprintf("Ended its turn %d times in a row without declaring anything or doing any work.", MaxNudges+1)
		c.event(EventStuck, "Stuck: unresponsive after three nudges; the stop was allowed", map[string]any{"rule": decision.Rule})
		c.notify = fmt.Sprintf("crowsnest: job %s is stuck (stopped three times without saying why): %s", FormatID(r.ID), oneLine(r.Title, 80))
		return HookReply{}
	}
	if decision.Allow {
		c.event(EventHook, "Turn ended ("+string(decision.Rule)+")", map[string]any{"rule": decision.Rule})
		return HookReply{}
	}
	now := s.opts.Now()
	r.Nudges++
	r.LastNudgeAt = now
	c.event(EventNudge, fmt.Sprintf("Nudged (%d of %d): the turn ended with nothing declared", decision.Streak, MaxNudges),
		map[string]any{"rule": decision.Rule, "streak": decision.Streak, "stop_hook_active": input.StopHookActive, "last_message": oneLine(input.LastAssistant, 300)})
	body, _ := json.Marshal(map[string]string{"decision": "block", "reason": decision.Reason})
	return HookReply{Stdout: string(body)}
}

// ---- SessionStart --------------------------------------------------------------------------

// hookSessionStart re-injects the job (6.3) for startup, resume, compact and clear: plain stdout
// becomes context (verified on 2.1.332).
func (s *Service) hookSessionStart(ctx context.Context, id string, input HookInput) (HookReply, error) {
	if _, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		s.touchLocked(r, c, input)
		c.event(EventHook, "Session started ("+input.Source+")", map[string]any{"source": input.Source})
		return nil
	}); err != nil {
		return HookReply{}, err
	}
	switch input.Source {
	case "startup", "resume", "compact", "clear", "":
	default:
		return HookReply{}, nil
	}
	detail, err := s.Get(ctx, id)
	if err != nil || detail.State.Terminal() {
		return HookReply{}, err
	}
	return HookReply{Stdout: ContextBlock(detail)}, nil
}

// ---- Notification, StopFailure, PermissionDenied, SessionEnd --------------------------------

func (s *Service) applyHookLocked(r *store.JobRecord, c *change, event string, input HookInput) {
	switch event {
	case HookNotification:
		s.notificationLocked(r, c, input)
	case HookStopFailure:
		s.stopFailureLocked(r, c, input)
	case HookPermissionDenied:
		r.Denials++
		c.event(EventHook, "Auto mode denied "+input.ToolName, map[string]any{"tool": input.ToolName, "reason": oneLine(input.Reason, 300), "in_a_row": r.Denials})
		if r.Denials >= maxDenials && !r.Stuck {
			r.Stuck, r.Cause = true, string(CausePermissions)
			r.CauseDetail = fmt.Sprintf("Last denial: %s (%s)", input.ToolName, oneLine(input.Reason, 200))
			c.event(EventStuck, "Stuck: tool calls keep being denied", nil)
		}
	case HookSessionEnd:
		c.event(EventHook, "Session ended ("+input.Reason+")", map[string]any{"reason": input.Reason})
		switch input.Reason {
		case "clear", "resume":
			// The conversation continues in a new session; SessionStart follows.
		default:
			if r.AgentName != OrchestratorName {
				r.Stuck, r.Cause, r.CauseDetail = true, string(CauseExited), "The agent process left ("+input.Reason+"); crowsnest restarts it when its pane is back at a shell."
			}
		}
	}
}

func (s *Service) notificationLocked(r *store.JobRecord, c *change, input HookInput) {
	switch input.NotificationType {
	case "permission_prompt", "elicitation_dialog", "elicitation_url_dialog":
		if State(r.State) != StateNeedsYou {
			r.State, r.Cause, r.CauseDetail = string(StateNeedsYou), string(CauseDialog), oneLine(input.Message, 300)
			c.event(EventStuck, "Needs you: "+oneLine(input.Message, 160), map[string]any{"notification": input.NotificationType})
			// A fixed sentence: the hook's message is the agent's to write (any agent can post a
			// hook), so it stays on the card and never reaches the phone (R2-L3).
			c.notify = fmt.Sprintf("crowsnest: job %s is waiting at a dialog in its terminal", FormatID(r.ID))
		}
	default:
		c.event(EventHook, "Notification: "+oneLine(firstNonEmpty(input.Message, input.NotificationType), 160), map[string]any{"type": input.NotificationType})
	}
}

func (s *Service) stopFailureLocked(r *store.JobRecord, c *change, input HookInput) {
	now := s.opts.Now()
	summary := "API error: " + input.Error
	if input.ErrorDetails != "" {
		summary += " (" + oneLine(input.ErrorDetails, 160) + ")"
	}
	switch input.Error {
	case "rate_limit":
		r.Stuck, r.Cause, r.CauseDetail = true, string(CauseLimit), oneLine(firstNonEmpty(input.ErrorDetails, "Usage or rate limit"), 300)
		r.RetryAt = now.Add(retryDelay(limitRetries, limitEvery, r.RetryCount))
	case "authentication_failed", "billing_error", "oauth_org_not_allowed", "account_on_hold", "cloud_credential_error":
		r.State, r.Cause, r.CauseDetail = string(StateNeedsYou), string(CauseAuth), summary
		r.Stuck, r.RetryAt = false, zeroTime
		c.notify = fmt.Sprintf("crowsnest: job %s stopped: %s; Claude needs a login or billing fix", FormatID(r.ID), input.Error)
	case "invalid_request", "model_not_found":
		r.State, r.Cause, r.CauseDetail = string(StateNeedsYou), string(CauseConfig), summary
		r.Stuck, r.RetryAt = false, zeroTime
		c.notify = fmt.Sprintf("crowsnest: job %s stopped: %s", FormatID(r.ID), input.Error)
	default: // server_error, max_output_tokens, unknown: "continue" usually gets it going again
		r.Stuck, r.Cause, r.CauseDetail = true, string(CauseServerError), summary
		if r.RetryCount < len(serverRetries) {
			r.RetryAt = now.Add(serverRetries[r.RetryCount])
		} else {
			r.RetryAt = zeroTime
			c.notify = fmt.Sprintf("crowsnest: job %s keeps hitting API errors; retries stopped", FormatID(r.ID)) // the error text is the hook's (R2-L3)
		}
	}
	detail := map[string]any{"error": input.Error}
	if !r.RetryAt.IsZero() {
		detail["retry_at"] = r.RetryAt.UTC().Format(time.RFC3339)
	}
	c.event(EventStuck, summary, detail)
}

// retryDelay is the delay before attempt n (0-based) of a schedule that repeats every after it.
func retryDelay(schedule []time.Duration, every time.Duration, attempt int) time.Duration {
	if attempt < len(schedule) {
		return schedule[attempt]
	}
	return every
}

// toolSummary renders a tool call in one line: the tool and its most telling argument.
func toolSummary(name string, input json.RawMessage) string {
	if name == "" {
		return ""
	}
	var fields map[string]any
	_ = json.Unmarshal(input, &fields)
	for _, key := range []string{"description", "command", "file_path", "path", "pattern", "url", "query", "progress", "title"} {
		if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
			return name + ": " + oneLine(value, 140)
		}
	}
	return name
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
