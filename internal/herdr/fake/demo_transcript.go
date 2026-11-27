// The demo's transcripts and scripted orchestrator, so the Chat view works under --demo.
//
// The demo writes real Claude-shaped JSONL files (the same record shapes the golden fixtures in
// internal/transcript pin) into a directory laid out like Claude's config dir, and crowsnest's
// ordinary transcript tailer reads them. So --demo exercises the real tail-and-normalise path end
// to end rather than a stand-in. A prompt to the demo orchestrator is answered after a short
// "working" period with canned markdown, a tool card first.

package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jclement/crowsnest/internal/demostory"
	"github.com/jclement/crowsnest/internal/herdr"
)

// DefaultReplyDelay is how long the demo orchestrator "works" before it answers.
const DefaultReplyDelay = 2500 * time.Millisecond

// scribe appends records to demo transcripts.
type scribe struct {
	mu        sync.Mutex
	configDir string
	delay     time.Duration
	// replies counts the human's prompts per pane: each conversation walks the canned replies in
	// its own order, and text crowsnest typed (a brief, a poke, an answer: "[crowsnest …") gets
	// the first reply without moving the rotation, so a dispatch or a lead's turn never changes
	// what the human's next prompt is answered with.
	replies map[string]int
}

// EnableTranscripts makes the demo keep transcripts under configDir (laid out like
// ~/.claude: projects/<cwd>/<session>.jsonl), seeds the orchestrator, the webhook builder and the
// fleet cache reviewer with a conversation, and makes the orchestrator answer prompts after delay
// (DefaultReplyDelay when zero). Point the transcript manager's config dir at the same directory.
func (d *Demo) EnableTranscripts(configDir string, delay time.Duration) error {
	if delay <= 0 {
		delay = DefaultReplyDelay
	}
	d.scribe = &scribe{configDir: configDir, delay: delay, replies: map[string]int{}}
	start := time.Now().Add(-40 * time.Minute)
	for name, seed := range map[string]func(*transcriptWriter){
		DemoOrchestrator: seedOrchestrator, demostory.WebhookBuilder: seedBuilder, demostory.CacheReviewer: seedReviewer,
	} {
		w, err := d.writerFor(d.panes[name])
		if err != nil {
			return err
		}
		// Start from a clean file so a restarted demo does not seed the same turns twice.
		if err := os.Remove(w.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("clearing the demo %s transcript: %w", name, err)
		}
		w.at = start
		seed(w)
		if w.err != nil {
			return fmt.Errorf("seeding the %s transcript: %w", name, w.err)
		}
	}
	return nil
}

// writerFor opens a writer on the current transcript of the agent in paneID.
func (d *Demo) writerFor(paneID string) (*transcriptWriter, error) {
	agent, err := d.AgentGet(context.Background(), paneID)
	if err != nil || agent.AgentSession == nil {
		return nil, fmt.Errorf("demo agent in %s has no session: %w", paneID, err)
	}
	dir := filepath.Join(d.scribe.configDir, "projects", projectDirName(agent.Cwd))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating demo transcript directory: %w", err)
	}
	return &transcriptWriter{path: filepath.Join(dir, agent.AgentSession.Value+".jsonl"), session: agent.AgentSession.Value, cwd: agent.Cwd, at: time.Now()}, nil
}

// projectDirName mirrors transcript.ProjectDirName (not imported: transcript is above herdr).
func projectDirName(cwd string) string {
	out := []byte(cwd)
	for i, c := range out {
		if !isASCIIAlphanumeric(c) {
			out[i] = '-'
		}
	}
	return string(out)
}

// orchestratorTurn records the prompt, shows the orchestrator working, and answers after the
// delay.
func (d *Demo) orchestratorTurn(paneID, text string) {
	d.working(paneID, "Working on: "+firstLine(promptBody(text)), 0)
	d.scribe.mu.Lock()
	defer d.scribe.mu.Unlock()
	w, err := d.writerFor(paneID)
	if err != nil {
		return
	}
	w.user(text)
	reply := cannedReplies[0]
	if !strings.HasPrefix(strings.TrimSpace(text), "[crowsnest ") || strings.HasPrefix(strings.TrimSpace(text), "[crowsnest voice") {
		reply = cannedReplies[d.scribe.replies[paneID]%len(cannedReplies)]
		d.scribe.replies[paneID]++
	}
	time.AfterFunc(d.scribe.delay, func() { d.finishTurn(paneID, text, reply) })
}

