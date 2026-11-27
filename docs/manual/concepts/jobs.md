# Jobs and their states

A **job** is one piece of work with one owner, a declared state and a result. It is what you
create, watch and get answers from. A chat with an agent is just one way of talking to whoever is
doing a job.

Every job has:

- a **brief** (what you typed; its first line is the title) and, optionally, **done when**;
- a **role** (`builder`, `lead`, `reviewer`, `tester` or one of your own: [Roles](roles.md));
- **where** it runs: a directory, or no directory (then the orchestrator or a project's lead holds
  it in its own conversation); a builder in a git repository gets its own worktree;
- exactly **one agent** holding it, so two agents never do the same work;
- a **state** the agent declares, a one-line **doing now**, and a heartbeat (last activity);
- a **result** at the end: what was done, what was verified, what was not.

## When you use one

Whenever you want something done and want to know it got done. Typing into Chat is fine for a
conversation; giving a job is how you get supervision, a result, a diff to review and a Land
button. Work you give an agent in conversation (in Chat or the Claude app) is recorded as a job
too, when the agent calls `job_adopt`.

## States

The state is on every card, as a chip.

| State | What it means for you |
|---|---|
| **Queued** | Not started yet. Either waiting for a free slot or for the machine to calm down ([held](#held)), or waiting for a standing agent to take it. |
| **Running** | An agent is on it. Nothing for you to do. |
| **Needs you** | Every line of work is waiting on you: a question, an approval, a terminal dialog, a budget, a failed check or review. The card says which, and the job is in your [queue](attention-and-now.md). |
| **Blocked** | Waiting on something that is not you: another job, an outage, a time. It always says what it waits on and when crowsnest will wake it ("rechecks at 07:12"). |
| **In review** | The agent said done. crowsnest is running the project's check, or a reviewer agent is reading the change. |
| **Done** | Finished. For a coding job, look at the acceptance chip beside it (below). |
| **Failed** | The agent declared it cannot be done, or it kept dying (three restarts in an hour). The reason is on the card. |
| **Cancelled** | You stopped it. Its pane stays open. |

A running job can also show **Stuck** instead of Running: the agent is not actually moving. The
card says why and the job view offers the one action that fits. [The supervisor](supervisor.md#stuck-causes)
lists every cause.

### Acceptance on the card

A coding job's **done** is the agent's claim. crowsnest checks it and shows its own verdict beside
the state:

| Chip | Meaning |
|---|---|
| **Checking** | crowsnest is running the project's check command in the job's worktree. |
| **Accepted** | Clean tree, commits, and the check passed at this commit. |
| **Unverified** | Clean tree and commits, but the project has no check command, so nothing was run. |
| **Check failed (round N)** | The check failed; the output went back to the agent to fix (three rounds, then it needs you). |
| **Evidence stale** | A commit came after the check. Run it again before landing. |
| **Needs rebase** | The base branch moved on since the branch started. |

What "accepted" does and does not prove: [Acceptance](acceptance.md).

### Held

A queued job whose card says **Waiting to start** is **held**: every running-job slot is taken
("waiting for a slot: 4 of 4 running"), the machine has been busy for a minute, the automation is
paused, or the fleet hit its usage limit. Held jobs start by themselves, oldest first, when the
reason clears. **Start anyway** on the job starts it now, past the slot limit, the load hold and
the pause. See [Budgets, limits and the pause](budgets-limits-pause.md).

A different hold: a job an agent asked for *outside its own directory* (or with another engine)
waits for your **Allow** in the Inbox before it starts at all. That one reads "Waiting for your
approval: an agent asked for it".

## Holds: what an agent cannot clear by itself

Some reasons a job waits are **holds**, kept apart from the state the agent declares, so an agent
cannot talk its way out of them (by declaring blocked and then running, say). Each says who lifts
it:

| Hold | Lifted by |
|---|---|
| An agent's job beyond its limits, waiting for your Allow | you |
| crowsnest is running the check, or a review round | crowsnest (the result) |
| A budget reached (its own, or a parent's) | you: raise it, let it finish its step, or stop it |
| The check still fails after the last round | you: run it again, land or discard |
| The reviewer still objects after the last round | you: a message, land or discard |
| A tool call waiting for approval, a blocking question open | crowsnest, when you answer |
| The usage limit | crowsnest's paced retry, or you (**Clear hold**) |

Under a hold the agent can still report progress and ask, but its blocked, running, done and fail
are refused, naming the hold and who lifts it.

## Who owns a job

The job view shows its chain from you down, for example "you → lead Crowsnest → tester j_6", each
link clickable, and the jobs it started. A job in a project belongs to that project's lead: the
lead hears when it finishes, gets stuck or asks. See [Projects, leads and the
orchestrator](projects-and-leads.md).

## Where to see jobs

- **Now** shows each project's running count and its most active job's doing-now line.
- **Jobs** ([Open Jobs](app:/jobs)) is the board: **Needs you or stuck** first, then **In
  progress**, then **Finished today**. The row of project buttons above it filters by project;
  **Unfiled** is work in no project.
- Pick a job for its **Overview** (the reason it waits, rescue, the result, the brief, spend,
  instructions, timeline), **Conversation**, **Terminal**, and, once finished in a worktree,
  **Land**.

![The Jobs board: needs you or stuck first, then in progress, then finished today](img/jobs-1440.jpg)

## What can go wrong

- **A done that is not done.** An agent can say done over failing tests. That is why coding jobs
  are checked by crowsnest ([Acceptance](acceptance.md)), and why the result's Verified and Not
  verified lists are labelled "reported by the agent".
- **A job that never starts.** It is held; the card says why. Start anyway, or raise the limit
  ([Settings](../reference/settings.md)).
- **A job with nothing happening.** It is stuck or blocked; see [The agent stopped: what
  now?](../troubleshooting/agent-stopped.md).
- **Settled jobs vanish from the board** after 14 days (`--archive-after-days`) and are deleted
  after 180 (`--delete-after-days`). A finished worktree job that was never landed is held back
  from deletion.
