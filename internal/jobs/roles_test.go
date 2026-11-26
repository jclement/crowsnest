package jobs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRolesEmbeddedAndOverrides(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "builder.md"), []byte("# Role: builder\n\nOur own builder rules."), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "docs.md"), []byte("# Role: docs\n\nWrites documentation."), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "Bad Name.md"), []byte("x"), 0o600)
	roles, problems := LoadRoles(dir)
	if len(problems) != 1 {
		t.Fatalf("problems %v", problems)
	}
	if got := strings.Join(roles.Names(), ","); got != "builder,lead,reviewer,tester,docs" {
		t.Fatalf("names %s", got)
	}
	prompt, err := roles.Prompt("builder", "j_7", true)
	if err != nil || !strings.Contains(prompt, "Our own builder rules") || !strings.Contains(prompt, "crowsnest job **j_7**") || !strings.Contains(prompt, "`done(result_md, verified, not_verified)`") {
		t.Fatalf("prompt %v %.300s", err, prompt)
	}
	cli, _ := roles.Prompt("docs", "j_7", false)
	if !strings.Contains(cli, "crowsnest job update") || strings.Contains(cli, "mcp__crowsnest") {
		t.Fatalf("cli prompt %.300s", cli)
	}
	for _, info := range roles.List() {
		if info.Summary == "" {
			t.Errorf("role %s has no summary", info.Name)
		}
		if (info.Name == "docs" || info.Name == "builder") != info.Custom {
			t.Errorf("role %s custom=%v", info.Name, info.Custom)
		}
	}
	if missing, problems := LoadRoles(filepath.Join(dir, "nope")); len(problems) != 0 || len(missing.Names()) != 4 {
		t.Fatal("a missing roles dir is fine")
	}
}
