package fleet

import (
	"strings"
	"testing"
)

func TestTrustPromptRealClaude(t *testing.T) {
	dialog := DetectDialog(StripANSI(fixture(t, "real_claude_2.1.332_trust.ansi")))
	trust := dialog.Trust
	if !dialog.Present || trust == nil {
		t.Fatalf("Claude 2.1.332's trust prompt not recognised: %+v", dialog)
	}
	if trust.Engine != TrustClaude {
		t.Errorf("engine = %q", trust.Engine)
	}
	if !strings.HasSuffix(trust.Folder, "/web/e2e/claude/.scratch-NpgTKa/repo") || !strings.HasPrefix(trust.Folder, "/Users/") {
		t.Errorf("folder = %q", trust.Folder)
	}
	if trust.Allow.Label != "Yes, I trust this folder" || strings.Join(trust.Allow.Keys, " ") != "down Enter" {
		t.Errorf("allow = %+v", trust.Allow)
	}
	if trust.Deny.Label != "No, exit" || strings.Join(trust.Deny.Keys, " ") != "Enter" {
		t.Errorf("deny = %+v", trust.Deny)
	}
}

// codex-cli 0.167's prompt: a numbered list whose footer has no "to", and whose digits only move
// the selection, so each answer ends with Enter. The folder wraps onto a second line.
func TestTrustPromptRealCodex(t *testing.T) {
	dialog := DetectDialog(StripANSI(fixture(t, "real_codex_0.167_trust.ansi")))
	trust := dialog.Trust
	if !dialog.Present || trust == nil {
		t.Fatalf("Codex 0.167's trust prompt not recognised: %+v", dialog)
	}
	if trust.Engine != TrustCodex {
		t.Errorf("engine = %q", trust.Engine)
	}
	if !strings.HasPrefix(trust.Folder, "/private/tmp/") || !strings.HasSuffix(trust.Folder, "/scratchpad/trust/cxfix") || strings.Contains(trust.Folder, " ") {
		t.Errorf("folder = %q (the wrapped path must be joined)", trust.Folder)
	}
	if trust.Allow.Label != "Trust and continue" || strings.Join(trust.Allow.Keys, " ") != "1 Enter" {
		t.Errorf("allow = %+v", trust.Allow)
	}
	if !strings.HasPrefix(trust.Deny.Label, "Back to") || strings.Join(trust.Deny.Keys, " ") != "2 Enter" {
		t.Errorf("deny = %+v", trust.Deny)
	}
}

// The numbered box older Claude versions drew still reads as a trust prompt, answered by a digit.
func TestTrustPromptNumberedBox(t *testing.T) {
	dialog := DetectDialog(fixture(t, "claude_trust.txt"))
	trust := dialog.Trust
	if trust == nil {
		t.Fatalf("numbered trust box not recognised: %+v", dialog)
	}
	if trust.Folder != "/Users/jsc/.local/share/crowsnest/orchestrator" {
		t.Errorf("folder = %q", trust.Folder)
	}
	if strings.Join(trust.Allow.Keys, " ") != "1" || strings.Join(trust.Deny.Keys, " ") != "2" {
		t.Errorf("keys: allow %v deny %v", trust.Allow.Keys, trust.Deny.Keys)
	}
}

func TestTrustPromptNotOtherDialogs(t *testing.T) {
	for _, name := range []string{"claude_permission_bash.txt", "claude_question_single.txt", "claude_permission_boxed.txt"} {
		dialog := DetectDialog(fixture(t, name))
		if !dialog.Present {
			t.Fatalf("%s: expected a dialog", name)
		}
		if dialog.Trust != nil {
			t.Errorf("%s: read as a trust prompt: %+v", name, dialog.Trust)
		}
	}
}
