// The --demo scenario: a believable canned fleet on the fake, and a slow script that changes it
// so a UI pointed at `crowsnest --demo` visibly updates (DESIGN.md 21.5). One workspace per demo
// project, named after it, plus the orchestrator's; every agent that holds a seeded job is named
// as that job's agent (internal/demostory), and sits in its project's workspace in the project's
// made-up directory. The Handbook job waits at a terminal dialog that a key press from the UI
// answers (and a second one arrives a little later, so there is always something to try); the
// panes the seeded approvals come from show the permission dialog their approval stands for
// (AwaitApproval) until it is decided (ApprovalSettled).

package fake

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/demostory"
	"github.com/jclement/crowsnest/internal/herdr"
)

// DefaultDemoStep is how often the demo script changes something.
const DefaultDemoStep = 6 * time.Second

// Demo agents that hold no job, stable so tests and E2E can address them. The agents that hold
// the seeded jobs are named in internal/demostory.
const (
	DemoOrchestrator = demostory.Orchestrator
	// DemoLinter is a Codex agent someone started by hand in the Mobile workspace.
	DemoLinter = "linter"
)

// The Handbook job's dialogs: the first is up at start; the second arrives a few steps after the
// first is answered. The job's cause detail is read from the screen (DialogCommand), never typed
// twice.
const (
	demoHandbookBuild   = "rm -rf dist && npm run build"
	demoHandbookPublish = "git push --force origin gh-pages"
)

// demoTasks are the progress lines a working job agent cycles through, one per step, so the
// Fleet shows work moving. They match what the job's own progress says (internal/jobs/demo.go).
var demoTasks = map[string][]string{
	demostory.WebhookBuilder: {"Writing table tests for the back-off schedule", "Running go test ./internal/webhook/", "Fixing the jitter bound", "Committing on job/1-add-retry-back-off"},
	demostory.InstallGuide:   {"Rewriting the macOS steps", "Rewriting the Linux steps", "Checking every command still runs"},
	demostory.CacheReviewer:  {"Reading internal/fleet/cache.go", "Checking the hub for races", "Writing findings to REVIEW.md"},
	demostory.DevboxBuilder:  {"Resolving the debian:bookworm-slim digest", "Rebuilding the devbox image", "Smoke-testing the new image"},
}

// demoTurn is a prompted agent's busy spell: the step it ends at, and the screen it goes back to.
type demoTurn struct {
	until  int
	resume string // the task it was working on ("" when it was not working: it goes idle)
}

// Demo is a fake session populated with the demo fleet, plus the script that animates it.
type Demo struct {
	*Fake
	mu        sync.Mutex
	step      int
	reblockAt int                 // step at which the Handbook job hits its next dialog; 0 = none due
	busy      map[string]demoTurn // pane id → a prompted agent's turn
	dialogs   map[string]string   // pane id → the command of the terminal dialog on it now
	tasks     map[string]string   // pane id → what a working agent is doing now
	panes     map[string]string   // agent name → pane id
	scribe    *scribe             // writes Claude-shaped transcripts; nil until EnableTranscripts
}

// NewDemo builds the demo fleet.
func NewDemo() *Demo {
	demo := &Demo{Fake: New(), busy: map[string]demoTurn{}, dialogs: map[string]string{}, tasks: map[string]string{}, panes: map[string]string{}}
	demo.build()
	demo.OnKeys(demo.answerDialog)
	demo.OnPrompt(demo.startTurn)
	return demo
}

// ---- The fleet ----------------------------------------------------------------------------

func (d *Demo) build() {
	_, _, orch := d.AddWorkspace("orchestrator", "~/.local/share/crowsnest/orchestrator")
	d.agent(orch, DemoOrchestrator, "claude", herdr.StatusIdle, "4d1f6c2a-0b7e-4a51-9a63-2f1d8c0e7b11")
	d.idle(orch, "Morning. Two questions and three tool calls wait on you; the handbook job is at a dialog.")
	d.buildCrowsnest()
	d.buildWebsite()
	d.buildMobile()
	d.buildInfra()
	d.buildHandbook()
}

