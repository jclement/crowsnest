// The folder picker's browser (internal/browse, DESIGN.md 4.23), wired to the rest of serve: its
// roots beyond the home directory and --browse-root come from the projects and the remembered
// directories (read on every call, so a new project is browsable at once), its favourites add
// herdr workspaces' directories and projects' parents, the check command is the one acceptance
// would run, and Claude's folder trust is read from its global config.
//
// --demo browses a small real tree made in its throwaway state directory instead of the home
// directory, so the hermetic suite and screenshots never show (or depend on) the machine's own
// files.

package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/browse"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/projects"
)

// browserSources are what the picker reads besides the disk.
type browserSources struct {
	jobs     *jobs.Service
	projects *projects.Service
	fleet    *fleet.Cache
}

func newBrowser(cfg config.Config, sources browserSources) *browse.Service {
	home, _ := os.UserHomeDir()
	trust := &claudeTrust{path: claudeGlobalConfig(home)}
	trusted := trust.trusted
	if cfg.Demo {
		home = filepath.Join(cfg.StateDir, "demo-home")
		trusted = nil // the demo's folders are made up: nothing to tell
	}
	return browse.New(browse.Options{
		Home:         home,
		Extra:        cfg.BrowseRoots,
		Sources:      sources.places,
		CheckCommand: jobs.AutoCheckCommand,
		Trusted:      trusted,
	})
}

// places lists projects (and their parents), remembered directories and workspace directories.
func (s browserSources) places(ctx context.Context) []browse.Place {
	var out []browse.Place
	if s.projects != nil {
		if list, err := s.projects.List(ctx, false); err == nil {
			for _, project := range list {
				out = append(out, browse.Place{Path: project.Dir, Label: project.Name, Kind: browse.KindProject})
			}
			for _, project := range list {
				out = append(out, browse.Place{Path: filepath.Dir(project.Dir), Kind: browse.KindProjectParent})
			}
		}
	}
	if s.jobs != nil {
		for _, dir := range s.jobs.RememberedDirs(ctx) {
			out = append(out, browse.Place{Path: dir, Kind: browse.KindRemembered})
		}
	}
	if s.fleet != nil {
		if view, err := s.fleet.Fleet(ctx); err == nil {
			for _, workspace := range view.Workspaces {
				for _, tab := range workspace.Tabs {
					for _, pane := range tab.Panes {
						if pane.Cwd != "" {
							out = append(out, browse.Place{Path: pane.Cwd, Kind: browse.KindRecent})
						}
					}
				}
			}
		}
	}
	return out
}

// ---- Claude's folder trust --------------------------------------------------------------------

// claudeConfigLimit caps the read of Claude's global config; it holds every project Claude has
// seen and can reach megabytes, but past this it is not "cheaply knowable".
const claudeConfigLimit = 16 << 20

// claudeGlobalConfig is $CLAUDE_CONFIG_DIR/.claude.json, else ~/.claude.json (as the doctor).
func claudeGlobalConfig(home string) string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// claudeTrust answers "does Claude trust this folder" from its config, re-read only when the
// file's size or modification time changes.
type claudeTrust struct {
	path string
	mu   sync.Mutex
	size int64
	mod  time.Time
	body []byte
}

func (c *claudeTrust) trusted(dir string) (trusted, ok bool) {
	info, err := os.Stat(c.path)
	if err != nil || info.Size() > claudeConfigLimit {
		return false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.body == nil || info.Size() != c.size || !info.ModTime().Equal(c.mod) {
		body, readErr := os.ReadFile(c.path)
		if readErr != nil {
			return false, false
		}
		c.body, c.size, c.mod = body, info.Size(), info.ModTime()
	}
	return doctor.DirTrusted(c.body, dir), true
}

// ---- The demo's tree --------------------------------------------------------------------------

// demoTreeFiles is the demo's home: two repositories (GitHub with a check task and AGENTS.md;
// Azure DevOps with CLAUDE.md), a folder that is not a repository, a hidden folder and a file.
// Git's state is the handful of metadata files the browser reads; no git is run.
var demoTreeFiles = map[string]string{
	"Developer/acme-api/.git/HEAD":                     "ref: refs/heads/main\n",
	"Developer/acme-api/.git/config":                   "[core]\n\tbare = false\n[remote \"origin\"]\n\turl = git@github.com:acme/acme-api.git\n",
	"Developer/acme-api/.git/refs/heads/main":          "0000000000000000000000000000000000000001\n",
	"Developer/acme-api/.git/refs/remotes/origin/HEAD": "ref: refs/remotes/origin/main\n",
	"Developer/acme-api/mise.toml":                     "[tasks.check]\nrun = \"go test ./...\"\n",
	"Developer/acme-api/AGENTS.md":                     "# Working here\n",
	"Developer/acme-api/README.md":                     "# acme-api\n",
	"Developer/acme-api/cmd/api/.keep":                 "",
	"Developer/billing/.git/HEAD":                      "ref: refs/heads/develop\n",
	"Developer/billing/.git/config":                    "[remote \"origin\"]\n\turl = https://acme@dev.azure.com/acme/billing/_git/billing\n",
	"Developer/billing/.git/refs/remotes/origin/HEAD":  "ref: refs/remotes/origin/develop\n",
	"Developer/billing/CLAUDE.md":                      "# Billing\n",
	"Developer/scratch/notes.md":                       "scratch\n",
	"Work/handbook-drafts/.keep":                       "",
	".config/.keep":                                    "",
	"todo.txt":                                         "the demo's file\n",
}

// seedDemoTree writes the demo's home under its state directory (idempotent).
func seedDemoTree(cfg config.Config) error {
	home := filepath.Join(cfg.StateDir, "demo-home")
	for name, body := range demoTreeFiles {
		path := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			return err
		}
	}
	return nil
}
