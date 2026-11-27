package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexUsageFromARollout(t *testing.T) {
	cursor := FileCursor{Path: filepath.Join("testdata", "codex", "rollout.jsonl"), Format: FormatCodex}
	var entries []UsageEntry
	if _, err := ReadUsage(&cursor, func(entry UsageEntry) { entries = append(entries, entry) }); err != nil {
		t.Fatal(err)
	}
	// The repeated report has the same id (the running total), so a meter counts it once.
	if len(entries) != 3 || entries[0].MessageID != entries[1].MessageID || entries[2].MessageID != "codex:25100" {
		t.Fatalf("entries = %+v", entries)
	}
	first := entries[0]
	if first.Model != "gpt-5.6-luna" || first.Input != 3000 || first.CacheRead != 9000 || first.Output != 40 {
		t.Errorf("first call = %+v (fresh input is input minus cached)", first)
	}
	if second := entries[2]; second.Input != 2000 || second.CacheRead != 11000 || second.Output != 60 {
		t.Errorf("second call = %+v", second)
	}
	if cursor.Model != "gpt-5.6-luna" {
		t.Errorf("the cursor should remember the model for a resumed read: %+v", cursor)
	}
	// Nothing new: nothing read again.
	again := 0
	if _, err := ReadUsage(&cursor, func(UsageEntry) { again++ }); err != nil || again != 0 {
		t.Errorf("a second read found %d entries, %v", again, err)
	}
}

func TestLocateCodex(t *testing.T) {
	home := t.TempDir()
	id := "01a21eb7-b939-7662-a41f-264c5cf9bd20"
	dir := filepath.Join(home, "sessions", "2026", "11", "27")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "rollout-2026-11-27T14-34-46-"+id+".jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LocateCodex(home, id); err != nil || got != want {
		t.Errorf("LocateCodex = %q, %v", got, err)
	}
	if _, err := LocateCodex(home, "../../etc"); err == nil {
		t.Error("a malformed id must be refused")
	}
}