func (d *Demo) buildCrowsnest() {
	dir := demostory.Dir(demostory.Crowsnest)
	ws, _, webhook := d.AddWorkspace(demostory.Crowsnest, "~/.herdr/worktrees/crowsnest/job-1")
	d.jobAgent(webhook, demostory.WebhookBuilder, "9b0e2f44-6c1d-4f7a-8e25-5d3a91c4e0f2")
	_, cache := d.AddTab(ws, "fleet cache", dir)
	d.agent(cache, demostory.CacheBuilder, "claude", herdr.StatusDone, "a81c3e5d-2b74-4f19-9d06-6e2f8b1c4a90")
	d.idle(cache, "The refresh coalesces now; tests pass (42/42). I asked whether to apply it and am waiting on your answer.")
	_, review := d.AddTab(ws, "cache review", dir)
	d.jobAgent(review, demostory.CacheReviewer, "c2a7d9e1-3f50-4b86-a1c4-7e9f20d6b833")
	_, limiter := d.AddTab(ws, "limiter review", dir)
	d.jobAgent(limiter, demostory.LimiterReviewer, "3e9f0a17-5c2d-4b8e-b7a1-0d4c6f2e9b58")
	d.working(limiter, "Rebasing fix/limiter onto main", 12)
	_, profile := d.AddTab(ws, "profile", dir)
	d.agent(profile, demostory.CacheProfiler, "claude", herdr.StatusIdle, "")
	d.idle(profile, "I can profile the cache once the herdr test session is up.")
	_, shell := d.AddTab(ws, "shell", dir)
	_ = d.SetScreen(shell, dir+" on main\n❯ mise run test\nok  internal/fleet  0.41s\nok  internal/herdr  1.33s\n\n"+dir+" on main\n❯ \n", "")
}

func (d *Demo) buildWebsite() {
	dir := demostory.Dir(demostory.Website)
	ws, _, install := d.AddWorkspace(demostory.Website, dir)
	d.jobAgent(install, demostory.InstallGuide, "")
	_, links := d.AddTab(ws, "links", dir)
	d.jobAgent(links, demostory.LinkFixer, "")
	d.working(links, "Fixing the links the link check found", 30)
	_, hero := d.AddTab(ws, "hero", dir)
	d.agent(hero, demostory.HeroWriter, "claude", herdr.StatusDone, "")
	d.idle(hero, "I need the homepage's first sentence from you before I go on.")
}

func (d *Demo) buildMobile() {
	dir := demostory.Dir(demostory.Mobile)
	ws, _, formKit := d.AddWorkspace(demostory.Mobile, dir)
	d.agent(formKit, demostory.FormKit, "claude", herdr.StatusIdle, "")
	d.idle(formKit, "API Error: Usage limit reached; resets at 14:00.")
	_, refresh := d.AddTab(ws, "inbox", dir)
	d.jobAgent(refresh, demostory.RefreshBuilder, "")
	d.working(refresh, "Adding pull-to-refresh to the inbox screen", 8)
	_, linter := d.AddTab(ws, "lint", dir)
	d.agent(linter, DemoLinter, "codex", herdr.StatusIdle, "")
	d.idle(linter, "No lint errors in 214 files.")
}

func (d *Demo) buildInfra() {
	_, _, devbox := d.AddWorkspace(demostory.Infra, demostory.Dir(demostory.Infra))
	d.jobAgent(devbox, demostory.DevboxBuilder, "")
}

func (d *Demo) buildHandbook() {
	_, _, publish := d.AddWorkspace(demostory.Handbook, demostory.Dir(demostory.Handbook))
	d.agent(publish, demostory.HandbookPublish, "claude", herdr.StatusBlocked, "e5b3a8f0-7d21-4c9e-b6f4-1a0c3e8d5f27")
	d.terminalDialog(publish, demoHandbookBuild, "Rebuild the handbook site from scratch")
}

// jobAgent adds a working Claude job agent on its first task.
func (d *Demo) jobAgent(paneID, name, session string) {
	d.agent(paneID, name, "claude", herdr.StatusWorking, session)
	if tasks := demoTasks[name]; len(tasks) > 0 {
		d.working(paneID, tasks[0], 42)
	}
}

func (d *Demo) agent(paneID, name, kind string, status herdr.AgentStatus, session string) {
	_ = d.AddAgent(paneID, name, kind, status, session)
	d.panes[name] = paneID
}

// PaneOf returns the pane id of a demo agent ("" for none).
func (d *Demo) PaneOf(name string) string { return d.panes[name] }

// DialogCommand returns the command of the terminal dialog on paneID now, or "" when there is
// none. A permission dialog an approval stands for (AwaitApproval) is not counted: the approval
// is what waits on the human there.
func (d *Demo) DialogCommand(paneID string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dialogs[paneID]
}

// ---- Approvals ----------------------------------------------------------------------------

