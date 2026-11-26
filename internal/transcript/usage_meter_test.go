package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ParseUsage reads the fields spend needs and refuses everything else.
func TestParseUsage(t *testing.T) {
	entry, ok := ParseUsage([]byte(`{"type":"assistant","isSidechain":true,"timestamp":"2026-11-26T00:00:00Z","message":{"id":"m1","model":"claude-opus-5-5",` +
		`"content":[{"type":"tool_use","id":"t1"},{"type":"text","text":"x"},{"type":"tool_use","id":"t2"}],` +
		`"usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":30,"cache_read_input_tokens":4,` +
		`"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":20},"speed":"fast","inference_geo":"us"}}}`))
	if !ok || entry.MessageID != "m1" || entry.Input != 1 || entry.Output != 2 || entry.CacheWrite5m != 10 || entry.CacheWrite1h != 20 ||
		entry.CacheRead != 4 || entry.Speed != "fast" || entry.Geo != "us" || !entry.Sidechain || strings.Join(entry.ToolUseIDs, ",") != "t1,t2" || entry.Prompt() != 35 {
		t.Fatalf("parsed %+v %v", entry, ok)
	}
	// Without the split, every cache write counts as 5-minute.
	entry, _ = ParseUsage([]byte(`{"type":"assistant","message":{"id":"m2","model":"x","content":[],"usage":{"cache_creation_input_tokens":7,"speed":"standard"}}}`))
	if entry.CacheWrite5m != 7 || entry.CacheWrite1h != 0 || entry.Speed != "" {
		t.Fatalf("no split: %+v", entry)
	}
	for _, line := range []string{
		`{"type":"user","message":{"id":"m","model":"x","usage":{"input_tokens":1}}}`,
		`{"type":"assistant","message":{"id":"m","model":"<synthetic>","usage":{"input_tokens":1}}}`,
		`{"type":"assistant","message":{"model":"x","usage":{"input_tokens":1}}}`,
		`{"type":"assistant","message":{"id":"m","model":"x"}}`,
		`not json "usage"`,
		``,
	} {
		if _, ok := ParseUsage([]byte(line)); ok {
			t.Errorf("accepted %q", line)
		}
	}
}

// ReadUsage resumes from its cursor, leaves an unfinished line for later, skips an absurd line,
// starts over when the file is replaced, and says when the file is gone.
func TestReadUsageCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	line := func(id string) string {
		return `{"type":"assistant","message":{"id":"` + id + `","model":"m","content":[],"usage":{"input_tokens":1}}}` + "\n"
	}
	write := func(text string, flag int) {
		t.Helper()
		file, err := os.OpenFile(path, flag|os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(text); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
	}
	var seen []string
	collect := func(e UsageEntry) { seen = append(seen, e.MessageID) }
	cursor := FileCursor{Path: path}

	whole := line("a")
	write(whole+line("b")[:20], os.O_TRUNC)
	if _, err := ReadUsage(&cursor, collect); err != nil || strings.Join(seen, ",") != "a" || cursor.Offset != int64(len(whole)) {
		t.Fatalf("first read: %v %v %d", err, seen, cursor.Offset)
	}
	write(line("b")[20:]+strings.Repeat("x", maxLineBytes+10)+"\n"+line("c"), os.O_APPEND)
	if _, err := ReadUsage(&cursor, collect); err != nil || strings.Join(seen, ",") != "a,b,c" {
		t.Fatalf("second read: %v %v", err, seen)
	}
	info, _ := os.Stat(path)
	if cursor.Offset != info.Size() {
		t.Fatalf("offset %d, size %d", cursor.Offset, info.Size())
	}
	// Replaced by a new file (another inode): read from the top.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	write(line("d"), os.O_TRUNC)
	reset, err := ReadUsage(&cursor, collect)
	if err != nil || !reset || seen[len(seen)-1] != "d" {
		t.Fatalf("replaced: %v %v %v", reset, err, seen)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadUsage(&cursor, collect); !errors.Is(err, ErrUsageFileGone) {
		t.Fatalf("gone: %v", err)
	}
}

func TestSubagentFiles(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s1.jsonl")
	if err := os.MkdirAll(filepath.Join(dir, "s1", "subagents"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agent-b.jsonl", "agent-a.jsonl", "agent-a.meta.json"} {
		if err := os.WriteFile(filepath.Join(dir, "s1", "subagents", name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := SubagentFiles(session)
	if len(got) != 2 || filepath.Base(got[0]) != "agent-a.jsonl" {
		t.Fatalf("subagents: %v", got)
	}
	if SubagentFiles(filepath.Join(dir, "none.jsonl")) != nil {
		t.Fatal("found subagents of a missing session")
	}
}
