// Screen analysis: pure functions over the text of an agent's visible screen. They answer three
// questions the UI and the supervisor need without asking the agent anything:
//
//   - Is a dialog up, and what are its numbered options? (DESIGN.md 8.2.) herdr reports most
//     dialogs as `blocked`, but sometimes as idle or done, so the screen is checked as well.
//   - Is there a draft in the input box, typed at the terminal? (7.3.) agent.prompt appends to
//     whatever is there, so a send from the UI must not silently glue onto someone's half-typed
//     line. Placeholder "ghost" text is drawn dim and is ignored.
//   - What is the screen's hash? A key press from a dialog card carries the hash of the screen it
//     was rendered from, and is refused if the screen has changed since.
//
// These are heuristics over Claude Code's TUI (2.1.x), which is not a stable contract; the
// fixtures under testdata/screens pin the shapes they were written against.
package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// ---- Screen hash ---------------------------------------------------------------------

// screenHashLength is how many hex characters of the SHA-256 are kept: 128 bits, far more than
// enough to tell two screens apart, and short enough to read in a log.
const screenHashLength = 32

// ScreenHash fingerprints a screen's plain text. Trailing spaces on each line and trailing blank
// lines are ignored, because terminals pad differently and neither changes what a person sees.
func ScreenHash(text string) string {
	sum := sha256.Sum256([]byte(normaliseScreen(text)))
	return hex.EncodeToString(sum[:])[:screenHashLength]
}

func normaliseScreen(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// ---- Dialogs -------------------------------------------------------------------------

// Dialog is what DetectDialog found on a screen.
type Dialog struct {
	Present bool           `json:"present"`
	Options []DialogOption `json:"options"`
	// Form is set for screens a single digit cannot answer: several questions behind tabs, or
	// a multi-select list where digits toggle. The UI offers only "open terminal" for these.
	Form bool `json:"form"`
	// Warnings are risky phrases found anywhere in the dialog (rm -rf, force push, …); the UI
	// shows them as a badge. They never block an answer: the human is reading the real screen.
	Warnings []string `json:"warnings"`
}

// DialogOption is one numbered choice. Key is the digit to send.
type DialogOption struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Selected bool   `json:"selected"`          // the highlighted (cursor) option
	Warning  string `json:"warning,omitempty"` // set when the label itself is risky
}

// dialogFooterLines is how many of the last non-blank lines may hold the footer hint.
const dialogFooterLines = 4

var (
	dialogFooter = regexp.MustCompile(`(?i)enter to (select|confirm|submit)|esc to cancel`)
	// optionLine: an optional cursor marker, a number, "." or ")", then the label.
	optionLine = regexp.MustCompile(`^(?:([❯›>])\s*)?(\d{1,2})[.)]\s+(\S.*?)\s*$`)
	// formHints mark screens that one digit cannot answer.
	formTabs     = regexp.MustCompile(`[☐☒✔✓].*\bSubmit\b|^←.*→$`)
	formCheckbox = regexp.MustCompile(`^\[[ xX✔✓]\]\s`)
	// riskyPhrases are the front door's refuse/deny list, kept as warnings (DESIGN.md 8.2).
	riskyPhrases = []struct {
		pattern *regexp.Regexp
		label   string
	}{
		{regexp.MustCompile(`(?i)always allow|allow all|don'?t ask again|do not ask again`), "stops asking"},
		{regexp.MustCompile(`(?i)bypass ?permissions|dangerously`), "bypasses permissions"},
		{regexp.MustCompile(`(?i)\brm\s+-[a-z]*r[a-z]*f|\brm\s+-[a-z]*f[a-z]*r`), "rm -rf"},
		{regexp.MustCompile(`(?i)push\s+(?:\S+\s+)*(?:--force\b|-f\b)|force[- ]push`), "force push"},
		{regexp.MustCompile(`(?i)git\s+reset\s+--hard|git\s+clean\s+-[a-z]*f`), "discards changes"},
		{regexp.MustCompile(`(?i)\bdrop\s+(table|database|schema)\b`), "drops data"},
	}
)

// DetectDialog reports whether text (a plain, ANSI-stripped screen) shows a dialog: one of the
// last four non-blank lines carries a footer hint ("Enter to select", "Esc to cancel", …) and a
// numbered option line exists. Both are required, so a numbered list in an agent's reply or a
// stray hint in scrollback is not mistaken for a dialog. One more guard: Claude hides its input
// box while a dialog is up, so options with a live input line below them are an agent's reply.
func DetectDialog(text string) Dialog {
	none := Dialog{Options: []DialogOption{}, Warnings: []string{}}
	lines := contentLines(text)
	if !footerNearEnd(lines) {
		return none
	}
	options, lastOption := lastOptionRun(lines)
	if len(options) == 0 || findInputLine(lines) > lastOption {
		return none
	}
	return Dialog{Present: true, Options: options, Form: isForm(lines), Warnings: warningsIn(lines)}
}

// contentLines splits a screen into lines with box borders and surrounding space removed.
func contentLines(text string) []string {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, stripBorders(line))
	}
	return lines
}

// stripBorders removes a leading and trailing box-drawing border ("│ … │") and spaces.
func stripBorders(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimSpace(strings.TrimPrefix(line, "│"))
	line = strings.TrimSpace(strings.TrimSuffix(line, "│"))
	return line
}

