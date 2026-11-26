// Upkeep (DESIGN.md 7.5): the watch that follows the orchestrator's state and restarts it with
// --resume when its pane is back at a shell, and the two manual operations, Restart (same
// conversation) and New conversation (fresh session id).

package orch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/journal"
)

// Watch checks the orchestrator once. Run calls it on a timer; tests call it directly. A tick
// that finds a lifecycle operation in progress does nothing.
func (o *Orchestrator) Watch(ctx context.Context) {
	if o.opts.Disabled || !o.lifecycle.TryLock() {
		return
	}
	defer o.lifecycle.Unlock()
	agent, alive, err := o.liveAgent(ctx)
	if err != nil {
		o.opts.Logger.Debug("orchestrator watch: herdr not answering", "err", err)
		return
	}
	if alive {
		previous := o.Status().SessionID
		status := o.follow(agent)
		if status.SessionID != previous {
			o.remember(ctx, "", status.SessionID) // /clear or a resume moved the conversation
		}
		return
	}
	if o.Status().State == StateFailed {
		return // waiting for a manual restart
	}
	o.recover(ctx)
}

// recover restarts an orchestrator that is gone (exited, crashed, pane closed), within the
// hourly budget.
func (o *Orchestrator) recover(ctx context.Context) {
	o.mu.Lock()
	tooMany := o.restartsInLastHourLocked() >= maxRestartsPerHour
	if !tooMany {
		o.restarts = append(o.restarts, o.opts.Now())
	}
	o.mu.Unlock()
	if tooMany {
		status := o.update(func(s *Status) {
			s.State = StateFailed
			s.Detail = fmt.Sprintf("Claude exited %d times within an hour, so crowsnest stopped restarting it. Check its terminal, then use Restart.", maxRestartsPerHour)
		})
		o.opts.Journal.Add(ctx, journal.KindAlert, status.Detail, nil)
		return
	}
	o.opts.Logger.Info("orchestrator is gone; restarting it with --resume")
	o.update(func(s *Status) { s.State, s.Detail = StateRestarting, "" })
	status, err := o.ensureLocked(ctx, true)
	if err != nil {
		o.opts.Logger.Warn("restarting the orchestrator", "err", err)
	}
	o.journalRestart(ctx, "Restarted the orchestrator automatically (Claude had exited)", true, status, err)
}

// Restart stops the orchestrator's Claude and starts it again on the same conversation
// (--resume). It also clears the automatic-restart budget, so it is how a failed orchestrator is
// brought back.
func (o *Orchestrator) Restart(ctx context.Context) (Status, error) {
	return o.relaunch(ctx, true)
}

// NewConversation stops the orchestrator's Claude and starts it with a fresh session id: the
// same agent and pane, an empty conversation.
func (o *Orchestrator) NewConversation(ctx context.Context) (Status, error) {
	return o.relaunch(ctx, false)
}

// ErrDisabled is returned by the manual operations under --no-orchestrator.
var ErrDisabled = errors.New("the orchestrator is disabled (--no-orchestrator)")

func (o *Orchestrator) relaunch(ctx context.Context, resume bool) (Status, error) {
	if o.opts.Disabled {
		return o.Status(), ErrDisabled
	}
	summary := "Restarted the orchestrator (Restart, same conversation)"
	if !resume {
		summary = "Started a new orchestrator conversation (New conversation)"
	}
	status, err := o.relaunchLocked(ctx, resume)
	o.journalRestart(ctx, summary, false, status, err)
	return status, err
}

// journalRestart records a restart and how it ended.
func (o *Orchestrator) journalRestart(ctx context.Context, summary string, automatic bool, status Status, err error) {
	detail := map[string]any{"automatic": automatic, "state": status.State, "session_id": status.SessionID}
	if err != nil {
		summary += "; it did not come back: " + status.Detail
	}
	o.opts.Journal.Add(ctx, journal.KindRestart, summary, detail)
}

func (o *Orchestrator) relaunchLocked(ctx context.Context, resume bool) (Status, error) {
	o.lifecycle.Lock()
	defer o.lifecycle.Unlock()
	o.mu.Lock()
	o.restarts = nil
	o.mu.Unlock()
	o.update(func(s *Status) { s.State, s.Detail = StateRestarting, "" })
	if agent, alive, err := o.liveAgent(ctx); err == nil && alive {
		if err := o.stop(ctx, agent); err != nil {
			return o.fail(err)
		}
	}
	if !resume {
		if err := o.opts.KV.DeleteKV(ctx, kvSessionID); err != nil {
			o.opts.Logger.Warn("forgetting the orchestrator session id", "err", err)
		}
		o.update(func(s *Status) { s.SessionID = "" })
	}
	return o.ensureLocked(ctx, resume)
}

// stop ends a running Claude and waits for its pane to show a shell: Esc to dismiss a dialog,
// then /exit, then Ctrl+C twice if it is still there (an /exit typed into a busy or wedged
// session may never be read).
func (o *Orchestrator) stop(ctx context.Context, agent herdr.Agent) error {
	callCtx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	if agent.AgentStatus == herdr.StatusBlocked {
		_ = o.opts.Client.AgentSendKeys(callCtx, agent.PaneID, []string{"esc"})
	}
	if _, err := o.opts.Client.AgentPrompt(callCtx, agent.PaneID, "/exit", nil); err != nil && !herdr.IsNotFound(err) {
		o.opts.Logger.Warn("typing /exit into the orchestrator", "err", err)
	}
	if o.waitForShell(ctx, agent.PaneID) == nil {
		return nil
	}
	interruptCtx, cancelInterrupt := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancelInterrupt()
	for range 2 {
		_ = o.opts.Client.PaneSendKeys(interruptCtx, agent.PaneID, []string{"ctrl+c"})
		time.Sleep(300 * time.Millisecond)
	}
	if err := o.waitForShell(ctx, agent.PaneID); err != nil {
		return fmt.Errorf("stopping the orchestrator: Claude did not exit; open its terminal: %w", err)
	}
	return nil
}
