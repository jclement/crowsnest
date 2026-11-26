package agentcfg

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAllowReadAddsRulesForThePathAsTypedAndResolved(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "state")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	resolvedReal, _ := filepath.EvalSymlinks(real)
	inbox := filepath.Join(link, "orchestrator", "attachments") // not created yet
	settings := AllowRead(ClaudeSettings("", "", "", "", "", true), inbox)
	allow := settings["permissions"].(map[string]any)["allow"].([]string)
	want := []string{
		MCPAllowRule,
		"Read(/" + inbox + "/**)",
		"Read(/" + filepath.Join(resolvedReal, "orchestrator", "attachments") + "/**)",
	}
	if !slices.Equal(allow, want) {
		t.Fatalf("allow = %q\nwant %q", allow, want)
	}
	if bare := AllowRead(map[string]any{}, ""); bare["permissions"] != nil {
		t.Fatalf("no directories added rules: %v", bare)
	}
}

func TestHooksIncludeApprovalAndGuard(t *testing.T) {
	settings := ClaudeSettings("/bin/crowsnest", "/tmp/s.sock", "j_3", "/state/agent-token", "/state/guard.json", true)
	hooks := settings["hooks"].(map[string]any)
	request := hooks["PermissionRequest"].([]map[string]any)[0]["hooks"].([]map[string]any)[0]
	if request["timeout"] != PermissionRequestTimeout || request["command"] != "'/bin/crowsnest' hook permission-request --socket '/tmp/s.sock' --job 'j_3' --token-file '/state/agent-token'" {
		t.Fatalf("PermissionRequest hook %v", request)
	}
	guard := hooks["PreToolUse"].([]map[string]any)[0]
	if guard["matcher"] != "Bash" {
		t.Fatalf("the guard runs on Bash only: %v", guard)
	}
	command := guard["hooks"].([]map[string]any)[0]["command"].(string)
	if command != "'/bin/crowsnest' hook pre-tool-use --socket '/tmp/s.sock' --job 'j_3' --token-file '/state/agent-token' --rules '/state/guard.json'" {
		t.Fatalf("PreToolUse command %q", command)
	}
	if _, ok := ClaudeSettings("/bin/crowsnest", "/tmp/s.sock", "", "", "", false)["hooks"].(map[string]any)["PreToolUse"]; ok {
		t.Fatal("no rules file, no guard hook")
	}
}