func footerNearEnd(lines []string) bool {
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < dialogFooterLines; i-- {
		if lines[i] == "" || isRule(lines[i]) {
			continue
		}
		seen++
		if dialogFooter.MatchString(lines[i]) {
			return true
		}
	}
	return false
}

// lastOptionRun returns the last run of options numbered 1, 2, 3, … on the screen. Lines between
// options (descriptions under a choice) are allowed; a restart at 1 begins a new run, so an
// earlier numbered list in the agent's output does not leak into the dialog's options.
// It also returns the index of the run's last line (-1 when there is none).
func lastOptionRun(lines []string) ([]DialogOption, int) {
	var run []DialogOption
	last := -1
	for index, line := range lines {
		match := optionLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		key, label := match[2], match[3]
		switch {
		case key == "1":
			run = run[:0]
		case len(run) == 0 || key != itoa(len(run)+1):
			continue // out of sequence: not part of a dialog's option list
		}
		run = append(run, DialogOption{Key: key, Label: label, Selected: match[1] != "", Warning: riskyLabel(label)})
		last = index
	}
	return append([]DialogOption{}, run...), last
}

func itoa(n int) string { return strconv.Itoa(n) }

func isForm(lines []string) bool {
	for _, line := range lines {
		if formTabs.MatchString(line) {
			return true
		}
		if match := optionLine.FindStringSubmatch(line); match != nil && formCheckbox.MatchString(match[3]) {
			return true
		}
	}
	return false
}

func riskyLabel(label string) string {
	for _, risky := range riskyPhrases {
		if risky.pattern.MatchString(label) {
			return risky.label
		}
	}
	return ""
}

// warningsIn lists each risky phrase found on the screen once, in table order.
func warningsIn(lines []string) []string {
	joined := strings.Join(lines, "\n")
	warnings := []string{}
	for _, risky := range riskyPhrases {
		if risky.pattern.MatchString(joined) {
			warnings = append(warnings, risky.label)
		}
	}
	return warnings
}

// ---- Drafts --------------------------------------------------------------------------

// Draft is what DetectDraft found in the agent's input box.
type Draft struct {
	// InputFound is false when no input box is visible (a dialog, a full-screen view, a shell);
	// then Present says nothing.
	InputFound bool   `json:"input_found"`
	Present    bool   `json:"present"`
	Text       string `json:"text"`
}

// promptMarkers start Claude's input line: "❯" in 2.x, ">" inside the older bordered box.
var promptMarkers = []string{"❯", ">"}

// DetectDraft finds the input box in an ANSI screen and returns the text typed into it. The
// input line is a prompt marker directly under a horizontal rule (Claude draws one above the
// box); its text, and any continuation lines down to the next rule, count as draft unless drawn
// dim or grey, which is how placeholder suggestions are shown. A lone reverse-video cell is the
// cursor and does not by itself make a draft.
func DetectDraft(ansiScreen string) Draft {
	lines := parseANSI(ansiScreen)
	plain := make([]string, len(lines))
	for i, line := range lines {
		plain[i] = stripBorders(line.plain())
	}
	start := findInputLine(plain)
	if start < 0 {
		return Draft{}
	}
	var parts []string
	for i := start; i < len(lines); i++ {
		if i > start && isRule(plain[i]) {
			break
		}
		cells := lines[i].cells
		if i == start {
			cells = afterMarker(cells)
		}
		if text := typedText(cells); text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	return Draft{InputFound: true, Present: text != "", Text: text}
}

// findInputLine returns the index of the last prompt line that sits right under a rule, or -1.
// lines are border-stripped plain lines.
func findInputLine(lines []string) int {
	for i := len(lines) - 1; i > 0; i-- {
		if !hasPromptMarker(lines[i]) || optionLine.MatchString(lines[i]) {
			continue
		}
		if above := previousNonBlank(lines, i); above >= 0 && isRule(lines[above]) {
			return i
		}
	}
	return -1
}

func hasPromptMarker(plain string) bool {
	for _, marker := range promptMarkers {
		if plain == marker || strings.HasPrefix(plain, marker+" ") || strings.HasPrefix(plain, marker+" ") {
			return true
		}
	}
	return false
}

func previousNonBlank(lines []string, index int) int {
	for i := index - 1; i >= 0; i-- {
		if lines[i] != "" {
			return i
		}
	}
	return -1
}

// isRule reports whether a line is a horizontal rule: at least ten box-drawing dashes, with
// optional corners (╭─…─╮, ╰─…─╯).
func isRule(line string) bool {
	trimmed := strings.Trim(line, "╭╮╰╯┌┐└┘├┤ ")
	return strings.Count(trimmed, "─") >= 10 && strings.Trim(trimmed, "─") == ""
}

// afterMarker drops the cells up to and including the prompt marker and its border.
func afterMarker(cells []styledCell) []styledCell {
	for i, cell := range cells {
		for _, marker := range promptMarkers {
			if string(cell.r) == marker {
				return cells[i+1:]
			}
		}
	}
	return cells
}

// typedText keeps the characters a person typed: not dim, not grey, borders removed. Reverse
// video cells (the drawn cursor) count only when other typed text is present.
func typedText(cells []styledCell) string {
	var typed, withCursor strings.Builder
	for _, cell := range cells {
		if cell.ghost() {
			continue
		}
		withCursor.WriteRune(cell.r)
		if !cell.inverse {
			typed.WriteRune(cell.r)
		}
	}
	if strings.TrimSpace(stripBorders(typed.String())) == "" {
		return ""
	}
	return stripBorders(withCursor.String())
}
