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

	"github.com/jclement/crowsnest/internal/herdr"
)

// DefaultReplyDelay is how long the demo orchestrator "works" before it answers.
const DefaultReplyDelay = 2500 * time.Millisecond

// scribe appends records to demo transcripts.
type scribe struct {
	mu        sync.Mutex
	configDir string
	delay     time.Duration
	replies   int
}

// EnableTranscripts makes the demo keep transcripts under configDir (laid out like
// ~/.claude: projects/<cwd>/<session>.jsonl), seeds the orchestrator, builder and reviewer with
// a conversation, and makes the orchestrator answer prompts after delay (DefaultReplyDelay when
// zero). Point the transcript manager's config dir at the same directory.
func (d *Demo) EnableTranscripts(configDir string, delay time.Duration) error {
	if delay <= 0 {
		delay = DefaultReplyDelay
	}
	d.scribe = &scribe{configDir: configDir, delay: delay}
	start := time.Now().Add(-40 * time.Minute)
	for name, seed := range map[string]func(*transcriptWriter){
		DemoOrchestrator: seedOrchestrator, DemoBuilder: seedBuilder, DemoReviewer: seedReviewer,
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
	reply := cannedReplies[d.scribe.replies%len(cannedReplies)]
	d.scribe.replies++
	time.AfterFunc(d.scribe.delay, func() { d.finishTurn(paneID, text, reply) })
}

func (d *Demo) finishTurn(paneID, prompt string, reply cannedReply) {
	d.scribe.mu.Lock()
	w, err := d.writerFor(paneID)
	if err == nil {
		w.at = time.Now()
		w.tool(reply.tool, reply.input, reply.result, false)
		w.text(demoSay(prompt, reply) + strings.ReplaceAll(reply.markdown, "{{prompt}}", firstLine(promptBody(prompt))))
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
	w.user("What's the state of the crowsnest build?")
	w.thinking("Let me look at the fleet before answering, rather than guessing from memory.")
	w.tool("Bash", map[string]any{"command": "herdr agent list --json | jq -r '.[] | \"\\(.name) \\(.agent_status)\"'", "description": "List agents and their states"},
		"orchestrator working\nbuilder working\nreviewer done\ndocs-writer blocked\nlinter idle", false)
	w.text("Four agents besides me:\n\n| Agent | State | Doing |\n|---|---|---|\n| builder | working | wiring `/api/v1/fleet` |\n| reviewer | done | review written to `REVIEW.md` |\n| docs-writer | **blocked** | wants to run a command |\n| linter | idle | no lint errors |\n\nThe docs writer needs you; everything else is moving.")
	w.user("[crowsnest poke 2026-11-26T03:10Z talk=off]\n- docs-writer is blocked on a dialog (10 min)\n- reviewer settled: done")
	w.tool("Read", map[string]any{"file_path": "~/src/crowsnest/REVIEW.md"}, "# Review: fleet cache\n\n1. minor: debounce constant could be named\n2. minor: missing test for events_lost", false)
	w.text("The reviewer found **two minor issues** in the fleet cache; I've passed them to the builder.\n\nThe docs writer is still waiting on you: it wants to run\n\n```sh\nrm -rf dist && npm run build\n```\n\nAnswer it from **Fleet** (or the Inbox, once that exists).")
}

func seedBuilder(w *transcriptWriter) {
	w.user("Wire GET /api/v1/fleet to the fleet cache and add tests.")
	w.tool("Edit", map[string]any{"file_path": "internal/httpd/agents.go", "old_string": "// TODO fleet", "new_string": "func (s *Server) handleFleet(…)"}, "The file internal/httpd/agents.go has been updated.", false)
	w.tool("Bash", map[string]any{"command": "go test ./internal/httpd/... ./internal/fleet/...", "description": "Run the affected tests"},
		"ok  \tgithub.com/jclement/crowsnest/internal/httpd\t0.81s\nok  \tgithub.com/jclement/crowsnest/internal/fleet\t0.47s", false)
	w.text("`GET /api/v1/fleet` now serves the cached fleet; **7 tests** added, all passing. Next: the `fleet` SSE event.")
}

func seedReviewer(w *transcriptWriter) {
	w.user("Review the fleet cache on wip/m2-herdr. Findings to REVIEW.md.")
	w.tool("Bash", map[string]any{"command": "git diff main...wip/m2-herdr --stat"}, " internal/fleet/cache.go | 338 +++++\n internal/fleet/view.go  | 181 +++\n 2 files changed, 519 insertions(+)", false)
	w.text("Review written to `REVIEW.md`: **2 findings, both minor**.\n\n1. The 150 ms debounce is a bare number; name it.\n2. No test covers `events_lost` resubscription.")
}

// cannedReply is one scripted answer of the demo orchestrator.
type cannedReply struct {
	tool     string
	input    map[string]any
	result   string
	markdown string
	say      string // the SAY line used when talk mode is on (DESIGN.md 7.1)
}

// demoSay follows the SAY contract the way a real orchestrator is told to: a prompt whose header
// says talk=on gets a spoken first line, so talk mode can be exercised without Claude.
func demoSay(prompt string, reply cannedReply) string {
	header, _, _ := strings.Cut(prompt, "\n") // not firstLine: it shortens the line, and the flag is at its end
	if !strings.HasPrefix(header, "[crowsnest ") || !strings.Contains(header, "talk=on") {
		return ""
	}
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
		say:      "M1 to M3 are merged and nothing needs you except one dialog.",
		markdown: "On **{{prompt}}**: here is where things stand.\n\n- M1 and M3 are merged; M2 merged an hour ago.\n- The builder is on the fleet SSE event.\n- Nothing is waiting on you except the docs writer's dialog.\n\nWant me to start the next milestone?",
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
