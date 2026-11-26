// Golden tests for the normaliser over real transcripts (testdata/*.jsonl, generated with Claude
// Code 2.1.332 and scrubbed: non-conversation records reduced to their type, paths replaced),
// plus table tests for the shapes the real ones do not happen to contain.
//
// Regenerate the goldens after an intended change with: go test ./internal/transcript -update

package transcript

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// normaliseFile runs a whole fixture through a Conversation, as the tailer would.
func normaliseFile(t *testing.T, path string) []Message {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	conv := NewConversation(SourceTranscript, "test")
	norm := NewNormaliser()
	for _, line := range strings.Split(string(data), "\n") {
		conv.Apply(norm.Line([]byte(line)))
	}
	return conv.Page("", MaxPageLimit).Messages
}

func TestGoldenTranscripts(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/*.jsonl")
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, fixture := range fixtures {
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			got, err := json.MarshalIndent(normaliseFile(t, fixture), "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			golden := strings.TrimSuffix(fixture, ".jsonl") + ".golden.json"
			if *update {
				if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden (run with -update): %v", err)
			}
			if strings.TrimSpace(string(want)) != strings.TrimSpace(string(got)) {
				t.Errorf("normalised %s differs from %s; run with -update if intended\n--- got\n%s", fixture, golden, got)
			}
		})
	}
}

// TestGoldenShapes pins the facts the goldens are meant to show, so a careless -update that
// broke them would still fail.
func TestGoldenShapes(t *testing.T) {
	tools := normaliseFile(t, "testdata/tool_calls.jsonl")
	kinds := []Kind{}
	for _, msg := range tools {
		kinds = append(kinds, msg.Kind)
	}
	want := []Kind{KindUser, KindTool, KindAssistant, KindTool, KindAssistant}
	if !equalKinds(kinds, want) {
		t.Fatalf("tool_calls kinds = %v, want %v", kinds, want)
	}
	bash := tools[1].Tool
	if bash.Name != "Bash" || bash.Summary != "ls" || !bash.Done || bash.IsError {
		t.Errorf("bash card = %+v", bash)
	}
	read := tools[3].Tool
	if read.Name != "Read" || !strings.HasSuffix(read.Summary, "notes.txt") || !strings.Contains(read.Result, "alpha") {
		t.Errorf("read card = %+v", read)
	}

	failed := normaliseFile(t, "testdata/tool_error.jsonl")
	var errorCard *ToolCall
	for _, msg := range failed {
		if msg.Kind == KindTool {
			errorCard = msg.Tool
		}
	}
	if errorCard == nil || !errorCard.IsError || strings.Contains(errorCard.Result, "\x1b") {
		t.Errorf("error card = %+v (want is_error and no ANSI)", errorCard)
	}

	reply := normaliseFile(t, "testdata/markdown_reply.jsonl")
	if len(reply) != 2 || reply[1].Kind != KindAssistant || !strings.Contains(reply[1].Text, "```go") {
		t.Errorf("markdown reply = %+v", reply)
	}
}

func equalKinds(a, b []Kind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNormaliserShapes(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		check func(t *testing.T, msgs []Message)
	}{
		{
			name: "crowsnest headers tag user messages",
			lines: []string{
				`{"type":"user","uuid":"u1","message":{"role":"user","content":"[crowsnest poke 2026-11-26T03:10Z talk=off]\n- builder settled"}}`,
				`{"type":"user","uuid":"u2","message":{"role":"user","content":"[crowsnest voice: transcribed speech, may contain mis-hearings] hello"}}`,
				`{"type":"user","uuid":"u3","message":{"role":"user","content":"[crowsnest answer q_7] Ship it? → yes"}}`,
				`{"type":"user","uuid":"u4","message":{"role":"user","content":"plain"}}`,
			},
			check: func(t *testing.T, msgs []Message) {
				tags := []string{msgs[0].Tag, msgs[1].Tag, msgs[2].Tag, msgs[3].Tag}
				if strings.Join(tags, ",") != "poke,voice,answer," {
					t.Errorf("tags = %q", tags)
				}
			},
		},
		{
			name: "meta, sidechain, unknown types and junk are skipped",
			lines: []string{
				`{"type":"user","uuid":"m","isMeta":true,"message":{"role":"user","content":"Caveat: injected"}}`,
				`{"type":"assistant","uuid":"s","isSidechain":true,"message":{"id":"x","content":[{"type":"text","text":"subagent"}]}}`,
				`{"type":"attachment","uuid":"a","attachment":{"content":"hook output"}}`,
				`{"type":"brand-new-record","message":{"content":42}}`,
				`not json at all`,
				`{"type":"assistant","uuid":"b","message":{"id":"y","content":"a string, not blocks"}}`,
				`{"type":"assistant","uuid":"c","message":{"id":"z","content":[{"type":"mystery_block","x":1}]}}`,
				`{"type":"user","uuid":"ok","message":{"role":"user","content":"survivor"}}`,
			},
			check: func(t *testing.T, msgs []Message) {
				if len(msgs) != 1 || msgs[0].Text != "survivor" {
					t.Errorf("messages = %+v", msgs)
				}
			},
		},
		{
			name: "text blocks of one reply merge; a tool call splits them",
			lines: []string{
				`{"type":"assistant","uuid":"a1","message":{"id":"msg1","content":[{"type":"text","text":"First."}]}}`,
				`{"type":"assistant","uuid":"a2","message":{"id":"msg1","content":[{"type":"text","text":"Second."}]}}`,
				`{"type":"assistant","uuid":"a3","message":{"id":"msg1","content":[{"type":"tool_use","id":"t1","name":"Grep","input":{"pattern":"TODO"}}]}}`,
				`{"type":"assistant","uuid":"a4","message":{"id":"msg1","content":[{"type":"text","text":"Third."}]}}`,
			},
			check: func(t *testing.T, msgs []Message) {
				if len(msgs) != 3 || msgs[0].Text != "First.\n\nSecond." || msgs[1].Tool.Summary != "TODO" || msgs[2].Text != "Third." {
					t.Errorf("messages = %+v", msgs)
				}
			},
		},
		{
			name: "tool_result with block content, and a result for an unseen call",
			lines: []string{
				`{"type":"assistant","uuid":"a1","message":{"id":"m","content":[{"type":"tool_use","id":"t1","name":"mcp__x__y","input":{"name":"z"}}]}}`,
				`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"line one"},{"type":"image"}]}]}}`,
				`{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"ghost","content":"orphan"}]}}`,
			},
			check: func(t *testing.T, msgs []Message) {
				if len(msgs) != 2 || msgs[0].Tool.Result != "line one\n[image]" || !msgs[0].Tool.Done || msgs[0].Tool.Summary != "z" {
					t.Fatalf("messages = %+v", msgs)
				}
				if msgs[1].Tool.Name != "tool" || msgs[1].Tool.Result != "orphan" {
					t.Errorf("orphan = %+v", msgs[1].Tool)
				}
			},
		},
		{
			name: "slash commands and their output",
			lines: []string{
				`{"type":"user","uuid":"c1","message":{"role":"user","content":"<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args>haiku</command-args>"}}`,
				`{"type":"user","uuid":"c2","message":{"role":"user","content":"<local-command-stdout>Set model to haiku</local-command-stdout>"}}`,
				`{"type":"user","uuid":"c3","message":{"role":"user","content":"<local-command-stdout></local-command-stdout>"}}`,
			},
			check: func(t *testing.T, msgs []Message) {
				if len(msgs) != 2 || msgs[0].Text != "/model haiku" || msgs[0].Tag != TagCommand || msgs[1].Text != "Set model to haiku" {
					t.Errorf("messages = %+v", msgs)
				}
			},
		},
		{
			name: "thinking with text is kept, redacted thinking is not",
			lines: []string{
				`{"type":"assistant","uuid":"t1","message":{"id":"m","content":[{"type":"thinking","thinking":"","signature":"abc"}]}}`,
				`{"type":"assistant","uuid":"t2","message":{"id":"m","content":[{"type":"thinking","thinking":"Let me check the tests."}]}}`,
			},
			check: func(t *testing.T, msgs []Message) {
				if len(msgs) != 1 || msgs[0].Kind != KindThinking {
					t.Errorf("messages = %+v", msgs)
				}
			},
		},
		{
			name: "huge results are truncated and marked",
			lines: []string{
				`{"type":"assistant","uuid":"a","message":{"id":"m","content":[{"type":"tool_use","id":"t","name":"Bash","input":{"command":"yes | head -100000"}}]}}`,
				`{"type":"user","uuid":"u","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"` + strings.Repeat("y\\n", 10000) + `"}]}}`,
			},
			check: func(t *testing.T, msgs []Message) {
				if !msgs[0].Tool.ResultTruncated || len(msgs[0].Tool.Result) > maxResultBytes {
					t.Errorf("result len %d truncated %v", len(msgs[0].Tool.Result), msgs[0].Tool.ResultTruncated)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conv := NewConversation(SourceTranscript, "s")
			norm := NewNormaliser()
			for _, line := range test.lines {
				conv.Apply(norm.Line([]byte(line)))
			}
			test.check(t, conv.Page("", MaxPageLimit).Messages)
		})
	}
}