// AwaitApproval shows the permission dialog a tool call waits at while its approval is pending
// (DESIGN.md 8.7): for Bash, subject is the command and detail its description; for Edit and
// Write, subject is the file; for anything else, subject is the call in one line.
func (d *Demo) AwaitApproval(paneID, tool, subject, detail string) {
	switch tool {
	case "Bash":
		d.drawDialog(paneID, "Bash("+subject+")", "Bash command", []string{subject, detail}, "Do you want to proceed?", "Yes, and don't ask again for this command")
	case "Edit", "Write", "MultiEdit":
		d.drawDialog(paneID, "Update("+subject+")", "Edit file", []string{subject}, "Do you want to make this edit to "+path.Base(subject)+"?", "Yes, allow all edits during this session (shift+tab)")
	default:
		d.drawDialog(paneID, tool, "Tool use", []string{subject}, "Do you want to proceed?", "Yes, and don't ask again for "+tool)
	}
	_ = d.SetStatus(paneID, herdr.StatusBlocked)
}

// ApprovalSettled is the agent's side of a decided approval: allowed, it runs the call; denied,
// it works on without it. Either way its job is running again.
func (d *Demo) ApprovalSettled(paneID string, allowed bool) {
	task := "Working on without that call, as you asked"
	if allowed {
		task = "Running the approved call"
	}
	d.working(paneID, task, 1)
	_ = d.SetStatus(paneID, herdr.StatusWorking)
}

// ---- The script ---------------------------------------------------------------------------

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
// waiting on a clock. Nothing here answers or removes what waits on the human: only a key press
// (answerDialog) or a decided approval (ApprovalSettled) does.
func (d *Demo) Step() {
	d.mu.Lock()
	d.step++
	n := d.step
	reblock := d.reblockAt != 0 && n >= d.reblockAt
	if reblock {
		d.reblockAt = 0
	}
	finished := map[string]demoTurn{}
	for pane, turn := range d.busy {
		if n >= turn.until {
			finished[pane] = turn
			delete(d.busy, pane)
		}
	}
	d.mu.Unlock()

	d.advanceTasks(n)
	if n%3 == 0 {
		d.toggle(d.panes[DemoLinter], "Checking 214 files", "No lint errors in 214 files.", n)
	}
	if reblock {
		pane := d.panes[demostory.HandbookPublish]
		d.terminalDialog(pane, demoHandbookPublish, "Publish the rebuilt handbook")
		_ = d.SetStatus(pane, herdr.StatusBlocked)
	}
	for pane, turn := range finished {
		if turn.resume != "" {
			d.working(pane, turn.resume, 0)
			_ = d.SetStatus(pane, herdr.StatusWorking)
			continue
		}
		d.idle(pane, "Done. That is handled.")
		_ = d.SetStatus(pane, herdr.StatusIdle)
	}
}

