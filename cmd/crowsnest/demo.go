// --demo gets throwaway state (R1 L2): a fresh temporary directory holds its database, its state
// directory (the demo transcripts, CROWSNEST.md) and the orchestrator's working directory, and
// is removed when serve returns. A demo run therefore never writes login sessions or an
// orchestrator session id into the real instance's database, never rewrites its CROWSNEST.md,
// and a session cookie a demo hands out is worthless against the real instance.

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jclement/crowsnest/internal/config"
)

// demoState points a --demo config at a new temporary directory and returns the cleanup that
// removes it. Without --demo it returns cfg unchanged and a no-op.
func demoState(cfg config.Config) (config.Config, func(), error) {
	if !cfg.Demo {
		return cfg, func() {}, nil
	}
	root, err := os.MkdirTemp("", "crowsnest-demo-")
	if err != nil {
		return cfg, func() {}, fmt.Errorf("creating the demo's temporary directory: %w", err)
	}
	cfg.DataDir = filepath.Join(root, "data")
	cfg.StateDir = filepath.Join(root, "state")
	cfg.Dir = filepath.Join(root, "orchestrator")
	return cfg, func() { _ = os.RemoveAll(root) }, nil
}
