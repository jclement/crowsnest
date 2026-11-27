# Every view

On a phone the bottom bar is **Now**, **Projects**, **Inbox**, **Chat** and **More**; More holds
**Jobs**, **Agents**, **Help** and **System**. On a desktop the left rail shows the
same, with the More views under a heading. The account menu (top right) holds **System**,
**Settings**, and two switches, **Pause automation** and **Do not disturb** (and **Sign out** with a login).

## Now

`/` · `g` then `h` · [Open Now](app:/)

Home. **Needs you** (the attention queue, answerable in place), **Projects** (one row per project,
then Unfiled and the Orchestrator), **Since you left**, and **Give a job**. See [The attention queue
and Now](../concepts/attention-and-now.md).

## Projects

`/projects` · `g` then `p` · [Open Projects](app:/projects)

The list of projects, each with its lead's state and **Start**/**Stop**, its needs-you and running
counts; **Show archived**; **New project**. A project's page (`/projects/<slug>`) has the lead's
controls, its jobs, schedules, approval rules, acceptance and budget, and instructions. See [Create
a project and tune its lead](../how-to/projects.md).

## Inbox

`/inbox` · `g` then `i` · [Open the Inbox](app:/inbox)

The attention queue in full (**Open**), **History**, and **Notices**. List and detail side by side
on a desktop; a decision packet per item on a phone. See [Answer questions and
approvals](../how-to/answer.md).

## Chat

`/chat` · `g` then `c` · [Open Chat](app:/chat)

A conversation with the orchestrator or a project's lead (**Chat with** at the top;
`/chat?target=lead-<slug>` for a lead). Replies render as formatted text with tool calls folded;
**Open in Claude** when its Remote Control link is known; the **Conversation menu** has its spend,
**Compact now**, **Poke now**, **Journal**, **Schedule**, **Instructions**, **Show thinking**,
**Restart** and **New conversation**. The composer sends with Enter on a wide screen (Shift+Enter
for a new line), takes attachments, and has the mic with talk mode on.

![Chat with the orchestrator](../img/chat-1440.jpg)

## Jobs

`/jobs` · `g` then `j` · [Open Jobs](app:/jobs)

The board, a list. The project buttons filter it (All, each project, Unfiled). **New job** opens
the new-job form (the same as Now's job box) in a sheet; `/jobs?new=1` opens it too. Spend totals,
the running count (opens **Limits**), **N to land**, **Schedule** and **Journal**. Then **Needs
you or stuck**, **In progress**, **Finished today**. A job (`/jobs?job=j_12`) has **Overview**,
**Conversation**, **Terminal** and, for a worktree job, **Review**/**Land**. See [Jobs and their
states](../concepts/jobs.md).

## Decision packet

`/decide?item=…`

One question, approval or dialog on one screen, with the answer bar at the bottom. Opened from the
tray, the Inbox on a phone, the palette and notifications. See [The decision
packet](../how-to/answer.md#the-decision-packet).

## Changes (the diff viewer)

`/diff?job=j_12` or `/diff?project=<slug>` · from a job's Review, Land or Changes tab, a project's
Changes card, the lead's **Changes** button, or the palette's **Changes: …**

A job's or project's changes, live and syntax-highlighted, with **Viewed** marks. Full screen
here: no header, nav or tray; **Back** or Esc returns. See [Review changes with the diff
viewer](../how-to/review-changes.md).

## Agents

`/agents` · `g` then `a` · [Open Agents](app:/agents)

The recovery and debugging view, under **More**. At left every herdr workspace, tab and pane, with
each agent's state (working, idle, done, blocked at a dialog), filterable with `/`. Pick an agent
for its **Conversation** or **Terminal**; plain shell panes open too. With nothing picked, the
right side is the **Session terminal**, herdr's own TUI for the whole session (on a phone, the
**Session terminal** row at the top opens it; `/agents?session=true`). The old Fleet and Terminal
views were merged into it: `/fleet` and `/terminal` links land here. See [Use the terminal
views](../how-to/terminals.md).

## Help

`/help` · `g` then `m` · [Open Help](app:/help)

This manual: the contents and a search on the left (a **Contents and search** sheet on a phone),
the page with its headings, previous and next. `/help/<page>#<heading>` links straight to a
heading. A **?** beside a view's title or a card opens a section in a side sheet instead.

## System

`/system` · [Open System](app:/system)

The doctor's report from the running server (**Re-check**, **Copy diagnostics**), versions, the
agents and which runs what, the running instance, paths, the machine, recent warnings and errors,
every check by group, settings not at their default, **Waits without an owner** and **Machines**.
It is live status; configuration is in [Settings](#settings). See [Doctor
checks](../troubleshooting/doctor.md).

## Settings

`/settings` · [Open Settings](app:/settings)

The one place to configure crowsnest (it replaced the separate Settings, Appearance, Account,
Notifications, Voice, Instructions and Limits sheets). A section list at the left (at the top on a
phone), then four groups:

- **This device**: **Appearance** (theme and mode), **Notifications**, **Voice** (with talk mode).
- **This instance**: **Account and name**, **Limits** (running jobs, load, heavy slots, and the
  throttling and review settings), **Budgets, upkeep and notices**, **Backups and storage**
  (**Download backup**, **Clean up now…**), **Tailnet** (publish to your tailnet, sign in with
  Tailscale), **System** (a link to the System view).
- **Agents**: **Agent settings** (models, engine, permission mode, Remote Control), **Instructions
  and roles**, **Schedules**.
- **Safety**: **Approval rules** (kept per project), **Protected hosts and branches**, **Pause and
  do not disturb** (switches in the account menu).

Each section has its own link, `/settings#<section>` (for example `/settings#limits`); the palette's
Settings, Appearance, Notifications, Instructions, Voice and Limits items land on theirs.

## Sheets

Opened from menus and the palette, over the current view:

| Sheet | What | From |
|---|---|---|
| **Schedule** | Scheduled tasks and the daily digest | Jobs, Chat's menu, palette; [Open Schedule](app:sheet/schedule) |
| **Spend** | Estimated spend by job, model or day | Jobs' spend button, palette; [Open Spend](app:sheet/spend) |
| **Journal** | Every poke, skip, send, answer, restart and alert | Jobs, Chat's menu, palette; [Open the Journal](app:sheet/journal) |
| **Worktrees still around** | Finished worktree jobs to land or discard | Jobs' **N to land**, palette; [Open it](app:sheet/worktrees) |
| **Pause automation** | Pause with an optional resume time and reason | The account menu's switch, palette; [Open it](app:sheet/pause) |
| **Digests** | Past daily digests | Schedule, palette; [Open Digests](app:sheet/digests) |
| **Keyboard shortcuts** | Every shortcut | `?`; [Open it](app:sheet/shortcuts) |

## The header

The instance's name and session, the **machine strip** (CPU with a one-minute sparkline, memory,
load; from wide screens free disk and working agents; on a phone one chip that opens the figures),
**Search or run…** (the command palette), the connection state, **Speak replies** (with talk
mode), the theme switch and the account menu.

The theme and mode: **Settings** → **Appearance**, the palette, or `T` for the next theme. See
[Choose a theme](../how-to/appearance.md).