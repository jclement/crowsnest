// Demo questions for `crowsnest --demo`: a seeded inbox with every kind (a mermaid body, a diff,
// a multi-select among them), and a script that asks a new one every so often so the Inbox, its
// badge and the browser notification visibly work. The askers are the demo fleet's agents, so an
// answer is typed back into a (fake) pane like the real thing.

package questions

import (
	"context"
	"log/slog"
	"time"
)

// DefaultDemoAskInterval is how often the demo asks a new question.
const DefaultDemoAskInterval = 90 * time.Second

// DemoPanes names the fake panes demo questions come from.
type DemoPanes struct {
	Orchestrator, Builder, Reviewer, Docs string
}

// SeedDemo fills an empty inbox with the demo questions. An inbox that already has questions
// (a reused demo data dir) is left alone.
func SeedDemo(ctx context.Context, service *Service, panes DemoPanes) error {
	existing, err := service.List(ctx, "all")
	if err != nil || len(existing) > 0 {
		return err
	}
	for _, seed := range demoSeeds(panes) {
		if _, err := service.Ask(ctx, seed.input, Caller{PaneID: seed.pane}); err != nil {
			return err
		}
	}
	// One settled question so the History tab is not empty.
	history, err := service.Ask(ctx, AskInput{
		Title: "Use SQLite for the session store?", Kind: KindConfirm, Recommend: "yes",
		BodyMD: "One file, no server, and WAL handles our write rate.", Context: map[string]string{"repo": "crowsnest"},
	}, Caller{PaneID: panes.Orchestrator})
	if err != nil {
		return err
	}
	_, err = service.Answer(ctx, history.ID, AnswerInput{Value: []byte(`"yes"`), Note: "Yes, modernc so CGO stays off."})
	return err
}

// RunDemo asks one question from the rotation every interval until ctx ends.
func RunDemo(ctx context.Context, service *Service, panes DemoPanes, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		interval = DefaultDemoAskInterval
	}
	rotation := demoRotation(panes)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for next := 0; ; next++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		seed := rotation[next%len(rotation)]
		if _, err := service.Ask(ctx, seed.input, Caller{PaneID: seed.pane}); err != nil && ctx.Err() == nil {
			logger.Warn("demo: asking a question", "err", err)
		}
	}
}

type demoSeed struct {
	pane  string
	input AskInput
}

func demoSeeds(panes DemoPanes) []demoSeed {
	return []demoSeed{
		{panes.Builder, AskInput{
			Title: "Apply the fleet cache refactor?", Kind: KindConfirm, Recommend: "yes", Urgency: UrgencyBlocking,
			Options: []Option{{Key: "yes", Label: "Apply it", Detail: "Tests pass locally (42/42)"}, {Key: "no", Label: "Not yet"}},
			Context: map[string]string{"repo": "crowsnest", "branch": "wip/fleet-cache", "worker": "builder"},
			BodyMD: "The refresh now **coalesces** events instead of resetting a debounce timer, so a busy pane " +
				"cannot starve the refetch.\n\n```mermaid\nsequenceDiagram\n  participant H as herdr\n  participant C as cache\n" +
				"  H->>C: pane_updated\n  Note over C: start 150 ms window\n  H->>C: pane_updated (rides along)\n  C->>H: session.snapshot\n" +
				"  H-->>C: snapshot\n  C-->>C: publish fleet\n```\n\nThe diff is below. Nothing else changes.",
			Files: []File{{Filename: "internal/fleet/cache.go", Original: demoOriginal, Final: demoFinal}},
		}},
		{panes.Reviewer, AskInput{
			Title: "Which findings should the builder fix before merge?", Kind: KindChoice, Multi: true, Recommend: "race",
			Options: []Option{
				{Key: "race", Label: "Data race in the hub", Detail: "Publish reads subscribers without the lock"},
				{Key: "leak", Label: "Goroutine leak on SSE close"},
				{Key: "names", Label: "Rename handleX helpers", Detail: "Cosmetic"},
				{Key: "docs", Label: "Document the 150 ms window"},
			},
			Context: map[string]string{"repo": "crowsnest", "worker": "reviewer"},
			BodyMD:  "Four findings from the review of `wip/fleet-cache`. Pick any number; the rest go to a follow-up.",
		}},
		{panes.Orchestrator, AskInput{
			Title: "Where should the release notes live?", Kind: KindChoice, Recommend: "changelog",
			Options: []Option{
				{Key: "changelog", Label: "CHANGELOG.md in the repo"},
				{Key: "releases", Label: "GitHub release text only"},
				{Key: "both", Label: "Both, generated from commits"},
			},
			BodyMD: "GoReleaser can write either. A file in the repo is reviewable in pull requests.",
		}},
		{panes.Docs, AskInput{
			Title: "What should the README's first sentence say?", Kind: KindManual,
			BodyMD:  "Today it reads *\"Crowsnest is a web UI for herdr.\"* That undersells jobs. Write the sentence you want.",
			Context: map[string]string{"repo": "crowsnest", "worker": "docs-writer"},
		}},
		{panes.Builder, AskInput{
			Title: "Heads up: the integration suite needs herdr 0.9.5", Kind: KindAck,
			BodyMD: "Older herdr builds answer `agent.start` without `launch_pending`; the suite skips with a message on them.",
		}},
		{panes.Orchestrator, AskInput{
			Title: "Nightly backup finished: 1.2 GB, 14 s", Kind: KindNotice, Urgency: UrgencyLow,
		}},
	}
}

func demoRotation(panes DemoPanes) []demoSeed {
	return []demoSeed{
		{panes.Builder, AskInput{
			Title: "Bump Go to 1.27.4?", Kind: KindConfirm, Recommend: "yes",
			BodyMD:  "A point release with a `net/http` fix. CI is green on it.",
			Context: map[string]string{"repo": "crowsnest", "worker": "builder"},
		}},
		{panes.Reviewer, AskInput{
			Title: "Squash or keep the commit history?", Kind: KindChoice, Recommend: "keep",
			Options: []Option{{Key: "squash", Label: "Squash into one"}, {Key: "keep", Label: "Keep the 12 commits"}},
		}},
		{panes.Docs, AskInput{
			Title: "Publish the docs site now?", Kind: KindConfirm, Recommend: "no", Urgency: UrgencyBlocking,
			BodyMD: "This force-pushes `gh-pages`. Say no to wait for the review.",
		}},
	}
}

const demoOriginal = `// refresh schedules a refetch after events go quiet.
func (c *Cache) onEvent() {
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timer = time.AfterFunc(debounce, c.refresh)
}
`

const demoFinal = `// onEvent schedules a refetch: the first event starts a window, and later events in it
// ride along, so continuous output cannot starve the refetch.
func (c *Cache) onEvent() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending {
		return
	}
	c.pending = true
	time.AfterFunc(coalesceWindow, c.refresh)
}
`
