// Tests for dialog detection, draft detection and the screen hash, against the fixture screens
// in testdata/screens (shaped after Claude Code 2.1.x; replace with real captures as they come).

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "screens", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDetectDialog(t *testing.T) {
	cases := []struct {
		file     string
		present  bool
		labels   []string
		selected string
		form     bool
		warnings []string
		risky    map[string]string // option key → warning
	}{
		{
			file: "claude_permission_bash.txt", present: true,
			labels:   []string{"Yes", "Yes, and don't ask again for rm commands in /Users/jsc/Work/crowsnest", "No, and tell Claude what to do differently (esc)"},
			selected: "1", warnings: []string{"stops asking", "rm -rf"}, risky: map[string]string{"2": "stops asking"},
		},
		{
			file: "claude_permission_boxed.txt", present: true,
			labels:   []string{"Yes", "Yes, allow all edits during this session (shift+tab)", "No, and tell Claude what to do differently (esc)"},
			selected: "1", warnings: []string{"stops asking"}, risky: map[string]string{"2": "stops asking"},
		},
		{
			file: "claude_question_single.txt", present: true,
			labels:   []string{"Event-driven with poll fallback", "Poll only", "Type something."},
			selected: "1", warnings: []string{},
		},
		{
			file: "claude_question_multi.txt", present: true, form: true,
			labels: []string{"Backend only", "Backend and UI", "Type something."}, selected: "1", warnings: []string{},
		},
		{
			file: "claude_multiselect.txt", present: true, form: true,
			labels: []string{"[✔] Go unit tests", "[ ] Web unit tests", "[ ] Integration (real herdr)"}, selected: "1", warnings: []string{},
		},
		{
			file: "claude_trust.txt", present: true,
			labels: []string{"Yes, proceed", "No, exit"}, selected: "1", warnings: []string{},
		},
		{file: "claude_working_numbered_reply.txt"}, // numbered reply, a hint, and a live input box
		{file: "shell_prompt.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			dialog := DetectDialog(fixture(t, tc.file))
			if dialog.Present != tc.present {
				t.Fatalf("Present = %v, want %v (options %+v)", dialog.Present, tc.present, dialog.Options)
			}
			if !tc.present {
				if len(dialog.Options) != 0 {
					t.Fatalf("no dialog but options %+v", dialog.Options)
				}
				return
			}
			if len(dialog.Options) != len(tc.labels) {
				t.Fatalf("options = %+v, want labels %q", dialog.Options, tc.labels)
			}
			for i, option := range dialog.Options {
				if option.Label != tc.labels[i] || option.Key != itoa(i+1) {
					t.Errorf("option %d = %+v, want key %d label %q", i, option, i+1, tc.labels[i])
				}
				if option.Selected != (option.Key == tc.selected) {
					t.Errorf("option %s selected = %v", option.Key, option.Selected)
				}
				if option.Warning != tc.risky[option.Key] {
					t.Errorf("option %s warning = %q, want %q", option.Key, option.Warning, tc.risky[option.Key])
				}
			}
			if dialog.Form != tc.form {
				t.Errorf("Form = %v, want %v", dialog.Form, tc.form)
			}
			if strings.Join(dialog.Warnings, ",") != strings.Join(tc.warnings, ",") {
				t.Errorf("Warnings = %q, want %q", dialog.Warnings, tc.warnings)
			}
		})
	}
}

func TestDetectDialogNeedsFooterNearEnd(t *testing.T) {
	screen := "Esc to cancel\n❯ 1. Yes\n  2. No\nsome\nmore\nlines\nafter\n"
	if DetectDialog(screen).Present {
		t.Fatal("a footer five lines up should not count")
	}
	if !DetectDialog("❯ 1. Yes\n  2. No\n\nEsc to cancel\n").Present {
		t.Fatal("a minimal dialog was not detected")
	}
	if DetectDialog("Enter to select\n").Present {
		t.Fatal("a footer without options is not a dialog")
	}
}

func TestDetectDialogFromANSI(t *testing.T) {
	dialog := DetectDialog(StripANSI(fixture(t, "claude_dialog.ansi")))
	if !dialog.Present || len(dialog.Options) != 2 || !dialog.Options[0].Selected {
		t.Fatalf("dialog = %+v", dialog)
	}
	if strings.Join(dialog.Warnings, ",") != "force push" {
		t.Errorf("Warnings = %q, want force push", dialog.Warnings)
	}
}