// advanceTasks moves each working job agent to its next progress line. An agent at a dialog, in
// a prompted turn or idle is left alone.
func (d *Demo) advanceTasks(n int) {
	for name, tasks := range demoTasks {
		pane := d.panes[name]
		agent, err := d.AgentGet(context.Background(), pane)
		d.mu.Lock()
		_, busy := d.busy[pane]
		d.mu.Unlock()
		if err != nil || busy || agent.AgentStatus != herdr.StatusWorking {
			continue
		}
		d.working(pane, tasks[n%len(tasks)], 5+n)
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

// answerDialog is the demo's KeysHook: a digit on a blocked agent answers its dialog. The
// Handbook job's next dialog arrives a few steps later, so there is always one to try.
func (d *Demo) answerDialog(f *Fake, paneID string, keys []string) {
	if len(keys) == 0 {
		return
	}
	agent, err := f.AgentGet(context.Background(), paneID)
	if err != nil || agent.AgentStatus != herdr.StatusBlocked {
		return
	}
	handbook := paneID == d.panes[demostory.HandbookPublish]
	switch keys[0] {
	case "1", "2":
		d.working(paneID, "Running the approved command", 1)
		_ = f.SetStatus(paneID, herdr.StatusWorking)
		if handbook {
			d.scheduleReblock(3)
		}
	case "3", "esc":
		d.idle(paneID, "Understood, I will not run that. What should I do instead?")
		_ = f.SetStatus(paneID, herdr.StatusIdle)
		if handbook {
			d.scheduleReblock(4)
		}
	}
}

func (d *Demo) scheduleReblock(steps int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reblockAt = d.step + steps
}

// startTurn is the demo's PromptHook: a prompted agent works for two steps, then goes back to
// what it was doing (a job agent mid-task) or idle.
func (d *Demo) startTurn(f *Fake, paneID, text string) {
	if d.scribe != nil && (paneID == d.panes[DemoOrchestrator] || isDemoLead(f, paneID)) {
		d.orchestratorTurn(paneID, text)
		return
	}
	d.mu.Lock()
	resume := d.tasks[paneID]
	if turn, ok := d.busy[paneID]; ok {
		resume = turn.resume // a second prompt mid-turn keeps the first one's way back
	}
	d.busy[paneID] = demoTurn{until: d.step + 2, resume: resume}
	d.mu.Unlock()
	d.working(paneID, "Working on: "+firstLine(text), 0)
	d.recordPrompt(paneID, text)
}

// recordPrompt appends what was typed to a job agent's transcript when it has one (a session), so
// its Conversation shows the message as Claude's own transcript would (a review's comments, a
// message from the job's page). Agents without a session are left alone.
func (d *Demo) recordPrompt(paneID, text string) {
	if d.scribe == nil {
		return
	}
	d.scribe.mu.Lock()
	defer d.scribe.mu.Unlock()
	if w, err := d.writerFor(paneID); err == nil {
		w.user(text)
	}
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if len(line) > 60 {
		line = line[:57] + "…"
	}
	return line
}

// ---- Screens ------------------------------------------------------------------------------

const esc = "\x1b"

var (
	demoRule   = esc + "[38;5;244m" + strings.Repeat("─", 100) + esc + "[39m"
	demoFooter = "  " + esc + "[2m⏵⏵ auto mode on (shift+tab to cycle)" + esc + "[22m"
	sgr        = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

func (d *Demo) setANSI(pane, ansi string) {
	_ = d.SetScreen(pane, sgr.ReplaceAllString(ansi, ""), ansi)
}

// note records what a pane shows now: the task a working agent is on, and the command of a
// terminal dialog. Any new screen replaces both.
func (d *Demo) note(pane, task, dialog string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tasks[pane] = task
	if dialog == "" {
		delete(d.dialogs, pane)
		return
	}
	d.dialogs[pane] = dialog
}

func (d *Demo) idle(pane, message string) {
	d.note(pane, "", "")
	d.setANSI(pane, fmt.Sprintf("%s[1m⏺%s[22m %s\n\n%s\n❯ %s[7m %s[27m%s[2mTry \"summarise what changed today\"%s[22m\n%s\n%s\n",
		esc, esc, message, demoRule, esc, esc, esc, esc, demoRule, demoFooter))
}

func (d *Demo) working(pane, task string, seconds int) {
	d.note(pane, task, "")
	d.setANSI(pane, fmt.Sprintf("%s[1m⏺%s[22m %s\n\n%s[38;5;174m✻%s[39m Working… (%ds · esc to interrupt)\n\n%s\n❯ %s[7m %s[27m\n%s\n%s\n",
		esc, esc, task, esc, esc, seconds, demoRule, esc, esc, demoRule, demoFooter))
}

// terminalDialog draws a Bash permission dialog that no approval stands for, and records its
// command for DialogCommand.
func (d *Demo) terminalDialog(pane, command, description string) {
	d.drawDialog(pane, "Bash("+command+")", "Bash command", []string{command, description}, "Do you want to proceed?", "Yes, and don't ask again for this command")
	d.note(pane, "", command)
}

// drawDialog draws a Claude permission dialog: the call, a titled box with its lines, the
// question and the three numbered options.
func (d *Demo) drawDialog(pane, call, title string, lines []string, question, always string) {
	d.note(pane, "", "")
	var body strings.Builder
	for _, line := range lines {
		if line != "" {
			body.WriteString("   " + line + "\n")
		}
	}
	d.setANSI(pane, fmt.Sprintf("%s[1m⏺%s[22m %s\n\n%s\n %s[1m%s%s[22m\n\n%s\n %s\n %s[36m❯ 1. Yes%s[39m\n   2. %s\n   3. No, and tell Claude what to do differently %s[2m(esc)%s[22m\n\n %s[2mEsc to cancel · Tab to amend%s[22m\n",
		esc, esc, call, demoRule, esc, title, esc, body.String(), question, esc, esc, always, esc, esc, esc, esc))
}

// isDemoLead reports whether the agent in paneID is a project's lead (DESIGN.md 21.2): leads
// answer prompts with canned replies, like the demo orchestrator, so Chat with a lead works.
func isDemoLead(f *Fake, paneID string) bool {
	agent, err := f.AgentGet(context.Background(), paneID)
	return err == nil && strings.HasPrefix(agent.Name, "lead-")
}
