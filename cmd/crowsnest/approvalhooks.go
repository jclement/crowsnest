// The two hooks of DESIGN.md 8.7: `crowsnest hook permission-request` (structured approvals) and
// `crowsnest hook pre-tool-use` (the guard).
//
// permission-request posts the call and long-polls until the human decides, then prints the
// decision JSON. It fails open to the existing behaviour, never to an allow: if crowsnest cannot
// be reached, refuses the request, or the approval expires or is superseded, it prints nothing
// and exits 0, and Claude's own terminal dialog (drawn at the same moment the hook started) is
// what the human answers. It never hangs forever: it gives up a minute after the server's expiry,
// and after two minutes of failed polls.
//
// pre-tool-use decides from the rules file alone (no server round trip), so it adds a few
// milliseconds to each Bash call (one `git rev-parse` when a git command meets a protected
// branch list). Only a refusal is reported to crowsnest, after the decision is printed, with a
// one-second cap. A missing or broken rules file allows everything (fail open, like every hook).

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/guard"
)

// approvalTimings are the permission-request hook's timings. Tests pass their own through
// hookFlags rather than changing package state, so a hook left running by one test cannot race
// the next.
type approvalTimings struct {
	create     time.Duration // the create call's timeout
	poll       time.Duration // one long-poll's wait
	giveUp     time.Duration // the whole wait, a minute past the server's expiry
	retryFor   time.Duration // how long polls may fail in a row before giving up
	retryEvery time.Duration // the pause between failed polls
}

var defaultApprovalTimings = approvalTimings{
	create: 10 * time.Second, poll: 50 * time.Second, giveUp: approvals.DefaultTTL + time.Minute,
	retryFor: 2 * time.Minute, retryEvery: 2 * time.Second,
}

// guardNotifyTimeout caps the guard's report of a refusal.
const guardNotifyTimeout = time.Second

func runPermissionRequest(ctx context.Context, stdout io.Writer, input []byte, client *agentapi.Client, timings approvalTimings) {
	ctx, cancel := context.WithTimeout(ctx, timings.giveUp)
	defer cancel()
	createCtx, cancelCreate := context.WithTimeout(ctx, timings.create)
	created, err := client.RequestApproval(createCtx, input)
	cancelCreate()
	if err != nil || created.ID == "" {
		return // crowsnest is down or refused: Claude's own dialog
	}
	var failingSince time.Time
	for ctx.Err() == nil {
		pollCtx, cancelPoll := context.WithTimeout(ctx, timings.poll+15*time.Second)
		reply, err := client.WaitApproval(pollCtx, created.ID, timings.poll)
		cancelPoll()
		if err != nil {
			var apiErr *agentapi.APIError
			if errors.As(err, &apiErr) {
				return // the server answered, but not with this approval (gone, not ours)
			}
			if failingSince.IsZero() {
				failingSince = time.Now()
			}
			if time.Since(failingSince) > timings.retryFor {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(timings.retryEvery):
			}
			continue
		}
		failingSince = time.Time{}
		if reply.Status == string(approvals.StatusPending) {
			continue
		}
		if reply.HookOutput != "" {
			_, _ = io.WriteString(stdout, reply.HookOutput)
		}
		return
	}
}

// guardInput is the part of a PreToolUse hook's input the guard reads.
type guardInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

func runGuardHook(ctx context.Context, stdout io.Writer, input []byte, rulesFile string, client *agentapi.Client) {
	if rulesFile == "" {
		return
	}
	var call guardInput
	if json.Unmarshal(input, &call) != nil || call.ToolName != "Bash" || strings.TrimSpace(call.ToolInput.Command) == "" {
		return
	}
	rules, err := guard.Load(rulesFile)
	if err != nil {
		return
	}
	verdict := guard.Check(rules, call.ToolInput.Command, func(dir string) string { return currentBranch(ctx, call.Cwd, dir) })
	if !verdict.Deny {
		return
	}
	body, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": verdict.Reason,
	}})
	_, _ = stdout.Write(body)
	if client == nil {
		return
	}
	notifyCtx, cancel := context.WithTimeout(ctx, guardNotifyTimeout)
	defer cancel()
	_ = client.GuardDenied(notifyCtx, agentapi.GuardDenial{Rule: verdict.Rule, Reason: verdict.Reason, Command: call.ToolInput.Command, SessionID: call.SessionID, Cwd: call.Cwd})
}

// currentBranch is the branch checked out in cwd (joined with a `git -C` directory), or "".
func currentBranch(ctx context.Context, cwd, dir string) string {
	where := cwd
	if dir != "" {
		if filepath.IsAbs(dir) {
			where = dir
		} else {
			where = filepath.Join(cwd, dir)
		}
	}
	if where == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "-C", where, "rev-parse", "--abbrev-ref", "HEAD").Output() //nolint:gosec // fixed arguments; the directory is the agent's own
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
