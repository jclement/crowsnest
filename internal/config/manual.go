// The manual's settings reference (docs/manual/reference/settings.md) is generated from the
// option table and the table of settings the app may change, so it cannot name a flag that does
// not exist or miss one that does. manual_test.go fails when the committed page differs;
// `UPDATE_MANUAL=1 go test ./internal/config -run Manual` rewrites it.

package config

import (
	"fmt"
	"strings"
)

// applyWords is how the manual says when a change made in the app takes effect.
var applyWords = map[Apply]string{
	ApplyLive:         "at once",
	ApplyOrchestrator: "when the orchestrator restarts (crowsnest restarts it)",
	ApplyRestart:      "when crowsnest restarts",
}

// ManualSettingsMarkdown renders the settings reference page.
func ManualSettingsMarkdown() string {
	var b strings.Builder
	b.WriteString("# Settings and flags\n\n")
	b.WriteString("<!-- Generated from internal/config (the option table and the settings the app may change). " +
		"Do not edit by hand: run `UPDATE_MANUAL=1 go test ./internal/config -run Manual`. -->\n\n")
	b.WriteString("Every setting crowsnest has, and where you can change it. A value is taken from the first of these that sets it: " +
		"a command-line flag, an environment variable, the app's **Settings** sheet (only the settings marked below), " +
		"the config file (`~/.config/crowsnest/config.yaml`, keys are the flag names), then the default. " +
		"When a flag or variable sets something, the Settings sheet shows it locked, with where to change it.\n\n")
	b.WriteString("Durations need a unit (`15m`, `2h`); a bare number is refused. Lists repeat as flags, are comma-separated in the environment, and are a YAML list in the file.\n\n")
	b.WriteString("Open the Settings sheet: [Open Settings](app:sheet/settings). " +
		"The [Settings concept](../concepts/budgets-limits-pause.md) and [how-to pages](../README.md) explain what the important ones are for.\n\n")

	b.WriteString("## Changeable in the app\n\n")
	b.WriteString("These appear in the **Settings** sheet (account menu → Settings), grouped as the sheet groups them.\n\n")
	section := ""
	for _, setting := range editable {
		if setting.Section != section {
			section = setting.Section
			fmt.Fprintf(&b, "### %s\n\n", section)
			b.WriteString("| Setting | Flag | Applies | What it does |\n|---|---|---|---|\n")
		}
		label := setting.Label
		if setting.Invert {
			label += " (on is `--" + setting.Name + "` off)"
		}
		fmt.Fprintf(&b, "| %s | `--%s` | %s | %s |\n", cell(label), setting.Name, applyWords[setting.Apply], cell(setting.Help))
		next := nextSection(setting.Name)
		if next != section {
			b.WriteString("\n")
		}
	}

	b.WriteString("## Every flag\n\n")
	b.WriteString("All of them, in `crowsnest --help` order. The config-file key is the flag's name without the dashes.\n\n")
	b.WriteString("| Flag | Environment | Default | In the app | What it does |\n|---|---|---|---|---|\n")
	for _, row := range FlagTable() {
		name := strings.TrimPrefix(row[0], "--")
		env := "none"
		if row[1] != "" {
			parts := strings.Split(row[1], ", ")
			for i, part := range parts {
				parts[i] = "`" + part + "`"
			}
			env = strings.Join(parts, ", ")
		}
		def := "none"
		if row[2] != "" {
			def = "`" + row[2] + "`"
		}
		inApp := "no"
		if setting, ok := LookupSetting(name); ok {
			inApp = setting.Section + " → " + setting.Label
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n", row[0], env, cell(def), cell(inApp), cell(row[3]))
	}
	b.WriteString("\nNever changeable from a browser, whatever the database holds: the login, the bind, the port, TLS, allowed hosts and trusted proxies, the herdr session, the data directory and the orchestrator's directory.\n")
	return b.String()
}

// nextSection is the Settings section of the row after name, or "" at the end.
func nextSection(name string) string {
	for i, setting := range editable {
		if setting.Name == name && i+1 < len(editable) {
			return editable[i+1].Section
		}
	}
	return ""
}

// cell makes text safe inside a markdown table cell.
func cell(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "|", "\\|"), "\n", " ")
}
