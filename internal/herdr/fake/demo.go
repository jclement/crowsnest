// The --demo scenario: a believable canned fleet on the fake, and a slow script that changes it
// so a UI pointed at `crowsnest --demo` visibly updates. Three workspaces, five agents in every
// state, one blocked on a permission dialog that a key press from the UI answers (and that comes
// back a little later, so there is always something to try).

package fake

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
)

// DefaultDemoStep is how often the demo script changes something.
const DefaultDemoStep = 6 * time.Second

// Demo agent names, stable so tests and E2E can address them.
const (
	DemoOrchestrator = "orchestrator"
	DemoBuilder      = "builder"
	DemoReviewer     = "reviewer"
	DemoDocs         = "docs-writer"
	DemoLinter       = "linter"
)

// Demo is a fake session populated with the demo fleet, plus the script that animates it.
type Demo struct {
	*Fake
	mu        sync.Mutex
	step      int
	reblockAt int            // step at which the docs writer hits its next dialog; 0 = blocked now
	busyUntil map[string]int // pane id → step at which a prompted agent goes idle again
	panes     map[string]string
	scribe    *scribe // writes Claude-shaped transcripts; nil until EnableTranscripts
}

// NewDemo builds the demo fleet.
func NewDemo() *Demo {
	demo := &Demo{Fake: New(), busyUntil: map[string]int{}, panes: map[string]string{}}
	demo.build()
	demo.OnKeys(demo.answerDialog)
	demo.OnPrompt(demo.startTurn)
	return demo
}

func (d *Demo) build() {
	_, _, orch := d.AddWorkspace("orchestrator", "~/.local/share/crowsnest/orchestrator")
	d.agent(orch, DemoOrchestrator, "claude", herdr.StatusIdle, "4d1f6c2a-0b7e-4a51-9a63-2f1d8c0e7b11")
	d.idle(orch, "Morning. Two builders are running; the docs writer is waiting on you.")

	crow, _, builder := d.AddWorkspace("crowsnest", "~/src/crowsnest")
	d.agent(builder, DemoBuilder, "claude", herdr.StatusWorking, "9b0e2f44-6c1d-4f7a-8e25-5d3a91c4e0f2")
	d.working(builder, "Writing internal/fleet/cache.go", 42)
	_, reviewer := d.AddTab(crow, "review", "~/src/crowsnest")
	d.agent(reviewer, DemoReviewer, "claude", herdr.StatusDone, "c2a7d9e1-3f50-4b86-a1c4-7e9f20d6b833")
	d.idle(reviewer, "Review written to REVIEW.md: 2 findings, both minor.")
	_, shell := d.AddTab(crow, "shell", "~/src/crowsnest")
	_ = d.SetScreen(shell, "~/src/crowsnest on wip/m3\n❯ mise run test\nok  internal/fleet  0.41s\nok  internal/herdr  1.33s\n\n~/src/crowsnest on wip/m3\n❯ \n", "")

	site, _, docs := d.AddWorkspace("website", "~/src/website")
	d.agent(docs, DemoDocs, "claude", herdr.StatusBlocked, "e5b3a8f0-7d21-4c9e-b6f4-1a0c3e8d5f27")
	d.dialog(docs, "rm -rf dist && npm run build", "Rebuild the site from scratch")
	_, linter := d.AddTab(site, "lint", "~/src/website")
	d.agent(linter, DemoLinter, "codex", herdr.StatusIdle, "")
	d.idle(linter, "No lint errors in 214 files.")
}

func (d *Demo) agent(paneID, name, kind string, status herdr.AgentStatus, session string) {
	_ = d.AddAgent(paneID, name, kind, status, session)
	d.panes[name] = paneID
}

// PaneOf returns the pane id of a demo agent.
func (d *Demo) PaneOf(name string) string { return d.panes[name] }

// Run animates the fleet until ctx ends, one change set every step (DefaultDemoStep when zero).
func (d *Demo) Run(ctx context.Context, step time.Duration) {
	if step <= 0 {
		step = DefaultDemoStep
	}
	ticker := time.NewTicker(step)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.Step()
		}
	}
}

// Step applies one round of scripted changes. Exported so tests can drive the demo without
// waiting on a clock.
func (d *Demo) Step() {
	d.mu.Lock()
	d.step++
	n := d.step
	reblock := d.reblockAt != 0 && n >= d.reblockAt
	if reblock {
		d.reblockAt = 0
	}
	var finished []string
	for pane, until := range d.busyUntil {
		if n >= until {
			finished = append(finished, pane)
			delete(d.busyUntil, pane)
		}
	}
	d.mu.Unlock()

	d.cycleBuilders(n)
	if n%3 == 0 {
		d.toggle(d.panes[DemoLinter], "Checking 214 files", "No lint errors in 214 files.", n)
	}
	if reblock {
		d.dialog(d.panes[DemoDocs], "git push --force origin gh-pages", "Publish the rebuilt site")
		_ = d.SetStatus(DemoDocs, herdr.StatusBlocked)
	}
	for _, pane := range finished {
		d.idle(pane, "Done. That is handled.")
		_ = d.SetStatus(pane, herdr.StatusIdle)
	}
}

