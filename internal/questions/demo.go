// Demo questions for `crowsnest --demo` (DESIGN.md 8.4, 21.5): a seeded inbox with every kind (a
// mermaid body, a diff, a multi-select among them), and a script that asks a new one every so
// often so the Inbox, its badge and the browser notification visibly work. Every question is
// asked from the pane of a demo job (internal/demostory) and names that job, with context.repo
// its project's slug, so an answer is typed back into that (fake) job agent like the real thing
// and the job's card counts it. Two of the jobs have declared they wait on theirs (internal/jobs
// seeds them needs_you), so those questions are marked blocking.

package questions

import (
	"context"
	"log/slog"
	"time"

	"github.com/jclement/crowsnest/internal/demostory"
)

// DefaultDemoAskInterval is how often the demo asks a new question.
const DefaultDemoAskInterval = 90 * time.Second

// DemoPanes is how the demo finds the job agents its questions come from.
type DemoPanes struct {
	Askers demostory.Askers
}

// SeedDemo fills an empty inbox with the demo questions. An inbox that already has questions
// (a reused demo data dir) is left alone.
func SeedDemo(ctx context.Context, service *Service, panes DemoPanes) error {
	existing, err := service.List(ctx, "all")
	if err != nil || len(existing) > 0 {
		return err
	}
	for _, seed := range demoSeeds() {
		if _, err := service.Ask(ctx, seed.input, panes.caller(seed.agent)); err != nil {
			return err
		}
	}
	// One settled question so the History tab is not empty.
	history, err := service.Ask(ctx, AskInput{
		Title: "Keep the retry schedule in SQLite?", Kind: KindConfirm, Recommend: "yes",
		BodyMD: "One table, no server, and WAL handles our write rate.", Context: map[string]string{"repo": demostory.Crowsnest},
	}, panes.caller(demostory.WebhookBuilder))
	if err != nil {
		return err
	}
	if _, err = service.Answer(ctx, history.ID, AnswerInput{Value: []byte(`"yes"`), Note: "Yes, modernc so CGO stays off."}); err != nil {
		return err
	}
	// Last, the question the orchestrator's release job waits on.
	_, err = service.Ask(ctx, AskInput{
		Title: "Ship v0.2 on Friday?", Kind: KindChoice, Recommend: "fri",
		BodyMD:  "The release notes are drafted. Friday gives a quiet weekend to watch it; Monday gives the docs another pass.",
		Options: []Option{{Key: "fri", Label: "Friday"}, {Key: "mon", Label: "Next Monday"}},
	}, panes.caller(demostory.Orchestrator))
	return err
}

// caller is the demo agent named agent as an asker: its pane and the job it holds.
func (panes DemoPanes) caller(agent string) Caller {
	if panes.Askers == nil {
		return Caller{}
	}
	asker := panes.Askers(agent)
	return Caller{PaneID: asker.PaneID, JobID: asker.JobID}
}

// RunDemo asks one question from the rotation every interval until ctx ends.
func RunDemo(ctx context.Context, service *Service, panes DemoPanes, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		interval = DefaultDemoAskInterval
	}
	rotation := demoRotation()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for next := 0; ; next++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		seed := rotation[next%len(rotation)]
		if _, err := service.Ask(ctx, seed.input, panes.caller(seed.agent)); err != nil && ctx.Err() == nil {
			logger.Warn("demo: asking a question", "err", err)
		}
	}
}

// demoSeed is one scripted question and the demo agent that asks it.
type demoSeed struct {
	agent string
	input AskInput
}

