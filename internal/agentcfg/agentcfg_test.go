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
	settings := AllowRead(ClaudeSettings("", "", "", "", true), inbox)
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
