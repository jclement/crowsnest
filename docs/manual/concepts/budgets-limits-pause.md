# Budgets, limits and the pause

Unattended agents need bounds: on money and time per job, on how many run at once, and a switch
that stops crowsnest's own automation without stopping anyone.

## Budgets

A job may have limits on four measures:

- **estimated dollars** and **tokens** (from Claude's transcripts: see [Spend](spend.md));
- **wall minutes** since the agent started;
- **turns** (each time the agent ends a turn).

Dollars, tokens and turns count **the job and every job below it** (its delegated children, five
levels down). An agent whose spend is not measured (Codex) shows "not measured", never zero, and is
bounded by minutes and turns.

Where a limit comes from, first match: the job's own (set from its view), its project's (the
project page's **Acceptance and budget** card: **Budget per job ($)** and **(minutes)**), then the
server's settings (`--job-budget-usd`, `--job-budget-tokens`, `--job-budget-minutes`,
`--job-budget-turns`; all off by default).

### Budget states

| On the card | What happened | What crowsnest does |
|---|---|---|
| **Budget 81%** (neutral from 50%, warning from 80%) | It has used that share of a limit. | At 80%, one line to the agent: plan to finish within it. |
| **Over budget** | It reached a limit. | The job **needs you** ("Reached its budget") and the agent is told to stop at a safe point: finish the step, commit what is safe, report. Nothing is killed and no tool is interrupted. |
| **Finishing its step** | You chose **Let it finish this step**. | It runs on; at its next stop it comes back to you. |

What you can do on the job's **Budget** panel when it is over: **Raise the budget** (it goes back
to running and is told), **Let it finish this step**, or **Stop the job** (cancel).

### Ceilings for the whole fleet

- `--daily-ceiling-usd`: once today's estimated spend reaches it, new job starts are held (like a
  slot hold; **Start anyway** passes it).
- `--orchestrator-ceiling-usd`, `--lead-ceiling-usd`: per day; past it, that standing agent's
  automatic pokes wait. **Poke now** still works.

## Limits on running jobs

The machine once hit a load of 63 with three agents running test suites at once. So:

- **Running jobs at once** (`--max-running-jobs`, default a quarter of the cores, at least 2):
  more jobs wait **queued**, held, and start in order. Leads, the orchestrator and jobs in review
  are not counted.
- **Hold starts above load per core** (`--load-hold`, default 2): when the one-minute load per
  core stays above it for a whole minute, new starts wait. Running work is never touched.
- **Heavy command slots** (`--heavy-slots`, default 2): builds, test suites, typechecks and
  crowsnest's own checks run through `crowsnest heavy`, at most that many at once; the rest wait
  for a slot. If another `heavy` is on your `PATH`, `crowsnest heavy` hands commands to it, so a
  machine has one set of slots.

The board's "13 of 32 running · 1 waiting" button opens **Limits**: running and waiting jobs, the
load, and who holds each heavy slot. **Change in Settings** goes to the settings. A held job's
**Start anyway** starts it past the slot limit, the load hold and the pause.

## The usage-limit hold

When a Claude subscription's usage limit is hit, every agent on the account is limited at once.
crowsnest records one fleet-wide limit with its reset time and holds new starts, nudges, retries
and pokes until then, with one banner and a countdown. Details: [the
supervisor](supervisor.md#the-fleets-usage-limit).

## The pause

One switch that stops crowsnest's own automation **without stopping any agent**.

While paused, crowsnest does **not**: poke the orchestrator or leads, nudge, send backstop
prompts, compact, start scheduled jobs, start new jobs or hand them to a lead, restart agents, or
retry. Each held thing is journalled once. The Stop hook lets every turn end, so no agent loops
against a supervisor that will not act.

It still records hooks, takes questions and approvals, and lets you chat, answer, message, nudge,
retry, restart, **Poke now** and **Start anyway**.

How:

1. Account menu → **Pause automation…** ([Open the pause sheet](app:sheet/pause)), or the palette.
2. Optionally **Resume by itself at (optional, HH:MM)** and a **Reason (shown on the banner)**.
3. **Pause**. A banner on every view says who paused it, why and until when, with **Resume**.

The pause survives restarts. From a shell: `crowsnest pause [--until HH:MM] [--reason …]`,
`crowsnest pause --status`, `crowsnest resume`. With the server down, `crowsnest pause` writes the
file `~/.config/crowsnest/paused`, which pauses it at the next start. A restore from backup starts
paused on purpose.

## When to use which

- **Budgets** for unattended work you want bounded: set a project's per-job dollars or minutes.
- **Running jobs at once** and **heavy slots** when the machine is struggling.
- **The pause** before a deploy window, while you work in the repositories by hand, or when
  something is going wrong and you want crowsnest to stop acting while you look.
- **Do not disturb** (account menu) when you want quiet, not a stop: see [Presence](presence.md).

## What can go wrong

- **Nothing starts.** Look for the pause banner, the usage-limit banner, or a held card's reason.
- **A job stopped at its budget mid-task.** That is the safe point; raise the budget or let it
  finish its step.
- **Builds are slow.** Two heavy slots is deliberate; raise **Heavy command slots** only if the
  machine has room.
