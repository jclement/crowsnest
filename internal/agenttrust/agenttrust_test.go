package agenttrust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func TestClaudeTrustKeepsEverythingElse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	original := `{"numStartups": 12345678901234567890, "tip": "<a & b>", "projects": {"/other": {"allowedTools": ["x"], "hasTrustDialogAccepted": false}, "/work/app": {"lastCost": 1.5}}, "oauthAccount": {"k": "v"}}`
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	results := Trust(Paths{ClaudeConfig: path, CodexConfig: filepath.Join(dir, "codex.toml")}, []string{"/work/app", "/work/new"}, false)
	if results[0].Status != StatusTrusted {
		t.Fatalf("claude: %+v", results[0])
	}
	if results[1].Status != StatusSkipped {
		t.Fatalf("codex not installed: %+v", results[1])
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	// Large integers and HTML-ish strings are carried verbatim, not reinterpreted.
	for _, want := range []string{"12345678901234567890", `"<a & b>"`, `"allowedTools": [`, `"lastCost": 1.5`, `"oauthAccount"`} {
		if !strings.Contains(text, want) {
			t.Errorf("lost %s from:\n%s", want, text)
		}
	}
	var parsed struct {
		Projects map[string]map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Projects["/work/app"]["hasTrustDialogAccepted"] != true || parsed.Projects["/work/new"]["hasTrustDialogAccepted"] != true {
		t.Errorf("not trusted: %+v", parsed.Projects)
	}
	if parsed.Projects["/other"]["hasTrustDialogAccepted"] != false {
		t.Errorf("another folder's answer changed: %+v", parsed.Projects["/other"])
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want the old 0640", info.Mode().Perm())
	}
	// A second call changes nothing.
	if again := Trust(Paths{ClaudeConfig: path}, []string{"/work/app"}, false); again[0].Status != StatusAlready {
		t.Errorf("again: %+v", again[0])
	}
}

func TestClaudeTrustRefusesABrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(`{"projects": [1, 2]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := Trust(Paths{ClaudeConfig: path}, []string{"/x"}, false)[0]; result.Status != StatusFailed {
		t.Fatalf("broken projects: %+v", result)
	}
	if data, _ := os.ReadFile(path); string(data) != `{"projects": [1, 2]}` {
		t.Errorf("the file was changed: %s", data)
	}
}

func TestClaudeTrustCreatesAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if result := Trust(Paths{ClaudeConfig: path}, []string{"/x"}, false)[0]; result.Status != StatusTrusted {
		t.Fatalf("missing file: %+v", result)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("a new file must be 0600, is %v", info.Mode().Perm())
	}
}

func TestCodexTrustAppendsAndRespectsAnExplicitNo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	original := "# my settings\nmodel = \"gpt-x\"\n\n[projects.\"/old\"]\ntrust_level = \"trusted\"\n\n[projects.\"/declined\"]\ntrust_level = \"untrusted\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := Paths{ClaudeConfig: filepath.Join(dir, ".claude.json"), CodexConfig: path}
	results := Trust(paths, []string{"/work/app"}, true)
	if results[1].Status != StatusTrusted {
		t.Fatalf("codex: %+v", results[1])
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), original) {
		t.Errorf("the existing file must be kept byte for byte, got:\n%s", data)
	}
	var parsed codexConfig
	if err := toml.Unmarshal(data, &parsed); err != nil || parsed.Projects["/work/app"].TrustLevel != "trusted" {
		t.Fatalf("parsed %+v, %v", parsed, err)
	}
	if again := Trust(paths, []string{"/work/app"}, true)[1]; again.Status != StatusAlready {
		t.Errorf("again: %+v", again)
	}
	// A folder the human told Codex not to trust is not overridden.
	declined := Trust(paths, []string{"/declined"}, true)[1]
	if declined.Status != StatusSkipped || !strings.Contains(declined.Detail, "untrusted") {
		t.Errorf("declined: %+v", declined)
	}
}

func TestCodexTrustLeavesAnInlineProjectsTableAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "projects = { \"/old\" = { trust_level = \"trusted\" } }\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	result := Trust(Paths{ClaudeConfig: filepath.Join(filepath.Dir(path), "c.json"), CodexConfig: path}, []string{"/new"}, true)[1]
	if result.Status != StatusFailed {
		t.Fatalf("inline table: %+v", result)
	}
	if data, _ := os.ReadFile(path); string(data) != original {
		t.Errorf("the file was changed: %s", data)
	}
}
