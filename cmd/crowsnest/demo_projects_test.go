// Tests for --demo's projects as a first impression (R10, R8 H13): every project folder is a git
// repository and trusted in the demo's own agent config, so no project page opens on a warning.

package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/demostory"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/store"
)

func TestDemoProjectsAreTrustedRepositories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := config.Config{Demo: true, StateDir: t.TempDir()}
	wiring := &projectWiring{service: projects.New(projects.Options{Store: db, Home: t.TempDir()})}
	projectOf := seedDemoProjects(ctx, cfg, wiring, slog.New(slog.NewTextHandler(io.Discard, nil)))
	trust := newFolderTrust(cfg, func() config.Config { return cfg })
	for _, slug := range []string{demostory.Crowsnest, demostory.Mobile, demostory.Infra, demostory.Handbook, demostory.Website} {
		_, dir := projectOf(demostory.Dir(slug))
		if dir == "" {
			t.Fatalf("%s: not seeded", slug)
		}
		if !trust.Trusted(dir) {
			t.Errorf("%s: %s is not trusted in the demo's agent config", slug, dir)
		}
		if slug == demostory.Website {
			continue // the diff demo makes Website's repository, with its worktrees
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			t.Errorf("%s: %s is not a git repository", slug, dir)
		}
	}
	// Seeding again (a reused demo database) changes nothing and fails nothing.
	seedDemoProjects(ctx, cfg, wiring, slog.New(slog.NewTextHandler(io.Discard, nil)))
}
