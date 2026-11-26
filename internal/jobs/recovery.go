// Recovery and the heartbeat (DESIGN.md 6.4 and 6.5): a small scheduler that looks at every
// live job on each tick and acts on what it finds. The clock is Options.Now, so tests drive
// Tick with a fake one. Every action is a job_event.
//
// Per job, in order:
//   - a queued no-directory job is offered to the orchestrator; a queued job whose launch was
//     interrupted (crowsnest restarted mid-launch) is launched again;
//   - a brief held back by a start-up dialog is sent once the agent is ready;
//   - needs_you for a dialog returns to running when the dialog is gone;
//   - an agent whose pane is back at a shell is restarted with --resume (at most 3 an hour,
//     then the job fails and Jeff is told);
//   - a limit or API error whose retry is due gets "continue" when the agent is idle;
//   - transcript activity advances last_activity_at; a running job silent for 20 minutes while
//     herdr says working is stuck: silent; idle for 10 minutes and not working is stuck: idle;
//   - an open question older than its urgency allows is re-notified once.

package jobs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

const (
	// DefaultTickInterval is how often the supervisor looks at live jobs.
	DefaultTickInterval = 10 * time.Second
	// silentAfter: herdr says working but the transcript has not moved (6.4).
	silentAfter = 20 * time.Minute
	// idleAfter: no activity and the agent is not working (6.5).
	idleAfter = 10 * time.Minute
	// maxRestartsPerHour caps automatic restarts of a job's agent (6.4).
	maxRestartsPerHour = 3
	// relaunchAfter is how long a queued job with a directory may sit unlaunched before the tick
	// launches it (its launch was interrupted by a crowsnest restart).
	relaunchAfter = 2 * time.Minute
	// continueText is what a retry sends after a limit or API error.
	continueText = "continue"
)

// renotifyAfter is how long an open question may wait, by urgency, before Jeff is told again.
var renotifyAfter = map[string]time.Duration{"blocking": 30 * time.Minute, "normal": 4 * time.Hour, "low": 24 * time.Hour}

// Run ticks until ctx ends.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultTickInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Tick(ctx)
		}
	}
}

// Tick runs one pass of the supervisor over every live job.
func (s *Service) Tick(ctx context.Context) {
	if s.opts.Herdr == nil {
		return
	}
	s.DispatchOrchestrator(ctx)
	records, err := s.opts.Store.ListJobs(ctx, []string{string(StateQueued), string(StateRunning), string(StateNeedsYou), string(StateBlocked)}, listLimit)
	if err != nil {
		s.opts.Logger.Warn("supervisor: listing jobs", "err", err)
		return
	}
	for _, record := range records {
		if ctx.Err() != nil {
			return
		}
		s.tickJob(ctx, record)
	}
}

func (s *Service) tickJob(ctx context.Context, r store.JobRecord) {
	id := FormatID(r.ID)
	if s.inflight(id) {
		return
	}
	now := s.opts.Now()
	if State(r.State) == StateQueued {
		if r.Dir != "" && r.PaneID == "" && now.Sub(r.CreatedAt) > relaunchAfter {
			s.goBackground(ctx, id, func(ctx context.Context) { s.launch(ctx, id) })
		}
		return
	}
	if r.PaneID == "" {
		return
	}
	s.renotifyQuestions(ctx, r)
	if r.AgentName == OrchestratorName {
		s.checkActivity(ctx, r, now) // the orchestrator's own lifecycle is internal/orch's
		return
	}
	agent, alive := s.agentAlive(ctx, r.PaneID)
	switch {
	case !r.BriefSent:
		if alive && agent.InteractiveReady && agent.AgentStatus != herdr.StatusBlocked && !s.hasDialog(ctx, r.PaneID) {
			job := s.toJob(r, 0)
			s.goBackground(ctx, id, func(ctx context.Context) {
				if err := s.sendBrief(ctx, job); err != nil {
					s.opts.Logger.Warn("sending a held brief", "job", id, "err", err)
				}
			})
		}
	case !alive:
		s.restart(ctx, r)
	case State(r.State) == StateNeedsYou && Cause(r.Cause) == CauseDialog:
		if agent.AgentStatus != herdr.StatusBlocked && !s.hasDialog(ctx, r.PaneID) {
			_, _ = s.mutate(ctx, id, func(rec *store.JobRecord, c *change) error {
				if State(rec.State) == StateNeedsYou && Cause(rec.Cause) == CauseDialog {
					rec.State, rec.Cause, rec.CauseDetail = string(StateRunning), "", ""
					c.event(EventHook, "The dialog is gone; back to running", nil)
				}
				return nil
			})
		}
	case r.Stuck && (Cause(r.Cause) == CauseLimit || Cause(r.Cause) == CauseServerError):
		s.retry(ctx, r, agent, now)
	default:
		s.checkActivity(ctx, r, now)
	}
}