func (d *Demo) finishTurn(paneID, prompt string, reply cannedReply) {
	d.scribe.mu.Lock()
	w, err := d.writerFor(paneID)
	if err == nil {
		w.at = time.Now()
		w.tool(reply.tool, reply.input, reply.result, false)
		w.text(demoSay(reply) + strings.ReplaceAll(reply.markdown, "{{prompt}}", firstLine(promptBody(prompt))))
	}
	d.scribe.mu.Unlock()
	d.idle(paneID, "Answered: "+firstLine(promptBody(prompt)))
	_ = d.SetStatus(paneID, herdr.StatusIdle)
}

// ---- Writing records -------------------------------------------------------------------

// transcriptWriter appends Claude-shaped records to one transcript file. The first error sticks
// and stops further writes.
type transcriptWriter struct {
	path, session, cwd string
	at                 time.Time
	err                error
}

func (w *transcriptWriter) write(record map[string]any) {
	if w.err != nil {
		return
	}
	w.at = w.at.Add(1500 * time.Millisecond)
	record["uuid"], record["timestamp"], record["sessionId"], record["cwd"] = uuid.NewString(), w.at.UTC().Format(time.RFC3339Nano), w.session, w.cwd
	record["isSidechain"] = false
	line, err := json.Marshal(record)
	if err != nil {
		w.err = err
		return
	}
	file, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		w.err = err
		return
	}
	defer func() { _ = file.Close() }()
	_, w.err = file.Write(append(line, '\n'))
}

func (w *transcriptWriter) user(text string) {
	w.write(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
}

func (w *transcriptWriter) assistant(block map[string]any) {
	w.write(map[string]any{"type": "assistant", "message": map[string]any{
		"id": "msg_demo_" + uuid.NewString()[:8], "role": "assistant", "model": "claude-demo", "content": []any{block},
	}})
}

func (w *transcriptWriter) text(markdown string) {
	w.assistant(map[string]any{"type": "text", "text": markdown})
}

func (w *transcriptWriter) thinking(text string) {
	w.assistant(map[string]any{"type": "thinking", "thinking": text})
}

func (w *transcriptWriter) tool(name string, input map[string]any, result string, isError bool) {
	id := "toolu_demo_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	w.assistant(map[string]any{"type": "tool_use", "id": id, "name": name, "input": input})
	w.write(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "content": result, "is_error": isError},
	}}})
}

// ---- The scripts -----------------------------------------------------------------------

func seedOrchestrator(w *transcriptWriter) {
	w.user("What's waiting on me this morning?")
	w.thinking("Let me look at the fleet and the jobs before answering, rather than guessing from memory.")
	w.tool("Bash", map[string]any{"command": "crowsnest jobs --live --json | jq -r '.[] | \"\\(.id) \\(.project) \\(.state) \\(.agent_name)\"'", "description": "List the live jobs"},
		"j_1 crowsnest running job-1\nj_11 crowsnest needs_you job-11\nj_13 crowsnest needs_you job-13\nj_15 website needs_you job-15\nj_3 mobile running(stuck) job-3\nj_5 handbook needs_you job-5\nj_17 infra running job-17", false)
	w.text("SAY: Four things need you: a force push, a question about the homepage, the handbook's dialog, and Mobile is out of usage.\n\n" +
		"| Project | What waits on you |\n|---|---|\n| Crowsnest | **j_13** wants to `git push --force origin main` (dangerous), and **j_11** asks whether to apply the fleet cache refactor |\n" +
		"| Website | **j_15** needs the homepage's first sentence before it goes on |\n| Handbook | **j_5** is at a dialog: `rm -rf dist && npm run build` |\n" +
		"| Mobile | **j_3** hit the usage limit; it retries on its own at 14:00 |\n\nInfra is quiet: **j_17** is rebuilding the devbox image.")
	w.user("[crowsnest poke 2026-11-26T03:10Z]\n- job-5 is blocked on a dialog (10 min)\n- j_12 asked q_2: Which findings should the builder fix before merge?")
	w.tool("Read", map[string]any{"file_path": "~/src/crowsnest/REVIEW.md"}, "# Review: fleet cache\n\n1. race: Publish reads subscribers without the lock\n2. leak: the SSE subscription is not closed\n3. names: handleX helpers\n4. docs: the 150 ms window", false)
	w.text("SAY: The cache review found four things, and the handbook job still needs you.\n\nThe reviewer (**j_12**) found **four issues** in the fleet cache and asked you which the builder should fix before merge.\n\nThe handbook job is still waiting on you: it wants to run\n\n```sh\nrm -rf dist && npm run build\n```\n\nAnswer it from **Now** or the Inbox.")
}

