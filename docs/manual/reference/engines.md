# Claude and Codex: what each engine gets

<!-- Generated from internal/engines. Do not edit by hand: run `UPDATE_MANUAL=1 go test ./internal/engines -run Manual`. -->

A job runs on Claude Code or on Codex. Crowsnest supervises both, but not equally: some of its guarantees depend on what each CLI lets it see and hold. The job form, a project's default engine, a Codex job's card and Rescue all read this same table from the server (`GET /api/v1/engines`).

**Claude Code:** Every guarantee crowsnest has: the stop guarantee, approvals from the browser, spend, receipts, usage-limit and spinning detection, and every rescue action.

**Codex:** The stop guarantee, approvals from the browser and the guard (through Codex's hooks), and spend in tokens; no answer receipts, usage-limit or spinning detection, and no restart, compact or model switch in Rescue.

## Guarantees

| Guarantee | Claude Code | Codex |
|---|---|---|
| Stop guarantee | yes: the Stop hook refuses a turn that ends without done, fail, blocked or a question | yes: Codex's Stop hook, handed to the job for its session (--dangerously-bypass-hook-trust, the hooks crowsnest wrote) |
| Approvals from the browser | yes: the PermissionRequest hook brings each tool approval to the browser | yes: Codex asks on request inside its workspace-write sandbox (writes outside the workspace; the network is on, as for a Claude job, so the shell reaches crowsnest); its PermissionRequest hook brings each request to the browser, and an unanswered one falls back to Codex's own dialog, shown as a card |
| Guard (protected branches and hosts) | yes: the PreToolUse hook checks shell commands and holds writes while crowsnest checks the worktree | partly: shell commands are checked by the PreToolUse hook; apply_patch edits are held while crowsnest checks the worktree but not otherwise inspected |
| Spend measured | yes: tokens and an estimated cost, from Claude's transcript | partly: tokens from Codex's session log; no cost estimate (crowsnest has no prices for OpenAI's models) |
| Answer receipts | yes: an answer is marked received when it shows up in the conversation | **no**: receipts read Claude's transcript; a Codex answer stops at "delivered" |
| Usage-limit detection | yes: the StopFailure hook reports the limit; the fleet holds and resumes at the reset | **no**: Codex's "You've hit your usage limit" is not recognised: a job that hits it sits idle until the backstop nudges it |
| Spinning detection | yes: read from the transcript: the same failing step, over and over | **no**: spinning is read from Claude's transcript |
| Instruction changes: restart to apply | yes: changed-since-start is shown, and Restart applies the new layers in the same conversation | **no**: changed-since-start is shown, but a Codex job cannot be restarted from crowsnest to apply it |
| No folder-trust prompt in a trusted project | yes: a linked worktree inherits its checkout's trust (Claude Code 2.1.332) | yes: Codex resolves a worktree to its repository root's trust (codex-cli 0.167) |

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

A Codex job starts with `--ask-for-approval on-request --sandbox workspace-write` and the sandbox's network on (`sandbox_workspace_write.network_access`: the only way its shell reaches crowsnest's agent socket; a Claude job has the network too, and the guard still refuses protected hosts), and with crowsnest's own hooks (Stop, SessionStart, PreToolUse, PostToolUse and PermissionRequest) handed to that one session with `-c hooks.…` and `--dangerously-bypass-hook-trust`, which runs them without Codex's per-hook review. That flag also runs any hooks the repository itself declares in `.codex/hooks.json` without review, as Claude Code runs a trusted folder's own hooks. Nothing is written into `~/.codex/config.toml` for this. Run `mise run test:codex` to check these against the Codex installed here.
