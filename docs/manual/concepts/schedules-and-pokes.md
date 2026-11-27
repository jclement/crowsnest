# Schedules and pokes

## Pokes

A **poke** is how crowsnest tells a standing agent (the orchestrator or a project's lead) that
something needs its attention. It types one message into the agent's pane:

```text
[crowsnest poke 2026-11-26T03:10Z human=away]
job j_12 (Review the fleet cache refactor) is done: …
question q_7 was answered: …
scheduled task digest is due (every 1d after 17:00, last run …): read <file> and do what it says
```

What makes a poke (its **reasons**):

- another agent settling into done or blocked;
- one of the agent's jobs finishing, getting stuck, or having a question answered or expire (the
  orchestrator for Unfiled work and escalations; a lead for its project: [who gets
  what](projects-and-leads.md#who-gets-poked-for-what));
- a scheduled task coming due;
- the **timer** (`--poke-interval`, every 15 minutes by default), which looks over stuck,
  needs-you and blocked jobs and open questions;
- you pressing **Poke now** (Chat's menu, a project page, the palette).

The rules that keep pokes cheap and safe:

- **Nothing due, no poke.** A poke is built from its reasons first; with none, nothing is typed
  and no tokens are spent.
- **Only when idle with an empty input box.** A poke never interrupts a turn and never mixes into
  something you are typing; it waits for the next quiet moment. A standing agent stuck at a dialog
  raises a needs-you item instead.
- The header's `human=here|maybe|away` tells the agent whether you are watching
  ([Presence](presence.md)).
- Every poke and every skipped poke is in the **Journal** (palette → Journal), and on Chat as a
  folded poke row.
- While the automation is paused, pokes wait; their reasons go with the first poke after.

## Schedules

A **schedule** is recurring work. It has a name, a cadence, a target and a body (what to do, in
markdown).

**Cadence**: **Every N minutes** (at least 5), **Every N hours**, **Every N days** (calendar days,
local time), **Daily at a time**, **With every poke** (rides along with a poke that has something
else to say; never makes one by itself), **When something changes** (the fleet or the jobs changed
since its last run, at most every 5 minutes).

**Target**:

- **The orchestrator**: the body becomes a line of its next poke ("scheduled task X is due: read
  <file> and do what it says");
- **A project's lead**: the same, to that lead;
- **A job**: a crowsnest job starts with the body as its brief, with the full supervisor. A job
  schedule whose previous job is still running waits for it rather than starting a second copy.

A task is marked run when the poke carrying it is delivered, by crowsnest, so it never repeats
because the model forgot, and never runs twice on one calendar day.

### Two kinds: app schedules and task files

- **App schedules** are made and edited in the **Schedule** sheet.
- **Task files** are markdown files with frontmatter (`every:`, `after:`, `as: job`, `dir:`,
  `role:`, `project:`) in `<orchestrator dir>/tasks/` or `~/.config/crowsnest/tasks/`. They are
  read at every check (no restart), and shown read-only "from file". The format is claude-agent's
  front door's, so its task files run unchanged.

Names are shared, and **a file wins**: an app schedule with a file's name is flagged and never
runs.

### Task files that start jobs are reviewed first

Agents can write files, including the tasks directories. So a task file that **starts a job**
(`as: job`) or **pokes a lead** (`project:`) runs only after you approve its exact text: the
Schedule sheet lists it as **new or changed: review**, with the body, until you press **Approve as
it reads now**. Any edit asks again. **Run now** is not an approval. Orchestrator poke tasks and
app schedules need no review. A restore from backup forgets every approval.

### The daily digest

At the top of the Schedule sheet: **Daily digest** (on/off) and its time. The orchestrator writes
the day's digest (what finished, what waits on you, each lead's day, the spend, what is next) into
the Inbox. **Past digests** lists earlier ones; **Digest now** in the palette writes one at once.

## What can go wrong

- **A schedule never runs.** Is it switched on? Is the automation paused? Is it a task file
  waiting for review? Is it shadowed by a file of the same name? The sheet shows each.
- **The orchestrator ignores a poke.** Pokes wait while it is working or has text in its input
  box; the Journal records each skip and why.
- **Pokes are too frequent or cost too much.** Raise the poke interval in Settings, or set a
  per-day ceiling for standing agents (`--orchestrator-ceiling-usd`, `--lead-ceiling-usd`).

Step by step: [Schedule recurring work](../how-to/schedule.md).
