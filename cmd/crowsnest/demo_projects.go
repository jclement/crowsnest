// --demo's projects (DESIGN.md 21.5): three projects in throwaway directories under the demo's
// state directory, the demo's jobs filed under them by directory, two leads running (on the fake
// herdr, answering with canned replies), two schedules made in the app, and edited instruction
// layers, so every new view has something real to show and the hermetic suite has fixtures.

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/instructions"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/schedule"
)

// demoProject is one seeded project and the made-up directory its demo jobs name.
type demoProject struct {
	fakeDir string // the directory the demo jobs name ("~/src/crowsnest")
	input   projects.Input
	lead    bool // start its lead
}

func strPtr(s string) *string { return &s }

func demoProjectList() []demoProject {
	return []demoProject{
		{fakeDir: "~/src/crowsnest", lead: true, input: projects.Input{Name: strPtr("Crowsnest"), Slug: strPtr("crowsnest"),
			Description: strPtr("The web app and supervisor for herdr sessions."), DefaultRole: strPtr("builder"), LeadStart: strPtr(projects.LeadOnJob)}},
		{fakeDir: "~/src/website", lead: true, input: projects.Input{Name: strPtr("Website"), Slug: strPtr("website"),
			Description: strPtr("The marketing site and docs."), DefaultRole: strPtr("builder"), LeadStart: strPtr(projects.LeadOnOpen)}},
		{fakeDir: "~/src/infra", input: projects.Input{Name: strPtr("Infra"), Slug: strPtr("infra"),
			Description: strPtr("Deploy scripts and the devbox image."), LeadStart: strPtr(projects.LeadManual)}},
	}
}

// seedDemoProjects creates the demo projects (once per demo database) and returns a mapping
// from the demo jobs' directories to them.
func seedDemoProjects(ctx context.Context, cfg config.Config, w *projectWiring, logger *slog.Logger) func(string) (int64, string) {
	byFake := map[string]projects.Project{}
	for _, seed := range demoProjectList() {
		dir := filepath.Join(cfg.StateDir, "demo-src", strings.TrimPrefix(seed.fakeDir, "~/src/"))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			logger.Warn("making a demo project directory", "err", err)
			continue
		}
		seed.input.Dir = &dir
		project, err := w.service.Create(ctx, seed.input)
		if err != nil {
			if existing, getErr := w.service.Get(ctx, *seed.input.Slug); getErr == nil {
				project = existing
			} else {
				logger.Warn("seeding a demo project", "err", err)
				continue
			}
		}
		byFake[seed.fakeDir] = project
	}
	return func(dir string) (int64, string) {
		if project, ok := byFake[dir]; ok {
			return project.ID, project.Dir
		}
		return 0, ""
	}
}

// seedDemoExtras starts the demo leads and adds the demo's schedules and instruction layers.
func seedDemoExtras(ctx context.Context, w *projectWiring, leads *projects.Leads, tasks *schedule.Service, layers *instructions.Service, logger *slog.Logger) {
	for _, seed := range demoProjectList() {
		project, err := w.service.Get(ctx, *seed.input.Slug)
		if err != nil || !seed.lead {
			continue
		}
		if _, err := leads.Start(ctx, project.ID); err != nil {
			logger.Warn("starting a demo lead", "project", project.Slug, "err", err)
		}
	}
	crowsnest, _ := w.service.Get(ctx, "crowsnest")
	website, _ := w.service.Get(ctx, "website")
	if tasks != nil {
		for _, input := range []schedule.ScheduleInput{
			{Name: "evening-review", Every: "1d", After: "18:00", As: "poke", ProjectID: crowsnest.ID,
				BodyMD: "# Evening review\n\nList what the project's jobs finished today and what is still open, in five lines."},
			{Name: "link-check", Every: "4h", As: "job", ProjectID: website.ID, Role: "tester",
				BodyMD: "# Check the site's links\n\nCrawl the built site and report broken links with the page they are on."},
		} {
			if err := tasks.Create(ctx, input); err != nil {
				logger.Debug("seeding a demo schedule", "name", input.Name, "err", err)
			}
		}
	}
	if layers != nil {
		for _, layer := range []struct{ scope, key, body string }{
			{"global", "", "Write for a phone: lead with the answer and keep replies short.\n\nNever push to `main`; open a pull request instead."},
			{"role", "builder", "# Role: builder\n\nYou implement one unit of work in your own worktree, test it, and report evidence.\n\n- Run only the tests for what you touched while working; the full suite once at the end.\n- Commit as you go."},
			{"project", formatProjectKey(crowsnest.ID), "Crowsnest's gate is `mise run check`. Screenshots live in `web/screenshots`."},
		} {
			if err := layers.Save(ctx, layer.scope, layer.key, layer.body); err != nil {
				logger.Debug("seeding a demo instruction layer", "scope", layer.scope, "err", err)
			}
		}
	}
}

func formatProjectKey(id int64) string { return strconv.FormatInt(id, 10) }
