# Create a project and tune its lead

A project gives a repository its own standing lead, its own instructions, defaults for its jobs,
a check command, budgets and approval rules. Concepts: [Projects, leads and the
orchestrator](../concepts/projects-and-leads.md).

## Create one

1. Open **Projects** ([Open Projects](app:/projects)) and press **New project**.
2. **Directory (where its jobs run)**: the repository's main checkout. Pick one of the
   **Remembered directories** (places you have given jobs before) or type a path (`~` works).
3. **Name**: follows the directory's name until you type over it. **Short name** (optional) is
   used in links and in the lead's name, `lead-<short name>`; it never changes afterwards.
4. **Description**: what it is, in a line. The lead reads it.
5. Open **Defaults for its jobs and lead** if you want to change them (below).
6. Press **Create project**.

<!-- STUB(wip/picker): a folder explorer for choosing the directory is being built on
wip/picker. Describe it in step 2 once it is on main. -->

> **Coming soon (not on main yet):** a folder browser for choosing the directory. Today you type
> the path or pick a remembered directory.

## Defaults for its jobs and lead

On **New project**, or **Edit** on the project's page:

| Field | What it sets |
|---|---|
| **Role** | The role a job in this project gets when you do not choose one. |
| **Engine** | Claude or Codex for its jobs. |
| **Model** | The model for its job agents (empty: crowsnest's default). |
| **Own worktree** | **Yes, a worktree per job**, or **No, work in the directory**. |
| **Permission mode** | The lead's Claude Code permission mode. |
| **Start the lead** | **Only when I start it**, **When I open the project (or give it a job)**, or **When the project is given a job for its lead**. |

## The project page

Open a project from **Projects** or a row on **Now**. Top to bottom:

- **Edit** and **Archive** (archived projects take no jobs; their lead stops; **Show archived** on
  the list brings them back into view).
- **The lead**: its state, then **Start lead** / **Stop lead**, **Chat with lead**, **Terminal**
  (its pane in Fleet), **Poke now**, **Restart** (same conversation), **New conversation**, and
  **Open in Claude** when its Remote Control link is known.
- **Jobs**: the project's jobs, with **Board** (the Jobs board filtered to it) and **New job**.
- **Schedules**: its scheduled tasks with **Run now**, and **Manage** (the Schedule sheet).
- **Approval rules** (below).
- **Acceptance and budget**: the check command and per-job budgets (below).
- **Project instructions**: the project's instruction layer, edited in place.
- **Instructions**: what its lead started with, with **Restart with new instructions** when that
  changed.

![A project's page: the lead's controls, its jobs, schedules, approval rules](img/project-1440.jpg)

## Tune the lead

- **What it should know**: write the project's instructions (its gate command, where things live,
  what not to touch) in **Project instructions** and **Save**. The lead and every job agent in the
  project get it. Press **Restart with new instructions** on the Instructions card so the running
  lead picks it up. See [Instruction layers](../concepts/instruction-layers.md).
- **Its model**: leads run on the orchestrator's model (Settings → Agents → **Orchestrator
  model**). The job agents use the project's **Model** default.
- **Its voice** (with talk mode on): account menu → **Voice**, where each standing agent has its
  own voice and Speak switch, so you can tell two leads apart by ear. See [Talk and listen](voice.md).
- **When it runs**: **Start the lead** (above). **Stop lead** keeps it stopped until you start it.

## The check command

On the **Acceptance and budget** card, **Check command**:

- leave it **empty** for automatic: `mise run check` when the repository's mise config has a
  `check` task, else nothing;
- type a command (`make test`, `go test ./...`) to run that, with `sh -c`, in each job's worktree;
- type `none` for no check (jobs finish **Unverified**).

The card says what runs now. Press **Save**. What it means: [Acceptance](../concepts/acceptance.md).

**Budget per job ($)** and **Budget per job (minutes)** on the same card bound each job in the
project ([Budgets](../concepts/budgets-limits-pause.md#budgets)).

## Approval rules

So your phone is not a tap per test run, crowsnest keeps approval rules of its own, per project.
A call a rule matches is allowed at once by the hook, without asking you, and journalled on the
job's timeline. Rules live in crowsnest, not in the agents' settings.

1. On the project page, in **Approval rules**, choose the **Kind**: **Bash command**, **File
   edits**, or **Tool**.
2. **Pattern**: a whole command with `*` for anything (`go test *`), a path (`internal/`), or a
   tool name (`WebSearch`). Every part of a `&&` or `|` chain must match a rule.
3. **Also when elevated**: off by default. Elevated calls push, install, reach the network or
   write outside the job; turn it on only for a pattern you trust that far.
4. **Note (optional)**: why this is safe.
5. Before saving, the card shows what the rule would have matched recently. **Add rule**.

A **dangerous** call is never allowed by a rule. **Remove** deletes one. On an approval card,
**Allow and remember for this job** makes a narrower rule that ends with the job.

## What can go wrong

- **"overlaps another project"**: one live project's directory may not contain another's.
- **The lead does not start**: its folder may be waiting at Claude's trust question; open
  **Terminal** and answer it.
- **Jobs do not join the project**: a job joins only if its directory is inside the project's.
  Give it **For** the project in the job box.