// demoSeeds are q_1 to q_6, in the order tests name them.
func demoSeeds() []demoSeed {
	return []demoSeed{
		{demostory.CacheBuilder, AskInput{
			Title: "Apply the fleet cache refactor?", Kind: KindConfirm, Recommend: "yes", Urgency: UrgencyBlocking,
			Options: []Option{{Key: "yes", Label: "Apply it", Detail: "Tests pass locally (42/42)"}, {Key: "no", Label: "Not yet"}},
			Context: map[string]string{"repo": demostory.Crowsnest, "branch": "wip/fleet-cache", "worker": demostory.CacheBuilder},
			BodyMD: "The refresh now **coalesces** events instead of resetting a debounce timer, so a busy pane " +
				"cannot starve the refetch.\n\n```mermaid\nsequenceDiagram\n  participant H as herdr\n  participant C as cache\n" +
				"  H->>C: pane_updated\n  Note over C: start 150 ms window\n  H->>C: pane_updated (rides along)\n  C->>H: session.snapshot\n" +
				"  H-->>C: snapshot\n  C-->>C: publish fleet\n```\n\nThe diff is below. Nothing else changes.",
			Files: []File{{Filename: "internal/fleet/cache.go", Original: demoOriginal, Final: demoFinal}},
		}},
		{demostory.CacheReviewer, AskInput{
			Title: "Which findings should the builder fix before merge?", Kind: KindChoice, Multi: true, Recommend: "race",
			Options: []Option{
				{Key: "race", Label: "Data race in the hub", Detail: "Publish reads subscribers without the lock"},
				{Key: "leak", Label: "Goroutine leak on SSE close"},
				{Key: "names", Label: "Rename handleX helpers", Detail: "Cosmetic"},
				{Key: "docs", Label: "Document the 150 ms window"},
			},
			Context: map[string]string{"repo": demostory.Crowsnest, "branch": "wip/fleet-cache", "worker": demostory.CacheReviewer},
			BodyMD:  "Four findings from the review of `wip/fleet-cache`. Pick any number; the rest go to a follow-up.",
		}},
		{demostory.LimiterReviewer, AskInput{
			Title: "Where should the release notes live?", Kind: KindChoice, Recommend: "changelog",
			Options: []Option{
				{Key: "changelog", Label: "CHANGELOG.md in the repo"},
				{Key: "releases", Label: "GitHub release text only"},
				{Key: "both", Label: "Both, generated from commits"},
			},
			BodyMD:  "The limiter fix needs a release note before it lands. GoReleaser can write either; a file in the repo is reviewable in pull requests.",
			Context: map[string]string{"repo": demostory.Crowsnest, "branch": "fix/limiter", "worker": demostory.LimiterReviewer},
		}},
		{demostory.HeroWriter, AskInput{
			Title: "What should the homepage hero say?", Kind: KindManual, Urgency: UrgencyBlocking,
			BodyMD:  "Its first sentence today reads *\"Crowsnest is a web UI for herdr.\"* That undersells jobs. Write the sentence you want; I'll fit the rest of the hero to it.",
			Context: map[string]string{"repo": demostory.Website, "worker": demostory.HeroWriter},
		}},
		{demostory.WebhookBuilder, AskInput{
			Title: "Heads up: the integration suite needs herdr 0.9.5", Kind: KindAck,
			BodyMD:  "Older herdr builds answer `agent.start` without `launch_pending`; the suite skips with a message on them.",
			Context: map[string]string{"repo": demostory.Crowsnest, "worker": demostory.WebhookBuilder},
		}},
		{demostory.InstallGuide, AskInput{
			Title: "Preview build finished: 214 pages, 14 s", Kind: KindNotice, Urgency: UrgencyLow,
			Context: map[string]string{"repo": demostory.Website, "worker": demostory.InstallGuide},
		}},
	}
}

// demoRotation is asked one at a time, from jobs that are running (so a new question does not
// change what they wait on) or already waiting on a question; never from Infra, which stays the
// project with nothing for the human.
func demoRotation() []demoSeed {
	return []demoSeed{
		{demostory.WebhookBuilder, AskInput{
			Title: "Bump Go to 1.27.4?", Kind: KindConfirm, Recommend: "yes",
			BodyMD:  "A point release with a `net/http` fix. CI is green on it.",
			Context: map[string]string{"repo": demostory.Crowsnest, "worker": demostory.WebhookBuilder},
		}},
		{demostory.CacheReviewer, AskInput{
			Title: "Squash or keep the commit history?", Kind: KindChoice, Recommend: "keep",
			Options: []Option{{Key: "squash", Label: "Squash into one"}, {Key: "keep", Label: "Keep the 12 commits"}},
			Context: map[string]string{"repo": demostory.Crowsnest, "branch": "wip/fleet-cache", "worker": demostory.CacheReviewer},
		}},
		{demostory.HeroWriter, AskInput{
			Title: "Publish the site as soon as the hero is in?", Kind: KindConfirm, Recommend: "no", Urgency: UrgencyBlocking,
			BodyMD:  "This deploys straight to production. Say no to wait for a review of the new hero.",
			Context: map[string]string{"repo": demostory.Website, "worker": demostory.HeroWriter},
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