// ---- Restart -------------------------------------------------------------------------------

// restart starts a job's agent again in its pane, within the hourly budget.
func (s *Service) restart(ctx context.Context, r store.JobRecord) {
	id := FormatID(r.ID)
	now := s.opts.Now()
	recent := recentRestarts(r.RestartTimes, now)
	if len(recent) >= maxRestartsPerHour {
		_, _ = s.mutate(ctx, id, func(rec *store.JobRecord, c *change) error {
			rec.State, rec.FailReason = string(StateFailed), fmt.Sprintf("The agent exited %d times within an hour; crowsnest stopped restarting it.", maxRestartsPerHour)
			rec.Stuck, rec.Cause = false, ""
			c.event(EventFail, rec.FailReason, nil)
			c.notify = fmt.Sprintf("crowsnest: job %s failed: its agent kept exiting: %s", id, oneLine(rec.Title, 80))
			return nil
		})
		return
	}
	job, err := s.mutate(ctx, id, func(rec *store.JobRecord, c *change) error {
		rec.Restarts++
		rec.RestartTimes = joinTimes(append(recent, now))
		rec.Stuck, rec.Cause, rec.CauseDetail = true, string(CauseExited), "Restarting the agent"
		c.event(EventRestart, fmt.Sprintf("The agent is gone; restarting it (%d of %d this hour)", len(recent)+1, maxRestartsPerHour), nil)
		return nil
	})
	if err != nil {
		return
	}
	resume := job.Engine == EngineClaude && job.SessionID != "" && s.transcriptExists(job)
	if !resume && job.Engine == EngineClaude {
		job.SessionID = s.newSession(ctx, id)
	}
	s.goBackground(ctx, id, func(ctx context.Context) {
		if err := s.startInPane(ctx, job, resume); err != nil {
			s.opts.Logger.Warn("restarting a job agent", "job", id, "err", err)
			_ = s.Journal(ctx, id, EventRestart, "Restart failed: "+oneLine(err.Error(), 200), nil)
			return
		}
		_, _ = s.mutate(ctx, id, func(rec *store.JobRecord, c *change) error {
			if Cause(rec.Cause) == CauseExited {
				rec.Stuck, rec.Cause, rec.CauseDetail = false, "", ""
			}
			return nil
		})
	})
}

// newSession gives a job a fresh Claude session id (no transcript to resume).
func (s *Service) newSession(ctx context.Context, id string) string {
	session := ""
	_, _ = s.mutate(ctx, id, func(rec *store.JobRecord, _ *change) error {
		rec.ClaudeSessionID = newUUID()
		rec.BriefSent = false
		session = rec.ClaudeSessionID
		return nil
	})
	return session
}

func (s *Service) transcriptExists(job Job) bool {
	if s.opts.Launch.ClaudeConfigDir == "" || job.WorkDir == "" {
		return false
	}
	_, err := transcript.Locate(s.opts.Launch.ClaudeConfigDir, job.WorkDir, job.SessionID)
	return err == nil
}

