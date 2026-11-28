# Claude and Codex: what each engine gets

<!-- Generated from internal/engines. Do not edit by hand: run `UPDATE_MANUAL=1 go test ./internal/engines -run Manual`. -->

A job runs on Claude Code or on Codex. Crowsnest supervises both, but not equally: some of its guarantees depend on what each CLI lets it see and hold. The job form, a project's default engine, a Codex job's card and Rescue all read this same table from the server (`GET /api/v1/engines`).

**Claude Code:** Every guarantee crowsnest has: the stop guarantee, approvals from the browser, spend, receipts, usage-limit and spinning detection, and every rescue action.

**Codex:** The stop guarantee, approvals from the browser and the guard (through Codex's hooks), and spend in tokens, with the sandbox's network off: anything that needs the internet asks you first. No answer receipts, usage-limit or spinning detection, and no restart, compact or model switch in Rescue.

## Guarantees

| Guarantee | Claude Code | Codex |
|---|---|---|
| Stop guarantee | yes: the Stop hook refuses a turn that ends without done, fail, blocked or a question | yes: Codex's Stop hook, handed to the job for its session (--dangerously-bypass-hook-trust, the hooks crowsnest wrote); the job reports through crowsnest's MCP server, which Codex runs outside its sandbox. A repository's own .codex/ config would run unreviewed with that flag, so a Codex job waits until you have allowed it on the project page |
| Approvals from the browser | yes: the PermissionRequest hook brings each tool approval to the browser | yes: Codex asks on request inside its workspace-write sandbox, whose network is off: a write outside the workspace and any command that needs the network escalate, and the PermissionRequest hook brings each request to the browser, rated by risk; an unanswered one falls back to Codex's own dialog, shown as a card. Codex keeps .git read-only in its sandbox, so every commit asks: a project approval rule for git commit answers those |
| Guard (protected branches and hosts) | yes: the PreToolUse hook checks shell commands and holds writes while crowsnest checks the worktree | partly: shell commands are checked by the PreToolUse hook; apply_patch edits are held while crowsnest checks the worktree but not otherwise inspected |
| Spend measured | yes: tokens and an estimated cost, from Claude's transcript | partly: tokens from Codex's session log; no cost estimate (crowsnest has no prices for OpenAI's models) |
| Answer receipts | yes: an answer is marked received when it shows up in the conversation | **no**: receipts read Claude's transcript; a Codex answer stops at "delivered" |
| Usage-limit detection | yes: the StopFailure hook reports the limit; the fleet holds and resumes at the reset | **no**: Codex's "You've hit your usage limit" is not recognised: a job that hits it sits idle until the backstop nudges it |
| Spinning detection | yes: read from the transcript: the same failing step, over and over | **no**: spinning is read from Claude's transcript |
| Instruction changes: restart to apply | yes: changed-since-start is shown, and Restart applies the new layers in the same conversation | **no**: changed-since-start is shown, but a Codex job cannot be restarted from crowsnest to apply it |
| No folder-trust prompt in a trusted project | yes: a linked worktree inherits its checkout's trust (Claude Code 2.1.332) | yes: Codex resolves a worktree to its repository root's trust (codex-cli 0.167) |
| Heavy-command slots | yes: crowsnest heavy waits for a slot, so agents testing at once cannot freeze the machine | partly: the sandbox cannot write the slot files, so crowsnest heavy runs the command at once, at low priority with the tools' parallelism capped, and does not wait for a slot |

## Rescue actions

| Action | Claude Code | Codex |
|---|---|---|
| Interrupt (Esc) | yes | yes |
| Continue | yes | yes |
| Nudge | yes | yes |
| Get it moving | yes | yes |
| Restart (same conversation) | yes | no: A Codex job cannot be restarted from crowsnest: Codex has no resume crowsnest drives. Interrupt it and send a message, or cancel it and give the job again. |
| Compact now | yes | no: Compact is a Claude Code command; a Codex job has no compact crowsnest can send. |
| Switch model | yes | no: Switching model is offered for Claude jobs only; give the job again with the model you want. |

## Models

Claude jobs offer Claude Code's aliases (`sonnet`, `opus`, `haiku`), which follow the newest model of each family. Codex jobs offer the models the installed Codex lists in its own picker (`codex debug models`, those marked to be listed), read when the form opens.

## How Codex is supervised

A Codex job starts with `--ask-for-approval on-request --sandbox workspace-write` and the sandbox's network off (`-c sandbox_workspace_write.network_access=false`, said explicitly so your own `~/.codex/config.toml` cannot turn it on for a job): a command that needs the internet escalates, and the escalation reaches you as an approval, rated by risk. The job reports to crowsnest through crowsnest's MCP server (`-c mcp_servers.crowsnest=…`, its tools pre-approved), and crowsnest's own hooks (Stop, SessionStart, PreToolUse, PostToolUse and PermissionRequest) are handed to that one session with `-c hooks.…` and `--dangerously-bypass-hook-trust`, which runs them without Codex's per-hook review. Codex runs both outside its sandbox, which is how they reach crowsnest's socket with the network off; the sandboxed shell cannot. That flag would also run, without review, whatever the repository itself declares in `.codex/` (hooks, and MCP servers, which also start outside the sandbox). The agent cannot write the workspace's `.codex/` from inside its sandbox (checked with `codex sandbox`), but a clone or a pull can bring one, so a Codex job does not start in a folder whose `.codex/` you have not allowed as it is now: trusting the folder while its files are listed allows them, or **Allow it for Codex jobs** on the project page; any later change stops the next Codex job until you look again. Codex keeps `.git` read-only inside its sandbox (`writable_roots` does not lift it; checked with `codex sandbox`), so each `git commit` is an escalation, which reaches you as an approval: a project approval rule for `git commit` answers them. `crowsnest heavy` cannot take a slot inside the sandbox, so there it runs the command at once at low priority. Nothing is written into `~/.codex/config.toml` for any of this. Run `mise run test:codex` to check these against the Codex installed here.
