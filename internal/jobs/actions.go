// What the browser does to a job (DESIGN.md section 10): message its agent, cancel it, nudge it,
// resume it after it stuck; plus the questions hook (a question asked or settled under a job)
// and NudgeIfDue, the entry point M8's herdr-side backstop calls for agents without hooks.

package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
)

// ErrNoAgent means the job has no agent to talk to (not launched yet, or finished).
var ErrNoAgent = errors.New("the job has no agent to talk to yet")

// MessageInput is POST /jobs/{id}/message.
type MessageInput struct {
	Text           string `json:"text"`
	ForceOverDraft bool   `json:"force_over_draft"`
}

// Message types the human's text into the job's agent under a header, and journals it. A human
// message counts as new input: it resets the nudge streak. Errors are orch's (BlockedError,
// DraftError) so the API can answer them like a Chat send.
func (s *Service) Message(ctx context.Context, id string, input MessageInput) (orch.PromptResult, error) {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return orch.PromptResult{}, invalid("text is required")
	}
	detail, err := s.Get(ctx, id)
	if err != nil {
		return orch.PromptResult{}, err
	}
	if detail.PaneID == "" || s.opts.Herdr == nil {
		return orch.PromptResult{}, ErrNoAgent
	}
	result, err := orch.Prompt(ctx, s.opts.Herdr, detail.PaneID, orch.PromptRequest{
		Text: fmt.Sprintf("[crowsnest message %s] %s", detail.ID, text), ForceOverDraft: input.ForceOverDraft,
	})
	if err != nil {
		return result, err
	}
	_, _ = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		r.NudgeStreak = 0
		if r.Stuck && Cause(r.Cause) == CauseUnresponsive {
			r.Stuck, r.Cause, r.CauseDetail = false, "", ""
		}
		c.event(EventMessage, "You: "+oneLine(text, 200), nil)
		return nil
	})
	return result, nil
}

// Cancel stops the job: Esc to interrupt the agent's turn, then cancelled. The pane and agent
// are left alone (the human may want to look), never killed.
func (s *Service) Cancel(ctx context.Context, id string) (Job, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if detail.State.Terminal() {
		return Job{}, invalid("job %s is already %s", id, detail.State)
	}
	if detail.PaneID != "" && s.opts.Herdr != nil {
		callCtx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
		_ = s.opts.Herdr.AgentSendKeys(callCtx, detail.PaneID, []string{"esc"})
		cancel()
	}
	return s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() {
			return invalid("job %s is already %s", id, r.State)
		}
		r.State, r.Stuck, r.Cause, r.CauseDetail, r.RetryAt = string(StateCancelled), false, "", "", zeroTime
		c.event(EventState, "Cancelled by you (Esc sent to the agent)", nil)
		return nil
	})
}

// Nudge sends the nudge text by hand. It does not count against the automatic budget, and it
// clears an unresponsive flag (the human is giving it another go).
func (s *Service) Nudge(ctx context.Context, id string) (Job, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if detail.State.Terminal() {
		return Job{}, invalid("job %s is already %s", id, detail.State)
	}
	if detail.PaneID == "" || s.opts.Herdr == nil {
		return Job{}, ErrNoAgent
	}
	if _, err := orch.Prompt(ctx, s.opts.Herdr, detail.PaneID, orch.PromptRequest{Text: "[crowsnest nudge] " + NudgeText(detail.ID)}); err != nil {
		return Job{}, err
	}
	return s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		r.Nudges++
		r.NudgeStreak = 0
		if r.Stuck && (Cause(r.Cause) == CauseUnresponsive || Cause(r.Cause) == CauseIdle) {
			r.Stuck, r.Cause, r.CauseDetail = false, "", ""
		}
		c.event(EventNudge, "Nudged by you", map[string]any{"manual": true})
		return nil
	})
}

// Resume gets a stuck job moving: a due-or-not retry for a limit or API error, a restart for an
// agent that is gone, otherwise a nudge.
func (s *Service) Resume(ctx context.Context, id string) (Job, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if detail.State.Terminal() {
		return Job{}, invalid("job %s is already %s; give it again as a new job", id, detail.State)
	}
	if detail.PaneID == "" || s.opts.Herdr == nil {
		return Job{}, ErrNoAgent
	}
	switch {
	case detail.Stuck && (detail.Cause == CauseLimit || detail.Cause == CauseServerError):
		s.RetryNow(ctx, id)
	case detail.AgentName != OrchestratorName && !s.alive(ctx, detail.PaneID):
		record, err := s.opts.Store.GetJob(ctx, mustParse(id))
		if err != nil {
			return Job{}, err
		}
		record.RestartTimes = "" // a manual resume clears the hourly budget
		s.restart(ctx, record)
	default:
		return s.Nudge(ctx, id)
	}
	refreshed, err := s.Get(ctx, id)
	return refreshed.Job, err
}

func (s *Service) alive(ctx context.Context, paneID string) bool {
	_, alive := s.agentAlive(ctx, paneID)
	return alive
}

func mustParse(id string) int64 {
	numeric, _ := ParseID(id)
	return numeric
}

// NudgeIfDue is for M8's herdr-side backstop (agents without hooks: Codex, a failed hook, a
// session crowsnest did not start). The caller decides the agent has sat idle with an empty
// input box long enough; this applies the same disposition rule and budget as the Stop hook and,
// when the job should not be resting, types the nudge. It reports whether it nudged.
func (s *Service) NudgeIfDue(ctx context.Context, id string) (bool, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return false, err
	}
	if detail.PaneID == "" || s.opts.Herdr == nil {
		return false, nil
	}
	var decision Decision
	_, err = s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		history := NudgeHistory{Streak: r.NudgeStreak, LastNudgeAt: r.LastNudgeAt, LastProgressAt: r.LastProgressAt}
		job := s.toJob(*r, detail.OpenQuestions)
		decision = Decide(&job, detail.OpenQuestions, StopInput{}, history, s.opts.Now())
		if decision.Allow && !decision.Escalate {
			return nil
		}
		s.applyDecisionLocked(r, c, decision, HookInput{})
		return nil
	})
	if err != nil || decision.Allow {
		return false, err
	}
	_, err = orch.Prompt(ctx, s.opts.Herdr, detail.PaneID, orch.PromptRequest{Text: "[crowsnest nudge] " + decision.Reason})
	return err == nil, err
}

// ---- Questions -----------------------------------------------------------------------------

// QuestionChanged is called by the questions service (OnAsked / OnSettled) for a question asked
// under a job: it is journalled, and needs_you is re-derived (an answer returns a needs_you job
// to running).
func (s *Service) QuestionChanged(ctx context.Context, jobID, questionID, title, what string, wait bool) {
	if _, err := ParseID(jobID); err != nil {
		return
	}
	summary := ""
	switch what {
	case "asked":
		summary = "Asked " + questionID + ": " + oneLine(title, 160)
		if wait {
			summary += " (blocking)"
		}
	default:
		summary = questionID + " " + what
	}
	_, err := s.mutate(ctx, jobID, func(r *store.JobRecord, c *change) error {
		if what == "asked" && !State(r.State).Terminal() {
			s.markProgressLocked(r)
		}
		c.event(EventQuestion, summary, map[string]any{"question": questionID, "change": what})
		return nil
	})
	if err != nil && !errors.Is(err, ErrNotFound) {
		s.opts.Logger.Warn("recording a job's question", "job", jobID, "err", err)
	}
}
