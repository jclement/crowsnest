package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The context size follows the latest call's usage, and a compaction boundary resets it: to
// postTokens when recorded, to unknown when not (Claude Code 2.1.332 records none for auto
// compaction), until the next call reports again. The human's last message ignores what
// crowsnest typed and the compaction summary.
func TestUsageTracking(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-11-26T10:00:00Z","message":{"content":"hello from Jeff"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-26T10:00:05Z","message":{"id":"m1","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":2,"cache_creation_input_tokens":1000,"cache_read_input_tokens":150000,"output_tokens":5}}}`,
		`{"type":"assistant","uuid":"s1","isSidechain":true,"timestamp":"2026-11-26T10:00:06Z","message":{"id":"m9","content":[],"usage":{"input_tokens":999999}}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-11-26T10:05:00Z","message":{"content":"[crowsnest poke 2026-11-26T10:05:00Z talk=off]\n- x"}}`,
		`{"type":"user","uuid":"u3","timestamp":"2026-11-26T10:06:00Z","message":{"content":"[crowsnest answer q_1] \"yes\""}}`,
		`{"type":"user","uuid":"u4","timestamp":"2026-11-26T10:07:00Z","message":{"content":"<command-name>/compact</command-name>"}}`,
	}
	conv := NewConversation(SourceTranscript, "s")
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write := func(more ...string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(strings.Join(append(lines, more...), "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, more...)
	}
	write()
	tailer := NewTailer(path, "s", conv)
	if _, err := tailer.Poll(); err != nil {
		t.Fatal(err)
	}
	usage := conv.Usage()
	if !usage.Known || usage.Tokens != 151002 || !usage.HumanAt.Equal(time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("before compaction: %+v", usage)
	}
	write(`{"type":"system","subtype":"compact_boundary","timestamp":"2026-11-26T10:08:00Z","compactMetadata":{"trigger":"manual","preTokens":151002}}`,
		`{"type":"user","uuid":"u5","isCompactSummary":true,"timestamp":"2026-11-26T10:08:01Z","message":{"content":"This session is being continued"}}`)
	if _, err := tailer.Poll(); err != nil {
		t.Fatal(err)
	}
	usage = conv.Usage()
	if usage.Known || usage.CompactedAt.IsZero() || !usage.HumanAt.Equal(time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("after a compaction without postTokens: %+v", usage)
	}
	write(`{"type":"system","subtype":"compact_boundary","timestamp":"2026-11-26T10:09:00Z","compactMetadata":{"postTokens":21000}}`,
		`{"type":"user","uuid":"u6","timestamp":"2026-11-26T10:10:00Z","message":{"content":"[crowsnest voice: transcribed speech, may contain mis-hearings] talk=on\nstatus?"}}`)
	if _, err := tailer.Poll(); err != nil {
		t.Fatal(err)
	}
	usage = conv.Usage()
	if !usage.Known || usage.Tokens != 21000 || !usage.HumanAt.Equal(time.Date(2026, 11, 26, 10, 10, 0, 0, time.UTC)) {
		t.Fatalf("after a compaction with postTokens and a voice message: %+v", usage)
	}
}
