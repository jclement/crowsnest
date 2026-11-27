package main

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jclement/crowsnest/internal/agenttrust"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/engines"
)

// engineCatalog is GET /engines' source: this machine's CLIs, except in the demo, whose table must
// not depend on what is installed (the hermetic browser suite runs it).
func engineCatalog(cfg config.Config) *engines.Catalog {
	if cfg.Demo {
		return nil
	}
	return engines.NewCatalog()
}

// folderTrust is httpd.FolderTrust over the agents' real config files (DESIGN.md 8.2.1). The
// setting is read live on every call. A demo instance writes into its own state directory, never
// the user's agent files.
type folderTrust struct {
	enabled func() bool
	paths   agenttrust.Paths
}

func newFolderTrust(cfg config.Config, live func() config.Config) *folderTrust {
	home, _ := os.UserHomeDir()
	paths := agenttrust.DefaultPaths(home, os.Getenv)
	if cfg.Demo {
		dir := filepath.Join(cfg.StateDir, "demo-agent-config")
		paths = agenttrust.Paths{ClaudeConfig: filepath.Join(dir, ".claude.json"), CodexConfig: filepath.Join(dir, "codex", "config.toml")}
	}
	return &folderTrust{enabled: func() bool { return live().TrustProjectFolders }, paths: paths}
}

func (f *folderTrust) Enabled() bool { return f.enabled() }

func (f *folderTrust) Trust(dirs []string) []agenttrust.Result {
	_, err := exec.LookPath("codex")
	return agenttrust.Trust(f.paths, dirs, err == nil)
}

func (f *folderTrust) Trusted(dir string) bool {
	data, err := os.ReadFile(f.paths.ClaudeConfig)
	return err == nil && doctor.DirTrusted(data, dir)
}
