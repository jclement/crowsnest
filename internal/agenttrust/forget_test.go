package agenttrust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

// A suite's teardown removes exactly the entries at or under its scratch folder, from both files,
// and nothing else.
func TestForgetUnderRemovesOnlyTheScratchFolders(t *testing.T) {
	dir := t.TempDir()
	scratch := "/Users/x/work/crowsnest/internal/jobs/e2e-stop-abc"
	claudePath := filepath.Join(dir, ".claude.json")
	write(t, claudePath, `{"oauthAccount": {"k": "v"}, "projects": {
		"/Users/x/work/real": {"hasTrustDialogAccepted": true},
		"`+scratch+`/repo": {"hasTrustDialogAccepted": true},
		"`+scratch+`/worktrees/j_1": {"lastCost": 1},
		"`+scratch+`-other/repo": {"hasTrustDialogAccepted": true}
	}}`)
	codexPath := filepath.Join(dir, "config.toml")
	write(t, codexPath, `model = "x"

[projects."/Users/x/work/real"]
trust_level = "trusted"

[projects."`+scratch+`/repo"]
trust_level = "trusted"

[projects."`+scratch+`-other/repo"]
trust_level = "trusted"

[features]
hooks = true
`)
	claude, codex, err := ForgetUnder(Paths{ClaudeConfig: claudePath, CodexConfig: codexPath}, scratch, "/Users/x")
	if err != nil || claude != 2 || codex != 1 {
		t.Fatalf("claude %d codex %d err %v", claude, codex, err)
	}
	var parsed struct {
		Projects     map[string]any `json:"projects"`
		OAuthAccount map[string]any `json:"oauthAccount"`
	}
	data, _ := os.ReadFile(claudePath)
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Projects) != 2 || parsed.Projects["/Users/x/work/real"] == nil || parsed.Projects[scratch+"-other/repo"] == nil || parsed.OAuthAccount["k"] != "v" {
		t.Errorf("claude: %s", data)
	}
	text, _ := os.ReadFile(codexPath)
	var config struct {
		Model    string         `toml:"model"`
		Projects map[string]any `toml:"projects"`
		Features map[string]any `toml:"features"`
	}
	if err := toml.Unmarshal(text, &config); err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if config.Model != "x" || len(config.Projects) != 2 || config.Features["hooks"] != true || strings.Contains(string(text), scratch+"/repo") {
		t.Errorf("codex:\n%s", text)
	}
}

func TestForgetUnderRefusesWideFolders(t *testing.T) {
	for _, root := range []string{"/", "/Users", "/tmp", "/Users/x", "relative/path/deep/enough"} {
		if _, _, err := ForgetUnder(Paths{}, root, "/Users/x"); err == nil {
			t.Errorf("%s was accepted", root)
		}
	}
}
