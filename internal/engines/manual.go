package engines

import (
	"fmt"
	"strings"
)

// ManualMarkdown is docs/manual/reference/engines.md, generated from the table so the manual
// says exactly what the code gives (`UPDATE_MANUAL=1 go test ./internal/engines -run Manual`).
// Models are left out: they come from the installed CLIs and change without a crowsnest release.
func ManualMarkdown() string {
	claude, codex := ClaudeEngine(true), CodexEngine(true, nil)
	var b strings.Builder
	b.WriteString("# Claude and Codex: what each engine gets\n\n")
	b.WriteString("<!-- Generated from internal/engines. Do not edit by hand: run `UPDATE_MANUAL=1 go test ./internal/engines -run Manual`. -->\n\n")
	b.WriteString("A job runs on Claude Code or on Codex. Crowsnest supervises both, but not equally: some of its guarantees depend on what each CLI lets it see and hold. ")
	b.WriteString("The job form, a project's default engine, a Codex job's card and Rescue all read this same table from the server (`GET /api/v1/engines`).\n\n")
	b.WriteString("**" + claude.Name + ":** " + claude.Summary + "\n\n")
	b.WriteString("**" + codex.Name + ":** " + codex.Summary + "\n\n")
	b.WriteString("## Guarantees\n\n| Guarantee | Claude Code | Codex |\n|---|---|---|\n")
	for i, c := range claude.Capabilities {
		x := codex.Capabilities[i]
		fmt.Fprintf(&b, "| %s | %s | %s |\n", c.Label, cell(c), cell(x))
	}
	b.WriteString("\n## Rescue actions\n\n| Action | Claude Code | Codex |\n|---|---|---|\n")
	for i, r := range claude.Rescue {
		x := codex.Rescue[i]
		codexCell := "yes"
		if !x.Available {
			codexCell = "no: " + x.Refusal
		}
		fmt.Fprintf(&b, "| %s | yes | %s |\n", rescueLabels[r.ID], codexCell)
	}
	b.WriteString("\n## Models\n\n")
	b.WriteString("Claude jobs offer Claude Code's aliases (" + modelList(claude.Models) + "), which follow the newest model of each family. ")
	b.WriteString("Codex jobs offer the models the installed Codex lists in its own picker (`codex debug models`, those marked to be listed), read when the form opens.\n\n")
	b.WriteString("## How Codex is supervised\n\n")
	b.WriteString("A Codex job starts with `--ask-for-approval on-request --sandbox workspace-write` and the sandbox's network off (`-c sandbox_workspace_write.network_access=false`, said explicitly so your own `~/.codex/config.toml` cannot turn it on for a job): a command that needs the internet escalates, and the escalation reaches you as an approval, rated by risk. ")
	b.WriteString("The job reports to crowsnest through crowsnest's MCP server (`-c mcp_servers.crowsnest=…`, its tools pre-approved), and crowsnest's own hooks (Stop, SessionStart, PreToolUse, PostToolUse and PermissionRequest) are handed to that one session with `-c hooks.…` and `--dangerously-bypass-hook-trust`, which runs them without Codex's per-hook review. Codex runs both outside its sandbox, which is how they reach crowsnest's socket with the network off; the sandboxed shell cannot. ")
	b.WriteString("That flag would also run, without review, whatever the repository itself declares in `.codex/` (hooks, and MCP servers, which also start outside the sandbox). The agent cannot write the workspace's `.codex/` from inside its sandbox (checked with `codex sandbox`), but a clone or a pull can bring one, so a Codex job does not start in a folder whose `.codex/` you have not allowed as it is now: trusting the folder while its files are listed allows them, or **Allow it for Codex jobs** on the project page; any later change stops the next Codex job until you look again. ")
	b.WriteString("Codex keeps `.git` read-only inside its sandbox (`writable_roots` does not lift it; checked with `codex sandbox`), so each `git commit` is an escalation, which reaches you as an approval: a project approval rule for `git commit` answers them. `crowsnest heavy` cannot take a slot inside the sandbox, so there it runs the command at once at low priority. Nothing is written into `~/.codex/config.toml` for any of this. ")
	b.WriteString("Run `mise run test:codex` to check these against the Codex installed here.\n")
	return b.String()
}

var rescueLabels = map[string]string{
	RescueInterrupt: "Interrupt (Esc)", RescueContinue: "Continue", RescueNudge: "Nudge", RescueUnstick: "Get it moving",
	RescueRestart: "Restart (same conversation)", RescueCompact: "Compact now", RescueModel: "Switch model",
}

func cell(c Capability) string {
	switch {
	case c.Available && c.Partial:
		return "partly: " + c.Note
	case c.Available:
		return "yes: " + c.Note
	default:
		return "**no**: " + c.Note
	}
}

func modelList(models []Model) string {
	ids := make([]string, len(models))
	for i, m := range models {
		ids[i] = "`" + m.ID + "`"
	}
	return strings.Join(ids, ", ")
}