func seedBuilder(w *transcriptWriter) {
	w.user("[crowsnest job j_1] Add retry back-off to the webhook sender\n\nExponential, capped at 5 minutes, with jitter. Tests for the schedule.")
	w.tool("Read", map[string]any{"file_path": "internal/webhook/sender.go"}, "func (s *Sender) deliver(ctx context.Context, hook Hook) error {\n\t// TODO retry\n\treturn s.post(ctx, hook)\n}", false)
	w.tool("Edit", map[string]any{"file_path": "internal/webhook/backoff.go", "old_string": "", "new_string": "func backoff(attempt int) time.Duration {…}"}, "The file internal/webhook/backoff.go has been created.", false)
	w.tool("Bash", map[string]any{"command": "go test ./internal/webhook/ -run Backoff", "description": "Run the back-off tests"},
		"ok  \tgithub.com/jclement/crowsnest/internal/webhook\t0.21s", false)
	w.text("The back-off is in: doubling from 1 s, **capped at 5 minutes**, with ±20 % jitter. Writing table tests for the schedule next.")
}

func seedReviewer(w *transcriptWriter) {
	w.user("[crowsnest job j_12] Review the fleet cache refactor on wip/fleet-cache. Findings to REVIEW.md, then ask which to fix.")
	w.tool("Bash", map[string]any{"command": "git diff main...wip/fleet-cache --stat"}, " internal/fleet/cache.go | 64 +++++---\n internal/hub/hub.go     | 12 ++-\n 2 files changed, 52 insertions(+), 24 deletions(-)", false)
	w.text("Review written to `REVIEW.md`: **4 findings**, one serious.\n\n1. Data race: `Publish` reads subscribers without the lock.\n2. Goroutine leak on SSE close.\n3. The `handleX` helpers want names.\n4. The 150 ms window is undocumented.\n\nI've asked which the builder should fix before merge.")
}

// cannedReply is one scripted answer of the demo orchestrator.
type cannedReply struct {
	tool     string
	input    map[string]any
	result   string
	markdown string
	say      string // the reply's SAY line (DESIGN.md 7.1)
}

// demoSay follows the SAY contract the way a real orchestrator is told to: every reply opens with
// a spoken first line, so speak mode can be exercised without Claude.
func demoSay(reply cannedReply) string {
	return "SAY: " + reply.say + "\n\n"
}

// promptBody drops crowsnest's header line (voice, poke) so the reply quotes what was said.
func promptBody(prompt string) string {
	if strings.HasPrefix(prompt, "[crowsnest ") {
		if _, rest, ok := strings.Cut(prompt, "\n"); ok && strings.TrimSpace(rest) != "" {
			return rest
		}
	}
	return prompt
}

var cannedReplies = []cannedReply{
	{
		tool: "Bash", input: map[string]any{"command": "git log --oneline -5", "description": "Recent commits"},
		result:   "a26b6f1 Merge branch 'wip/m2-herdr'\n4252245 M2: README and DESIGN\n8e1218b STATUS: M1 and M3 merged",
		say:      "M1 to M3 are merged; four things wait on you.",
		markdown: "On **{{prompt}}**: here is where things stand.\n\n- M1 and M3 are merged; M2 merged an hour ago.\n- **j_1** is on the webhook back-off, in its worktree.\n- Waiting on you: the force push in Crowsnest, the homepage question in Website, the handbook's dialog, and Mobile's usage limit.\n\nWant me to start the next milestone?",
	},
	{
		tool: "Read", input: map[string]any{"file_path": "DESIGN.md"},
		result:   "## 7. The orchestrator\n\nThe orchestrator is the session's standing lead…",
		say:      "Yes, with one caveat about drafts.",
		markdown: "I checked `DESIGN.md` for that. Short answer: **yes**, with one caveat.\n\n```go\n// the send path refuses to type over a draft\nif draft.Present && !force {\n\treturn &DraftError{Draft: draft}\n}\n```\n\nThe caveat: a draft is only detected when the input box is visible.",
	},
	{
		tool: "Grep", input: map[string]any{"pattern": "TODO", "path": "internal"},
		result:   "internal/fleet/cache.go:142: // TODO: name the debounce\ninternal/httpd/sse.go:61: // TODO: replay",
		say:      "Two small TODOs are left, and neither blocks you.",
		markdown: "Two TODOs left in `internal/`:\n\n| File | Line | Note |\n|---|---|---|\n| `fleet/cache.go` | 142 | name the debounce |\n| `httpd/sse.go` | 61 | replay |\n\nNeither blocks **{{prompt}}**.",
	},
}

func isASCIIAlphanumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
