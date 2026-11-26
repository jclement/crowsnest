// Rendering a report: the one-line summary serve logs at start, the text `crowsnest doctor`
// prints (coloured on a terminal, plain when piped), and the redacted diagnostics the System
// page's "Copy diagnostics" produces for pasting into an issue.

package doctor

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/jclement/crowsnest/internal/redact"
)

// Summary is the one-line state of the essentials:
// "herdr 0.9.5 ✓  claude 2.1.332 ✓ signed in  codex not found".
func Summary(report Report) string {
	var parts []string
	if check, ok := report.Find("herdr.installed"); ok && report.Versions.Herdr != "" {
		parts = append(parts, "herdr "+report.Versions.Herdr+" "+mark(check.Status))
	} else {
		parts = append(parts, "herdr not found ✗")
	}
	for _, agent := range report.Agents {
		switch {
		case !agent.Installed:
			parts = append(parts, agent.Name+" not found")
		case agent.SignedIn != nil && !*agent.SignedIn:
			parts = append(parts, agent.Name+" "+agent.Version+" ✗ not signed in")
		case agent.SignedIn != nil:
			parts = append(parts, agent.Name+" "+agent.Version+" ✓ signed in")
		default:
			parts = append(parts, agent.Name+" "+agent.Version+" ?")
		}
	}
	return strings.Join(parts, "  ")
}

func mark(status Status) string {
	switch status {
	case StatusFail:
		return "✗"
	case StatusWarn:
		return "!"
	}
	return "✓"
}

// groupTitles name the report's sections.
var groupTitles = []struct{ id, title string }{
	{GroupConfig, "Configuration"}, {GroupHerdr, "herdr"}, {GroupAgents, "Agents"}, {GroupClaude, "Claude Code"},
	{GroupMachine, "Machine"}, {GroupOptional, "Optional"}, {GroupLive, "Running instance"},
}

// Style is how Write draws: colour on a terminal.
type Style struct{ Color bool }

func (s Style) paint(code, text string) string {
	if !s.Color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) badge(status Status) string {
	switch status {
	case StatusOK:
		return s.paint("32", "  ok ")
	case StatusInfo:
		return s.paint("36", "info ")
	case StatusWarn:
		return s.paint("33", "warn ")
	case StatusFail:
		return s.paint("1;31", "FAIL ")
	}
	return s.paint("2", "skip ")
}

// Write prints the report as text: sections, one line per check with its fix below, then a
// verdict.
func Write(out io.Writer, report Report, style Style) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", style.paint("1", "crowsnest doctor"), style.paint("2", report.Versions.Crowsnest))
	checks := withLive(report)
	for _, group := range groupTitles {
		var lines []Check
		for _, check := range checks {
			if check.Group == group.id {
				lines = append(lines, check)
			}
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s\n", style.paint("1;4", group.title))
		for _, check := range lines {
			fmt.Fprintf(&b, "  %s %s", style.badge(check.Status), check.Title)
			if check.Detail != "" {
				fmt.Fprintf(&b, "%s", style.paint("2", ": "+check.Detail))
			}
			if check.Cost != "" {
				fmt.Fprintf(&b, "%s", style.paint("35", " [cost: "+check.Cost+"]"))
			}
			b.WriteString("\n")
			if check.Fix != "" && (check.Status == StatusFail || check.Status == StatusWarn || check.Status == StatusInfo) {
				fmt.Fprintf(&b, "        %s %s\n", style.paint("36", "→"), check.Fix)
			}
		}
	}
	fmt.Fprintf(&b, "\n%s  %s\n", verdictLine(report.Verdict, style), report.Summary)
	if report.Verdict == StatusFail {
		b.WriteString("Fix the FAIL lines above; crowsnest still starts and its app shows the same list.\n")
	}
	_, err := io.WriteString(out, b.String())
	return err
}

func verdictLine(verdict Status, style Style) string {
	switch verdict {
	case StatusFail:
		return style.paint("1;31", "✗ problems found")
	case StatusWarn:
		return style.paint("1;33", "! works, with warnings")
	}
	return style.paint("1;32", "✓ ready")
}

// withLive turns a running instance's view into checks of the "live" group.
func withLive(report Report) []Check {
	checks := report.Checks
	live := report.Live
	if live == nil || !live.Running {
		return checks
	}
	extra := []Check{}
	if live.SetupRequired {
		extra = append(extra, Check{ID: "live.setup", Group: GroupLive, Title: "Setup", Status: StatusInfo,
			Detail: "waiting for first-run setup", Fix: "Open the setup link from crowsnest's log"})
	}
	if live.Orchestrator != "" {
		status := StatusOK
		if !live.OrchestratorOK {
			status = StatusWarn
		}
		extra = append(extra, Check{ID: "live.orchestrator", Group: GroupLive, Title: "Orchestrator", Status: status, Detail: live.Orchestrator + detailSuffix(live.Detail)})
	}
	if live.Uptime != "" {
		extra = append(extra, Check{ID: "live.uptime", Group: GroupLive, Title: "Uptime", Status: StatusOK, Detail: live.Uptime})
	}
	if len(live.Hooks) > 0 {
		var names []string
		for name := range live.Hooks {
			names = append(names, name)
		}
		extra = append(extra, Check{ID: "live.hooks", Group: GroupLive, Title: "Hooks seen firing", Status: StatusOK, Detail: strings.Join(sortedStrings(names), ", ")})
	}
	for _, line := range live.LastErrors {
		extra = append(extra, Check{ID: "live.error", Group: GroupLive, Title: line.Time.Format("15:04:05") + " " + line.Level, Status: StatusInfo, Detail: line.Message})
	}
	return append(append([]Check{}, checks...), extra...)
}

func detailSuffix(detail string) string {
	if detail == "" {
		return ""
	}
	return " (" + detail + ")"
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// emailPattern catches an email address that slipped into a detail (an agent's own output).
var emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

// Diagnostics is the report as plain text for an issue: no colour, the home directory shortened
// to ~, token-shaped strings and email addresses removed. Secrets are never in a report in the
// first place (Sources prints "(set)"); this is the second fence.
func Diagnostics(report Report, home string) string {
	var b strings.Builder
	_ = Write(&b, report, Style{})
	if len(report.Settings) > 0 {
		b.WriteString("\nSettings not at their default\n")
		for _, setting := range report.Settings {
			fmt.Fprintf(&b, "  %s = %s  (%s: %s)\n", setting.Name, setting.Value, setting.Source, setting.Where)
		}
	}
	fmt.Fprintf(&b, "\nSelection: orchestrator %s, jobs %s (%s)\n", orDash(report.Selection.Orchestrator), orDash(report.Selection.Jobs), report.Selection.Reason)
	return RedactText(b.String(), home)
}

// RedactText shortens home to ~ and removes tokens and email addresses.
func RedactText(text, home string) string {
	if home != "" && home != "/" {
		text = strings.ReplaceAll(text, home, "~")
	}
	text = emailPattern.ReplaceAllString(text, "[email]")
	return redact.String(text)
}
