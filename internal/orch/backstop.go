// The herdr-side backstop (DESIGN.md 6.3, "Backstop without hooks"): for agents whose Stop hook
// does not run (Codex jobs, sessions started by hand, a hook that failed), an agent holding a
// running job that sits idle or done for BackstopIdle with an empty input box is sent the
// supervisor's nudge text as a prompt. The rule and the nudge budget are the jobs service's
// (jobs.Service.NudgeIfDue, the function M7 left for this), so the hook and the backstop spend
// one budget.
//
// One stop is never nudged twice: the Stop hook stamps the job's last_stop_at, and a stop whose
// hook ran (a stamp no older than the moment the agent was seen to settle, less a little slack
// for the hook running just before herdr notices the turn ended) is left to the hook.
//
// It lives in the poker's loop because it watches the same thing (agents settling) and needs
// the same screen read; internal/jobs cannot be imported here (it imports orch), so the jobs
// service is reached through the Backstop interface.

package orch

import (
	"context"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/journal"
)

const (
	// BackstopIdle is how long an agent holding a job must sit settled before it is nudged, and
	// how long the backstop waits before looking at the same agent again.
	BackstopIdle = 60 * time.Second
	// StopHookSlack covers the Stop hook running a few seconds before the poker sees herdr
	// report the turn over (herdr's own detection plus the poll interval).
	StopHookSlack = 15 * time.Second
)

// BackstopJob is a job the backstop watches: running, not waiting on questions, held by the
// agent in PaneID.
type BackstopJob struct {
	ID     string
	PaneID string
	// LastStopAt is when the agent's Stop hook last ran (zero: never).
	LastStopAt time.Time
}

// Backstop is the jobs service as the backstop uses it.
type Backstop interface {
	// BackstopJobs lists the running, non-waiting jobs whose agent has a pane.
	BackstopJobs(ctx context.Context) ([]BackstopJob, error)
	// NudgeIfDue applies the disposition rule and budget and, when due, types the nudge. job is
	// as BackstopJobs returned it: its LastStopAt is the Stop hook's mark the backstop judged by,
	// and a newer one means a hook handled the stop meanwhile (nothing is typed).
	NudgeIfDue(ctx context.Context, job BackstopJob) (bool, error)
}

// BackstopDue says whether a job's agent should be looked at now (pure): it is idle or done,
// has been for BackstopIdle, its stop was not handled by a hook, and the backstop did not look
// at it within the last BackstopIdle (next is when it may look again).
func BackstopDue(job BackstopJob, agent AgentSeen, now, next time.Time) bool {
	if !agent.Status.Ready() || agent.Since.IsZero() || now.Sub(agent.Since) < BackstopIdle {
		return false
	}
	if now.Before(next) {
		return false
	}
	return !StopHandled(job.LastStopAt, agent.Since)
}

// StopHandled reports whether a Stop hook ran for the stop that left the agent settled at since.
func StopHandled(lastStopAt, since time.Time) bool {
	return !lastStopAt.IsZero() && !lastStopAt.Before(since.Add(-StopHookSlack))
}

// runBackstop nudges every due agent whose input box is empty and shows no dialog.
func (p *Poker) runBackstop(ctx context.Context, now time.Time) {
	if p.opts.Backstop == nil {
		return
	}
	jobs, err := p.opts.Backstop.BackstopJobs(ctx)
	if err != nil {
		p.opts.Logger.Warn("backstop could not list jobs", "err", err)
		return
	}
	for _, job := range jobs {
		p.mu.Lock()
		agent, ok := p.agents[job.PaneID]
		next := p.backstop[job.PaneID]
		p.mu.Unlock()
		if !ok || !BackstopDue(job, agent, now, next) {
			continue
		}
		p.mu.Lock()
		p.backstop[job.PaneID] = now.Add(BackstopIdle)
		p.mu.Unlock()
		p.backstopOne(ctx, job, agent)
	}
}

// backstopOne checks the input box and asks the jobs service to nudge.
func (p *Poker) backstopOne(ctx context.Context, job BackstopJob, agent AgentSeen) {
	screen, err := readScreen(ctx, p.opts.Client, job.PaneID)
	if err != nil || screen.dialog.Present || screen.draft.Present || agent.Status == herdr.StatusBlocked {
		return // a dialog is the Inbox's; a draft is someone at the terminal
	}
	nudged, err := p.opts.Backstop.NudgeIfDue(ctx, job)
	if err != nil {
		p.opts.Logger.Warn("backstop nudge failed", "job", job.ID, "err", err)
		return
	}
	if nudged {
		p.opts.Journal.Add(ctx, journal.KindBackstop, "Nudged "+job.ID+" ("+agent.Name+"): it sat idle with nothing declared and no Stop hook ran",
			map[string]any{"job": job.ID, "pane_id": job.PaneID, "agent": agent.Name})
	}
}
