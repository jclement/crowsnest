# Projects, leads and the orchestrator

## Projects

A **project** is a directory (usually a git repository's main checkout) with a name, a short name
(its slug, used in links and in the lead's name `lead-<slug>`), a description, and defaults for
its jobs: role, engine, model, whether each job gets its own worktree, and the lead's permission
mode. A directory belongs to at most one live project: crowsnest refuses one inside another, and
refuses `/`, your home directory and anything above it.

A job joins a project by itself when it runs in a directory inside the project. Jobs in no project
are **Unfiled**. Projects are archived, never deleted: an archived project takes no new jobs and
its lead is stopped.

## Leads

A **lead** is a standing Claude for one project, running in the project's directory in its own
herdr workspace (`lead <slug>`). It is the same kind of agent as the orchestrator: same
instructions template (saying "you are the lead of project X"), same hooks and tools, same
recovery, its own Remote Control session named "`<instance> <project>`".

A lead:

- holds jobs given **to the lead** (the default when you give a project a job with no directory);
  it may do them itself or delegate them to job agents inside the project's directory, without
  asking you;
- **owns every job in its project**: it is poked when one finishes, gets stuck or asks, and when a
  question one of them asked is answered;
- gets the project's scheduled tasks;
- needs your approval for anything wider than its project (another directory, another engine).

When a lead starts is the project's **Start the lead** setting: **Only when I start it** (the
**Start lead** button), **When I open the project (or give it a job)** (the default), or **When
the project is given a job for its lead**. A lead you stop stays stopped across crowsnest
restarts.

## The orchestrator

The **orchestrator** is the front door: one standing Claude in the `orchestrator` workspace, in
crowsnest's own directory (`--dir`). It:

- holds and runs **Unfiled** work (jobs given with no project and no directory);
- hands work to projects (it can list projects and create a job for one);
- hears about a project's jobs only as **outcomes of work it handed over** and as
  **escalations**: a project with no lead running, a job stuck or needing you for 30 minutes with
  no change, a lead that failed.

## Who gets poked for what

The escalation ladder is: job agent → its lead → the orchestrator → you.

| Something happens | The project's lead (if running) | The orchestrator |
|---|---|---|
| An Unfiled job settles, gets stuck, or its question is answered | (none) | told |
| A project job settles, gets stuck, or its question is answered | told | not told |
| A project job the orchestrator handed over settles | told | told the outcome |
| A project job event while the project has no lead running | (none) | told, "project X has no lead running" |
| A project job stuck or needing you for 30 minutes with no change | already knew | one escalation |
| A lead fails or needs its terminal | (none) | told |

Every row on Now ends with the sentence saying which of these applies, for example "Owns every job
in its project: told when one finishes, gets stuck or asks; the orchestrator hears only what the
lead could not resolve", or "No lead is running: the orchestrator hears about this project's jobs
until one is started".

A **poke** is how a standing agent is told: crowsnest types `[crowsnest poke <time>]` and one line
per reason into its pane, only when it is idle with an empty input box. See [Schedules and
pokes](schedules-and-pokes.md).

## When to use which

- Talk to **the orchestrator** (Chat) for work across projects, for quick one-off jobs with no
  repository, and for "what is going on?".
- Talk to **a project's lead** (Chat with the lead selected, or **Chat with lead** on the project
  page) when the work is in that project: it knows the project's instructions and its jobs.
- **Give a job** directly ("As a job") when you know exactly what one agent should do in that
  directory and do not need the lead to plan it.

## What can go wrong

- **A lead is off.** A job given to it stays queued until the lead runs (with **When the project
  is given a job for its lead** it starts the lead), and the orchestrator hears this project's
  events instead. Press **Start lead**.
- **A lead hit its usage limit or crashed.** It is restarted with `--resume` up to three times an
  hour; after that it shows as failed and the orchestrator is told.
- **Overlapping directories.** Making a project inside another project's directory is refused;
  choose the outer one, or archive it first.

How to make and tune one: [Create a project and tune its lead](../how-to/projects.md).