// cycleBuilders alternates the builder and reviewer between working and settled.
func (d *Demo) cycleBuilders(n int) {
	builder, reviewer := d.panes[DemoBuilder], d.panes[DemoReviewer]
	switch n % 4 {
	case 1:
		d.idle(builder, "Fleet cache done; 7 tests added, all passing.")
		_ = d.SetStatus(DemoBuilder, herdr.StatusDone)
		d.working(reviewer, "Reviewing the fleet cache", 3)
		_ = d.SetStatus(DemoReviewer, herdr.StatusWorking)
	case 3:
		d.working(builder, "Wiring /api/v1/fleet", 5+n)
		_ = d.SetStatus(DemoBuilder, herdr.StatusWorking)
		d.idle(reviewer, "Review written to REVIEW.md: no new findings.")
		_ = d.SetStatus(DemoReviewer, herdr.StatusDone)
	}
}

func (d *Demo) toggle(pane, task, result string, n int) {
	if (n/3)%2 == 1 {
		d.working(pane, task, 2)
		_ = d.SetStatus(pane, herdr.StatusWorking)
		return
	}
	d.idle(pane, result)
	_ = d.SetStatus(pane, herdr.StatusIdle)
}

// answerDialog is the demo's KeysHook: a digit on the blocked docs writer answers its dialog,
// and the next dialog arrives a few steps later.
func (d *Demo) answerDialog(f *Fake, paneID string, keys []string) {
	if paneID != d.panes[DemoDocs] || len(keys) == 0 {
		return
	}
	agent, err := f.AgentGet(context.Background(), paneID)
	if err != nil || agent.AgentStatus != herdr.StatusBlocked {
		return
	}
	switch keys[0] {
	case "1", "2":
		d.working(paneID, "Running the approved command", 1)
	case "3", "esc":
		d.idle(paneID, "Understood, I will not run that. What should I do instead?")
		_ = f.SetStatus(paneID, herdr.StatusIdle)
		d.scheduleReblock(4)
		return
	default:
		return
	}
	_ = f.SetStatus(paneID, herdr.StatusWorking)
	d.scheduleReblock(3)
}

func (d *Demo) scheduleReblock(steps int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reblockAt = d.step + steps
}

// startTurn is the demo's PromptHook: a prompted agent works for two steps, then goes idle.
func (d *Demo) startTurn(_ *Fake, paneID, text string) {
	if d.scribe != nil && paneID == d.panes[DemoOrchestrator] {
		d.orchestratorTurn(paneID, text)
		return
	}
	d.working(paneID, "Working on: "+firstLine(text), 0)
	d.mu.Lock()
	d.busyUntil[paneID] = d.step + 2
	d.mu.Unlock()
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if len(line) > 60 {
		line = line[:57] + "…"
	}
	return line
}

// ---- Screens -------------------------------------------------------------------------

const esc = "\x1b"

var (
	demoRule   = esc + "[38;5;244m" + strings.Repeat("─", 100) + esc + "[39m"
	demoFooter = "  " + esc + "[2m⏵⏵ auto mode on (shift+tab to cycle)" + esc + "[22m"
	sgr        = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

func (d *Demo) setANSI(pane, ansi string) {
	_ = d.SetScreen(pane, sgr.ReplaceAllString(ansi, ""), ansi)
}

func (d *Demo) idle(pane, message string) {
	d.setANSI(pane, fmt.Sprintf("%s[1m⏺%s[22m %s\n\n%s\n❯ %s[7m %s[27m%s[2mTry \"summarise what changed today\"%s[22m\n%s\n%s\n",
		esc, esc, message, demoRule, esc, esc, esc, esc, demoRule, demoFooter))
}

func (d *Demo) working(pane, task string, seconds int) {
	d.setANSI(pane, fmt.Sprintf("%s[1m⏺%s[22m %s\n\n%s[38;5;174m✻%s[39m Working… (%ds · esc to interrupt)\n\n%s\n❯ %s[7m %s[27m\n%s\n%s\n",
		esc, esc, task, esc, esc, seconds, demoRule, esc, esc, demoRule, demoFooter))
}

func (d *Demo) dialog(pane, command, description string) {
	d.setANSI(pane, fmt.Sprintf("%s[1m⏺%s[22m Bash(%s)\n\n%s\n %s[1mBash command%s[22m\n\n   %s\n   %s\n\n Do you want to proceed?\n %s[36m❯ 1. Yes%s[39m\n   2. Yes, and don't ask again for this command\n   3. No, and tell Claude what to do differently %s[2m(esc)%s[22m\n\n %s[2mEsc to cancel · Tab to amend%s[22m\n",
		esc, esc, command, demoRule, esc, esc, command, description, esc, esc, esc, esc, esc, esc))
}
