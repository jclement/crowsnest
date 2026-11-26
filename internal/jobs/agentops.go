// herdr operations the launcher and the recovery tick share: is a pane at a bare shell, wait for
// one, start an agent riding out herdr's transient refusals, and wait for it to become
// interactive-ready. They follow what M4 learnt starting the orchestrator against real herdr
// (DESIGN.md 7.6): agent.start answers at once with launch_pending, so readiness is polled; a
// brand-new pane can refuse with agent_pane_busy for a moment; a dead agent's record keeps its
// name for a while (agent_name_taken). internal/orch has the orchestrator's own copy of this
// logic; it is repeated here rather than shared so the orchestrator's lifecycle code (under
// review fixes at the time) stayed untouched.

package jobs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
)

const (
	// herdrCallTimeout bounds each ordinary herdr call.
	herdrCallTimeout = 5 * time.Second
	// startTimeoutMS is agent.start's own timeout (herdr accepts 3001..300000).
	startTimeoutMS = 60_000
	// startRetryWait bounds how long a start keeps retrying transient refusals.
	startRetryWait = 15 * time.Second
	// launchPollInterval is how often a launching agent is checked.
	launchPollInterval = 250 * time.Millisecond
)

// shellNames are the processes that count as "a bare shell" in a pane.
var shellNames = map[string]bool{"zsh": true, "bash": true, "sh": true, "fish": true, "dash": true, "ksh": true, "tcsh": true, "csh": true, "nu": true}

// atShell reports whether a pane's foreground holds only a shell.
func (s *Service) atShell(ctx context.Context, paneID string) bool {
	ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	info, err := s.opts.Herdr.PaneProcessInfo(ctx, paneID)
	if err != nil {
		return false
	}
	for _, process := range info.ForegroundProcesses {
		if !shellNames[strings.TrimPrefix(filepath.Base(process.Name), "-")] {
			return false
		}
	}
	return true
}

// waitForShell polls until the pane is at a bare shell, for up to ShellWait.
func (s *Service) waitForShell(ctx context.Context, paneID string) error {
	deadline := s.opts.Now().Add(s.opts.Launch.shellWait())
	for {
		if s.atShell(ctx, paneID) {
			return nil
		}
		if s.opts.Now().After(deadline) {
			return fmt.Errorf("pane %s did not show a bare shell within %s", paneID, s.opts.Launch.shellWait())
		}
		if err := sleep(ctx, launchPollInterval); err != nil {
			return err
		}
	}
}

// startAgent calls agent.start, retrying agent_pane_busy and clearing a stale name on
// agent_name_taken, then waits until the agent is interactive-ready. herdr.ErrAgentNotReady
// means it stopped at a start-up dialog (folder trust, login).
func (s *Service) startAgent(ctx context.Context, params herdr.AgentStartParams) (herdr.Agent, error) {
	agent, err := s.agentStart(ctx, params)
	if err != nil {
		return agent, err
	}
	return s.waitForLaunch(ctx, params.PaneID, agent)
}

func (s *Service) agentStart(ctx context.Context, params herdr.AgentStartParams) (herdr.Agent, error) {
	deadline := s.opts.Now().Add(startRetryWait)
	for {
		startCtx, cancel := context.WithTimeout(ctx, startTimeoutMS*time.Millisecond+10*time.Second)
		agent, err := s.opts.Herdr.AgentStart(startCtx, params)
		cancel()
		var herdrErr *herdr.Error
		if !errors.As(err, &herdrErr) || s.opts.Now().After(deadline) {
			return agent, err
		}
		switch herdrErr.Code {
		case "agent_name_taken":
			s.releaseStaleName(ctx, params.Name)
		case "agent_pane_busy":
		default:
			return agent, err
		}
		if err := sleep(ctx, 500*time.Millisecond); err != nil {
			return agent, err
		}
	}
}

// releaseStaleName clears name from a record whose agent has exited (its pane is at a shell).
func (s *Service) releaseStaleName(ctx context.Context, name string) {
	getCtx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	stale, err := s.opts.Herdr.AgentGet(getCtx, name)
	cancel()
	if err != nil || stale.LaunchPending || !s.atShell(ctx, stale.PaneID) {
		return
	}
	renameCtx, cancelRename := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancelRename()
	_, _ = s.opts.Herdr.AgentRename(renameCtx, stale.PaneID, "")
}

// waitForLaunch polls agent.get on the pane until the agent is interactive-ready.
func (s *Service) waitForLaunch(ctx context.Context, paneID string, agent herdr.Agent) (herdr.Agent, error) {
	deadline := s.opts.Now().Add(s.opts.Launch.launchTimeout())
	for {
		if !agent.LaunchPending && agent.InteractiveReady {
			return agent, nil
		}
		if !agent.InteractiveReady && agent.AgentStatus == herdr.StatusBlocked {
			return agent, fmt.Errorf("the agent is at a start-up dialog: %w", herdr.ErrAgentNotReady)
		}
		if s.opts.Now().After(deadline) {
			return agent, fmt.Errorf("the agent did not become ready within %s: %w", s.opts.Launch.launchTimeout(), herdr.ErrAgentNotReady)
		}
		if err := sleep(ctx, launchPollInterval); err != nil {
			return agent, err
		}
		next, err := s.agentGet(ctx, paneID)
		if err != nil && !herdr.IsNotFound(err) {
			return agent, fmt.Errorf("waiting for the agent to start: %w", err)
		}
		if err == nil {
			agent = next
		}
	}
}

func (s *Service) agentGet(ctx context.Context, target string) (herdr.Agent, error) {
	ctx, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	return s.opts.Herdr.AgentGet(ctx, target)
}

// agentAlive returns the agent in paneID and whether it is really running: herdr can keep a
// record after the agent exits, so a pane back at a bare shell counts as gone.
func (s *Service) agentAlive(ctx context.Context, paneID string) (herdr.Agent, bool) {
	agent, err := s.agentGet(ctx, paneID)
	if err != nil {
		return herdr.Agent{}, false
	}
	if !agent.LaunchPending && s.atShell(ctx, paneID) {
		return agent, false
	}
	return agent, true
}

func sleep(ctx context.Context, wait time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}