func recentRestarts(encoded string, now time.Time) []time.Time {
	var out []time.Time
	for _, part := range strings.Split(encoded, ",") {
		seconds, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			continue
		}
		if at := time.Unix(seconds, 0); now.Sub(at) < time.Hour {
			out = append(out, at)
		}
	}
	return out
}

func joinTimes(times []time.Time) string {
	parts := make([]string, len(times))
	for i, at := range times {
		parts[i] = strconv.FormatInt(at.Unix(), 10)
	}
	return strings.Join(parts, ",")
}

// ---- Retries -------------------------------------------------------------------------------

// retry acts on a limit or API error: when due and the agent is idle, it types "continue".
// An agent that is working again has recovered by itself.
func (s *Service) retry(ctx context.Context, r store.JobRecord, agent herdr.Agent, now time.Time) {
	id := FormatID(r.ID)
	if agent.AgentStatus == herdr.StatusWorking {
		_, _ = s.mutate(ctx, id, func(rec *store.JobRecord, c *change) error {
			rec.Stuck, rec.Cause, rec.CauseDetail, rec.RetryAt = false, "", "", zeroTime
			c.event(EventRetry, "Working again; the "+r.Cause+" has cleared", nil)
			return nil
		})
		return
	}
	if r.RetryAt.IsZero() || now.Before(r.RetryAt) || !agent.AgentStatus.Ready() {
		return
	}
	s.RetryNow(ctx, id)
}

// RetryNow types "continue" into a job stuck on a limit or API error and schedules the next
// attempt. It is the tick's action when a retry is due and the card's "Retry now".
func (s *Service) RetryNow(ctx context.Context, id string) {
	detail, err := s.Get(ctx, id)
	if err != nil || detail.PaneID == "" {
		return
	}
	_, sendErr := orch.Prompt(ctx, s.opts.Herdr, detail.PaneID, orch.PromptRequest{Text: continueText})
	_, _ = s.mutate(ctx, id, func(rec *store.JobRecord, c *change) error {
		now := s.opts.Now()
		rec.RetryCount++
		if sendErr != nil {
			c.event(EventRetry, "Retry could not type: "+oneLine(sendErr.Error(), 160), nil)
		} else {
			c.event(EventRetry, fmt.Sprintf("Retry %d: typed \"continue\"", rec.RetryCount), nil)
		}
		switch Cause(rec.Cause) {
		case CauseLimit:
			rec.RetryAt = now.Add(retryDelay(limitRetries, limitEvery, rec.RetryCount))
		case CauseServerError:
			if rec.RetryCount < len(serverRetries) {
				rec.RetryAt = now.Add(serverRetries[rec.RetryCount])
			} else {
				rec.RetryAt = zeroTime
				c.notify = fmt.Sprintf("crowsnest: job %s still hits API errors after %d retries", FormatID(rec.ID), rec.RetryCount)
			}
		default:
			rec.RetryAt = zeroTime
		}
		return nil
	})
}

// ---- Heartbeat -----------------------------------------------------------------------------