// Claude Code 2.1.332's folder-trust prompt, captured by `mise run e2e:claude` (M9), is an
// unnumbered list with "No, exit" preselected, unlike the numbered box in claude_trust.txt (an
// older shape). A digit cannot answer it, so it is not a pressable dialog: the agent still needs
// you (herdr reports it blocked) and the card says to answer it in the terminal.
func TestRealTrustPromptIsNotAPressableDialog(t *testing.T) {
	dialog := DetectDialog(StripANSI(fixture(t, "real_claude_2.1.332_trust.ansi")))
	if dialog.Present || len(dialog.Options) != 0 {
		t.Fatalf("the unnumbered trust prompt was read as a pressable dialog: %+v", dialog)
	}
	for _, file := range []string{"real_claude_2.1.332_idle.ansi", "real_claude_2.1.332_typed_draft.ansi"} {
		if got := DetectDialog(StripANSI(fixture(t, file))); got.Present {
			t.Fatalf("%s: a dialog was found on an idle screen: %+v", file, got)
		}
	}
}

func TestDetectDraft(t *testing.T) {
	cases := []struct {
		file       string
		inputFound bool
		text       string
	}{
		{"claude_idle_ghost.ansi", true, ""},
		{"claude_idle_empty.ansi", true, ""},
		{"claude_idle_draft.ansi", true, "also run the integration suite"},
		{"claude_idle_draft_multiline.ansi", true, "first line of the draft\nsecond line"},
		{"claude_boxed_draft.ansi", true, "fix the flaky test"},
		{"claude_dialog.ansi", false, ""},
		{"shell_prompt.txt", false, ""},
		// Captured from real Claude Code 2.1.332 by `mise run e2e:claude` (M9), joining the
		// hand-made screens above: the idle prompt after a reply, and a draft typed at the TUI.
		{"real_claude_2.1.332_idle.ansi", true, ""},
		{"real_claude_2.1.332_typed_draft.ansi", true, "half-typed draft from the terminal"},
		{"real_claude_2.1.332_trust.ansi", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			draft := DetectDraft(fixture(t, tc.file))
			if draft.InputFound != tc.inputFound || draft.Text != tc.text || draft.Present != (tc.text != "") {
				t.Fatalf("draft = %+v, want input_found %v text %q", draft, tc.inputFound, tc.text)
			}
		})
	}
}

func TestStripANSI(t *testing.T) {
	cases := map[string]string{
		"\x1b[1mbold\x1b[0m":                        "bold",
		"\x1b]0;title\x07after":                     "after",
		"\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\": "link",
		"a\x1b[2Kb\r\nc":                            "ab\nc",
		"\x1b[38;2;10;20;30mrgb\x1b[39m":            "rgb",
		"trailing\x1b[":                             "trailing",
		"\x1b7saved\x1b8":                           "saved",
	}
	for input, want := range cases {
		if got := StripANSI(input); got != want {
			t.Errorf("StripANSI(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSGRGhostDetection(t *testing.T) {
	cases := []struct {
		input string
		ghost bool
	}{
		{"\x1b[2mx", true},
		{"\x1b[2m\x1b[22mx", false},
		{"\x1b[90mx", true},
		{"\x1b[90m\x1b[39mx", false},
		{"\x1b[38;5;244mx", true},
		{"\x1b[38;5;33mx", false},
		{"\x1b[38;2;128;128;128mx", true},
		{"\x1b[38;2;200;40;40mx", false},
		{"\x1b[2;7mx\x1b[0my", true}, // x ghost; checked below that y is not
	}
	for _, tc := range cases {
		cells := parseANSI(tc.input)[0].cells
		if len(cells) == 0 || cells[0].ghost() != tc.ghost {
			t.Errorf("%q: first cell ghost = %v, want %v", tc.input, len(cells) > 0 && cells[0].ghost(), tc.ghost)
		}
	}
	cells := parseANSI("\x1b[2;7mx\x1b[0my")[0].cells
	if cells[1].ghost() || cells[1].inverse {
		t.Error("SGR 0 did not reset faint and inverse")
	}
}

func TestScreenHash(t *testing.T) {
	base := ScreenHash("line one\nline two\n")
	if len(base) != screenHashLength {
		t.Fatalf("hash length %d", len(base))
	}
	if ScreenHash("line one   \nline two\n\n\n") != base || ScreenHash("line one\r\nline two") != base {
		t.Error("trailing space, blank lines and CRLF should not change the hash")
	}
	if ScreenHash("line one\nline 2\n") == base {
		t.Error("different text, same hash")
	}
}
