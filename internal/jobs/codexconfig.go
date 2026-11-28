// A Codex job is not started on a repository whose .codex/ the human has not seen (R9-S5).
//
// A supervised Codex job gets crowsnest's hooks through --dangerously-bypass-hook-trust (DESIGN
// 20.1), and that flag runs a repository's own .codex/hooks.json without Codex's per-hook review;
// a project .codex/config.toml can also name MCP servers, which Codex starts outside its sandbox.
// The agent itself cannot write the workspace's .codex/ (codex-cli 0.167 keeps it read-only in
// workspace-write, like .git: verified with `codex sandbox`, for a missing and an existing one),
// so what remains is what the repository brings: a clone, a pull, a branch. So the job's working
// directory is fingerprinted just before its agent starts, and the fingerprint must be one the
// human acknowledged: by trusting the folder while the project form or banner listed its .codex
// files, or with the project page's "Allow it for Codex jobs" (POST …/codex-config). A later change
// to any file there stops the next Codex job with the reason, until the human looks again.

package jobs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/agenttrust"
	"github.com/jclement/crowsnest/internal/store"
)

// codexSeenPrefix keys the kv records of the .codex/ fingerprints the human has seen; a restore
// drops them (store.ScrubRestored).
const codexSeenPrefix = store.CodexConfigSeenPrefix

// CodexConfigState is a folder's .codex/ and whether the human has seen it as it is now.
type CodexConfigState struct {
	agenttrust.CodexConfig
	// Seen is true when there is no .codex/ or its fingerprint was acknowledged.
	Seen bool `json:"seen"`
	// Error is why the folder's .codex/ could not be fingerprinted ("" when it could).
	Error string `json:"error,omitempty"`
}

// CodexConfig reports dir's .codex/ and whether the human has seen it.
func (s *Service) CodexConfig(ctx context.Context, dir string) CodexConfigState {
	config, err := agenttrust.CodexConfigOf(dir)
	if err != nil {
		return CodexConfigState{CodexConfig: config, Error: err.Error()}
	}
	if config.Fingerprint == "" {
		return CodexConfigState{CodexConfig: config, Seen: true}
	}
	value, _ := s.opts.Store.GetKV(ctx, codexSeenPrefix+config.Fingerprint)
	return CodexConfigState{CodexConfig: config, Seen: value != ""}
}

// SeeCodexConfig records that the human has seen dir's .codex/ as fingerprint (what their page
// showed them). A fingerprint that no longer matches the folder is refused: the files changed
// after the page was drawn, and the human has not seen the new ones.
func (s *Service) SeeCodexConfig(ctx context.Context, dir, fingerprint string) (CodexConfigState, error) {
	state := s.CodexConfig(ctx, dir)
	switch {
	case state.Error != "":
		return state, &ValidationError{Message: "The folder's .codex/ cannot be read safely, so it cannot be allowed: " + state.Error}
	case state.Fingerprint == "":
		return state, nil
	case fingerprint != state.Fingerprint:
		return state, &ValidationError{Message: "The folder's .codex/ changed since this page showed it. Look at it again, then allow it."}
	}
	if err := s.opts.Store.SetKV(ctx, codexSeenPrefix+state.Fingerprint, dir+" "+s.opts.Now().UTC().Format(time.RFC3339)); err != nil {
		return state, fmt.Errorf("recording the .codex/ as seen: %w", err)
	}
	state.Seen = true
	return state, nil
}

// codexConfigGate refuses to start a supervised Codex job in a working directory whose .codex/
// the human has not seen. The error is the job's needs-you reason.
func (s *Service) codexConfigGate(ctx context.Context, job Job) error {
	if job.Engine != EngineCodex || !s.codexSupervised() || job.WorkDir == "" {
		return nil
	}
	state := s.CodexConfig(ctx, job.WorkDir)
	switch {
	case state.Error != "":
		return fmt.Errorf("not started: this folder's .codex/ cannot be checked (%s), and Codex would run it outside its sandbox", state.Error)
	case !state.Seen:
		return fmt.Errorf("not started: this folder carries its own Codex configuration (%s), which a Codex job runs outside its sandbox with no review, and it is not what you allowed. Look at it on the project's page and allow it for Codex jobs, then give the job again", strings.Join(state.Files, ", "))
	}
	return nil
}
