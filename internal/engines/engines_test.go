package engines

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

const manualPath = "../../docs/manual/reference/engines.md"

func TestManualEnginesReference(t *testing.T) {
	want := ManualMarkdown()
	if os.Getenv("UPDATE_MANUAL") == "1" {
		if err := os.WriteFile(manualPath, []byte(want), 0o644); err != nil { //nolint:gosec // a committed doc
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(manualPath)
	if err != nil || string(got) != want {
		t.Errorf("%s is out of date: run UPDATE_MANUAL=1 go test ./internal/engines -run Manual", manualPath)
	}
}

func TestTheTwoEnginesListTheSameRows(t *testing.T) {
	claude, codex := ClaudeEngine(true), CodexEngine(true, nil)
	if len(claude.Capabilities) != len(order) || len(codex.Capabilities) != len(order) {
		t.Fatalf("each engine states every capability once: %d %d", len(claude.Capabilities), len(codex.Capabilities))
	}
	for i, id := range order {
		if claude.Capabilities[i].ID != id || codex.Capabilities[i].ID != id {
			t.Errorf("row %d: %s / %s, want %s", i, claude.Capabilities[i].ID, codex.Capabilities[i].ID, id)
		}
		if codex.Capabilities[i].Note == "" || claude.Capabilities[i].Note == "" {
			t.Errorf("%s: every row says why", id)
		}
	}
	if len(claude.Without) != 0 {
		t.Errorf("Claude runs without nothing: %v", claude.Without)
	}
	for _, want := range []string{"Answer receipts", "Usage-limit detection", "Spinning detection"} {
		if !strings.Contains(strings.Join(codex.Without, "|"), want) {
			t.Errorf("Codex's card must say it runs without %s: %v", want, codex.Without)
		}
	}
}

func TestRescueRefusals(t *testing.T) {
	for _, action := range []string{RescueRestart, RescueCompact, RescueModel} {
		if RescueRefusal(Codex, action) == "" {
			t.Errorf("Codex %s must be refused with a sentence", action)
		}
		if RescueRefusal(Claude, action) != "" {
			t.Errorf("Claude %s is supported", action)
		}
	}
	for _, action := range []string{RescueInterrupt, RescueContinue, RescueNudge, RescueUnstick} {
		if RescueRefusal(Codex, action) != "" {
			t.Errorf("Codex %s works", action)
		}
	}
}

func TestCatalogReadsCodexModels(t *testing.T) {
	calls := 0
	catalog := &Catalog{
		LookPath: func(name string) (string, error) {
			if name == "codex" || name == "claude" {
				return "/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Run: func(context.Context, string, ...string) ([]byte, error) {
			calls++
			return []byte(`{"models":[{"slug":"b","display_name":"B","visibility":"list","priority":2},{"slug":"hidden","visibility":"hide","priority":0},{"slug":"a","display_name":"A","visibility":"list","priority":1}]}`), nil
		},
	}
	table := catalog.Engines(context.Background())
	if len(table) != 2 || !table[0].Installed || !table[1].Installed {
		t.Fatalf("table = %+v", table)
	}
	models := table[1].Models
	if len(models) != 2 || models[0].ID != "a" || models[1].ID != "b" {
		t.Errorf("listed models by priority: %+v", models)
	}
	catalog.Engines(context.Background())
	if calls != 1 {
		t.Errorf("the model list is cached: %d calls", calls)
	}
}
