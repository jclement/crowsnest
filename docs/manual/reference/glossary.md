# Glossary

**Accepted**: a coding job whose done crowsnest checked itself: clean tree, commits, and the
project's check command passed at that commit. [Acceptance](../concepts/acceptance.md)

**Approval**: a tool call (Bash, an edit, a URL, an MCP call) waiting for your **Allow once** or
**Deny**, caught by crowsnest's PermissionRequest hook. [Questions, approvals and
dialogs](../concepts/questions-approvals-dialogs.md)

**Approval rule**: a per-project rule that allows matching calls without asking ("Allow Bash
commands matching `go test *`"). Never allows a dangerous call. [Projects](../how-to/projects.md#approval-rules)

**Attention queue**: the one list of everything waiting on you; every badge and count reads it.
[The attention queue and Now](../concepts/attention-and-now.md)

**Backstop**: the herdr-side nudge for agents without hooks (Codex): idle 60 seconds with an empty
input box while holding a live job.

**Blocked**: a job waiting on something other than you, with a wake-up crowsnest will act on.

**Blocking** (an item): work has stopped until you act. The other band is **can wait**.

**Brief**: what you typed when giving a job; its first line is the title.

**Ceiling**: a per-day spend limit for the fleet (`--daily-ceiling-usd`) or for standing agents.

**Check command**: the project's command crowsnest runs to accept a job (`mise run check` when
there is one). [Acceptance](../concepts/acceptance.md)

**Decision packet**: one screen for one question, approval or dialog, with the answer bar at the
bottom (`/decide`).

**Dialog**: Claude's own on-screen menu in a pane (a permission prompt, folder trust); crowsnest
shows it and presses the key you choose.

**Agents** (the view): every workspace, tab, pane and agent in the herdr session, and the session
terminal. **Fleet**: what it lists; also the old name of its tree view.

**Gate**: a question that grants something (land, allow a wider job, approve a task file). Never
answered from a notification. [Safety](../safety.md)

**Guard**: the local check before every Bash call that refuses a few commands outright (protected
hosts and branches, stopping herdr).

**Heavy command**: a build, test suite or typecheck run through `crowsnest heavy`, which limits how
many run at once.

**Held**: a queued job waiting to start (slots, load, pause, usage limit); also a delivery receipt
(the agent could not take the answer yet), and a stuck card's label while the fleet's usage limit
holds everyone.

**Instruction layers**: global, role, project and job instructions crowsnest adds to every agent.
[Instruction layers](../concepts/instruction-layers.md)

**Job**: one piece of work with one agent, a declared state and a result. [Jobs](../concepts/jobs.md)

**Land**: merge, push and open a pull request, or discard a finished worktree job; always yours.
[Worktrees and landing](../concepts/worktrees-and-landing.md)

**Lead**: a standing Claude for one project, in its directory, owning its jobs.
[Projects, leads and the orchestrator](../concepts/projects-and-leads.md)

**Needs you**: a job state: every line of work is waiting on you.

**Nudge**: the supervisor sending a stopped agent back to work; three without progress make it
stuck. [The supervisor](../concepts/supervisor.md)

**Orchestrator**: the front-door standing Claude; holds Unfiled work, routes across projects.

**Pause**: the switch that stops crowsnest's own automation without stopping any agent.
[The pause](../concepts/budgets-limits-pause.md#the-pause)

**Poke**: crowsnest typing `[crowsnest poke …]` with reasons into an idle standing agent.
[Schedules and pokes](../concepts/schedules-and-pokes.md)

**Presence**: here, maybe or away; decides where notices go. [Presence](../concepts/presence.md)

**Receipt**: how far an answer got: stored, held, delivered, received (or uncertain, not
delivered).

**Remote Control**: Claude Code's feature that puts a session in the Claude app; on for the
orchestrator and leads. [Remote Control](../how-to/remote-control.md)

**Review gate**: a reviewer agent reading a builder's change after the check passes.

**Role**: a reusable prompt: builder, lead, reviewer, tester, or your own. [Roles](../concepts/roles.md)

**Schedule**: recurring work, an app schedule or a task file. [Schedules](../concepts/schedules-and-pokes.md)

**Since you left**: what finished, failed or was asked since this browser was last here.

**Snooze**: hide an item for ten minutes on every device; decides nothing.

**Spend**: tokens and an API-equivalent cost estimate from the transcripts; not a bill.
[Spend](../concepts/spend.md)

**Standing agent**: the orchestrator or a lead: long-running, poked, not a job agent.

**Stuck**: a running job that is not moving; the card names the cause.

**Supervisor**: the part of crowsnest that refuses silent stops and surfaces stuck work.

**Sweep**: the list of finished worktree jobs never landed (**Worktrees still around**).

**Tray**: the queue with quick answers on every view but Now and the Inbox: docked at the right on
a desktop, a bar at the bottom on a phone.

**Unfiled**: jobs in no project; the orchestrator's.

**Unverified**: done with a clean tree and commits, but the project has no check command.

**Worktree**: a separate git checkout on its own branch, made by herdr for a builder job.