// checkActivity advances last_activity_at from the transcript and flags silent or idle jobs.
func (s *Service) checkActivity(ctx context.Context, r store.JobRecord, now time.Time) {
	if State(r.State) != StateRunning || !r.BriefSent {
		return
	}
	id := FormatID(r.ID)
	lastAppend, lastTool := time.Time{}, ""
	if s.opts.Activity != nil {
		lastAppend, lastTool = s.opts.Activity(ctx, s.toJob(r, 0))
	}
	agent, _ := s.agentGet(ctx, r.PaneID)
	working := agent.AgentStatus == herdr.StatusWorking
	_, _ = s.mutate(ctx, id, func(rec *store.JobRecord, c *change) error {
		if State(rec.State) != StateRunning {
			return nil
		}
		if lastAppend.After(rec.LastActivityAt) {
			rec.LastActivityAt = lastAppend
			if lastTool != "" {
				rec.LastTool = oneLine(lastTool, maxProgressRunes)
			}
			if rec.Stuck && (Cause(rec.Cause) == CauseSilent || Cause(rec.Cause) == CauseIdle) {
				rec.Stuck, rec.Cause, rec.CauseDetail = false, "", ""
				c.event(EventHook, "Activity again; no longer stuck", nil)
			}
		}
		quiet := now.Sub(rec.LastActivityAt)
		switch {
		case rec.Stuck:
		case working && quiet >= silentAfter:
			rec.Stuck, rec.Cause = true, string(CauseSilent)
			rec.CauseDetail = fmt.Sprintf("herdr says working, but nothing has happened for %d minutes", int(quiet.Minutes()))
			c.event(EventStuck, "Stuck: silent while working", nil)
			c.notify = fmt.Sprintf("crowsnest: job %s has been silent for %d minutes", id, int(quiet.Minutes()))
		case !working && quiet >= idleAfter:
			rec.Stuck, rec.Cause = true, string(CauseIdle)
			rec.CauseDetail = fmt.Sprintf("Not working and nothing declared for %d minutes", int(quiet.Minutes()))
			c.event(EventStuck, "Stuck: idle with nothing declared", nil)
		}
		return nil
	})
}

// ---- Questions -----------------------------------------------------------------------------

// renotifyQuestions tells Jeff once more about a job's question that has waited too long.
// Expiry is never consent: the job stays where it is and says so.
func (s *Service) renotifyQuestions(ctx context.Context, r store.JobRecord) {
	questionList, err := s.questionsOf(ctx, r.ID)
	if err != nil {
		return
	}
	now := s.opts.Now()
	for _, q := range questionList {
		limit, ok := renotifyAfter[q.Urgency]
		if q.Status != "open" || !ok || now.Sub(q.CreatedAt) < limit {
			continue
		}
		marker := `"question":"` + q.ID + `"`
		if done, err := s.opts.Store.HasJobEvent(ctx, r.ID, EventNotify, marker); err != nil || done {
			continue
		}
		_, _ = s.mutate(ctx, FormatID(r.ID), func(_ *store.JobRecord, c *change) error {
			c.event(EventNotify, fmt.Sprintf("Reminded you about %s (open for %s)", q.ID, now.Sub(q.CreatedAt).Round(time.Minute)), map[string]any{"question": q.ID})
			c.notify = fmt.Sprintf("crowsnest: %s is still waiting for you: %s", q.ID, oneLine(q.Title, 100))
			return nil
		})
	}
}

// ---- Helpers -------------------------------------------------------------------------------

// hasDialog reads the pane's screen for a dialog (herdr sometimes reports one as idle).
func (s *Service) hasDialog(ctx context.Context, paneID string) bool {
	ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	read, err := s.opts.Herdr.PaneRead(ctx, paneID, herdr.ReadOptions{Source: herdr.SourceVisible})
	if err != nil {
		return false
	}
	return fleet.DetectDialog(fleet.StripANSI(read.Text)).Present
}

// inflight reports whether background work (a launch, a restart) is running for a job.
func (s *Service) inflight(id string) bool {
	s.busyMu.Lock()
	defer s.busyMu.Unlock()
	return s.busy[id]
}

// goBackground runs fn for job id unless something already runs for it.
func (s *Service) goBackground(ctx context.Context, id string, fn func(context.Context)) {
	s.busyMu.Lock()
	if s.busy == nil {
		s.busy = map[string]bool{}
	}
	if s.busy[id] {
		s.busyMu.Unlock()
		return
	}
	s.busy[id] = true
	s.busyMu.Unlock()
	run := func() {
		defer func() {
			s.busyMu.Lock()
			delete(s.busy, id)
			s.busyMu.Unlock()
		}()
		fn(context.WithoutCancel(ctx))
	}
	if s.opts.Synchronous {
		run()
		return
	}
	go run()
}
