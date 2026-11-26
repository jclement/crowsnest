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

// Cancel stops the job: cancelled first, which takes the claim from any step acting on it (a
// restart, a retry, a brief: none of them types or starts anything after that, R2-C08), then Esc
// to interrupt the agent's turn. The pane and agent are left alone (the human may want to look),
// never killed.
func (s *Service) Cancel(ctx context.Context, id string) (Job, error) {
	job, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		if State(r.State).Terminal() {
			return invalid("job %s is already %s", id, r.State)
		}
		r.State, r.Stuck, r.Cause, r.CauseDetail, r.RetryAt = string(StateCancelled), false, "", "", zeroTime
		c.event(EventState, "Cancelled by you (Esc sent to the agent)", nil)
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	if job.PaneID != "" && s.opts.Herdr != nil {
		callCtx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
		_ = s.opts.Herdr.AgentSendKeys(callCtx, job.PaneID, []string{"esc"})
		cancel()
	}
	return job, nil
}

// Nudge sends the nudge text by hand. It does not count against the automatic budget, and it
// clears an unresponsive flag (the human is giving it another go).
func (s *Service) Nudge(ctx context.Context, id string) (Job, error) {
	if s.opts.Herdr == nil {
		return Job{}, ErrNoAgent
	}
	cl, err := s.claimStep(ctx, id, opNudge, func(r *store.JobRecord, _ *change) error {
		if r.PaneID == "" {
			return ErrNoAgent
		}
		return nil
	})
	switch {
	case errors.Is(err, errClaimLost):
		detail, getErr := s.Get(ctx, id)
		if getErr != nil {
			return Job{}, getErr
		}
		return Job{}, invalid("job %s is already %s", id, detail.State)
	case errors.Is(err, errBusy):
		return Job{}, invalid("crowsnest is typing into job %s's agent right now; try again in a moment", id)
	case err != nil:
		return Job{}, err
	}
	detail, err := s.Get(ctx, id)
	if err == nil {
		err = s.holds(ctx, cl)
	}
	if err != nil {
		s.finishQuietly(ctx, cl, nil)
		return Job{}, err
	}
	if _, err := orch.Prompt(ctx, s.opts.Herdr, detail.PaneID, orch.PromptRequest{Text: "[crowsnest nudge] " + NudgeText(detail.ID)}); err != nil && typedOutcome(orch.PromptResult{}, err) == typedNo {
		s.finishQuietly(ctx, cl, nil)
		return Job{}, err
	}
	return s.finish(ctx, cl, func(r *store.JobRecord, c *change) {
		r.Nudges++
		r.NudgeStreak = 0
		if r.Stuck && (Cause(r.Cause) == CauseUnresponsive || Cause(r.Cause) == CauseIdle) {
			r.Stuck, r.Cause, r.CauseDetail = false, "", ""
		}
		c.event(EventNudge, "Nudged by you", map[string]any{"manual": true})
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
	case detail.AgentName != OrchestratorName && s.gone(ctx, detail.PaneID):
		record, err := s.opts.Store.GetJob(ctx, mustParse(id))
		if err != nil {
			return Job{}, err
		}
		s.restart(ctx, record, true) // a manual resume clears the hourly budget
	default:
		return s.Nudge(ctx, id)
	}
	refreshed, err := s.Get(ctx, id)
	return refreshed.Job, err
}

// gone reports whether the agent in paneID is confirmed gone.
func (s *Service) gone(ctx context.Context, paneID string) bool {
	_, seen := s.observeAgent(ctx, paneID)
	return seen == livenessGone
}

func mustParse(id string) int64 {
	numeric, _ := ParseID(id)
	return numeric
}

// NudgeIfDue is for M8's herdr-side backstop (agents without hooks: Codex, a failed hook, a
// session crowsnest did not start). The caller has seen the agent idle with an empty input box
// long enough, with job.LastStopAt as the Stop hook's mark at the time it looked. Under the lock
// this checks that no Stop hook has run since (one that did handled the stop, R2-C10), applies
// the same disposition rule and budget as the Stop hook, and claims the nudge. The nudge counts
// against the budget only once it is typed (or its typing is uncertain); a send that provably
// typed nothing (a dialog or a draft appeared) spends nothing. It reports whether it nudged.
func (s *Service) NudgeIfDue(ctx context.Context, job orch.BackstopJob) (bool, error) {
	if s.opts.Herdr == nil {
		return false, nil
	}
	var decision Decision
	var cl claim
	_, err := s.mutate(ctx, job.ID, func(r *store.JobRecord, c *change) error {
		if r.PaneID == "" || !r.LastStopAt.Equal(job.LastStopAt) {
			return errSkip
		}
		view := s.toJob(*r, c.questions.Open)
		decision = Decide(&view, c.questions.Open, StopInput{}, s.historyLocked(*r, c), s.opts.Now())
		switch {
		case decision.Escalate:
			s.applyDecisionLocked(r, c, decision, HookInput{})
			return nil
		case decision.Allow:
			return errSkip
		}
		return s.claimLocked(r, c, opNudge, nil, &cl)
	})
	switch {
	case errors.Is(err, errSkip), errors.Is(err, errBusy), errors.Is(err, errClaimLost):
		return false, nil
	case err != nil:
		return false, err
	case decision.Allow:
		return false, nil
	}
	detail, err := s.Get(ctx, job.ID)
	if err == nil {
		err = s.holds(ctx, cl)
	}
	if err != nil {
		s.finishQuietly(ctx, cl, nil)
		if errors.Is(err, errClaimLost) {
			return false, nil
		}
		return false, err
	}
	result, sendErr := orch.Prompt(ctx, s.opts.Herdr, detail.PaneID, orch.PromptRequest{Text: "[crowsnest nudge] " + decision.Reason})
	outcome := typedOutcome(result, sendErr)
	if outcome == typedNo {
		s.finishQuietly(ctx, cl, nil) // nothing typed: no budget spent
		return false, nil
	}
	if _, err := s.finish(ctx, cl, func(r *store.JobRecord, c *change) {
		if r.LastProgressAt.After(cl.at) {
			decision.Streak = 1 // progress came while the nudge was typed: a fresh streak
		}
		s.applyDecisionLocked(r, c, decision, HookInput{})
		if outcome == typedMaybe {
			c.event(EventNudge, "crowsnest lost track of that nudge's send; counted as given", nil)
		}
	}); err != nil && !errors.Is(err, errClaimLost) {
		return true, err
	}
	return true, nil
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
