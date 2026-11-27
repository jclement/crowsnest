# The supervisor: why agents do not silently stop

The problem crowsnest exists to solve: you give an agent a job, it works for a while, then it just
stops, and you find out an hour later. The supervisor is the part of crowsnest that makes an agent
either keep going or say, out loud, why it cannot.

## The rule

An agent holding a job may end its turn only when it has **declared** something:

- **done** (with its result and evidence) or **fail** (with the reason);
- **blocked** on something that is not you, with a wake-up (a time, a delay, or another job);
- **needs you**: it asked a question and has nothing left that the answer does not block.

crowsnest enforces this with Claude Code's own **Stop hook**. When a turn ends with nothing
declared, the hook refuses the stop and types back: you have not finished, continue, or call done,
ask, or job_update with the reason. That is a **nudge**. Agents crowsnest did not give hooks to
(Codex, or a session it did not start) get the same text from a backstop that watches herdr: an
agent idle for 60 seconds with an empty input box while it holds a live job.

Two more refusals, because they are the common ways an agent "stops" while seeming to talk:

- **A question asked in prose.** "Should I disable that lint rule?" at the end of a reply goes
  nowhere: you do not read every reply. The Stop hook refuses it and tells the agent to call
  `ask`, which puts the question in your queue.
- **A done crowsnest cannot accept.** An uncommitted tree, or a worktree with no commits, is
  refused at done, with the files named.

## The budget of three

A nudge counts only when nothing happened since the previous one (no tool call, no progress
line). After **three** nudges in a row with no progress, the stop is allowed, the job is marked
**Stuck: Stopped three times without saying why**, and you are told. An agent that is working
between nudges is never cut off. Claude Code has its own fence too (eight hook continuations in a
row).

Every nudge is on the job's timeline and counted on its card ("3 nudges").

## Stuck causes

A stuck job's card names its cause in one line, and the job view offers the one action that fits
it. "What the agent last said" is shown beside it, so you see the agent's own words.

| The card says | What happened | What crowsnest does | Your action |
|---|---|---|---|
| Stopped three times without saying why | Three turns ended with nothing declared and no work between them. | Stopped nudging; told you. | **Nudge** (one more), or read what it last said and message it. |
| Idle with nothing declared | No activity for 10 minutes and the agent is not working (a hook may be missing). | Nothing more. | **Nudge**. |
| Tool calls keep being denied | Claude's auto mode denied three calls in a row. | Shows the last denial. | **Nudge**, or change the brief or an [approval rule](../how-to/projects.md#approval-rules). |
| Hit a usage or rate limit | Claude's usage limit or a rate limit. | Retries "continue" after 5, 15 and 30 minutes, then hourly; the card shows **Retry in N min**. A subscription limit holds the whole fleet (below). | Nothing, or **Retry now**. |
| The API returned errors | Anthropic's API failed. | Retries after 1, 5 and 15 minutes, then tells you. | **Retry now**. |
| Claude needs a login or billing fix | Sign-in expired, or billing. | Nothing: no retries. | **Open terminal** and sign in (`claude`, then `/login`). |
| Claude refused the request (model or settings) | A bad model name or setting. | Nothing. | **Open terminal**; fix the job's model. |
| Working but silent for 20 minutes | herdr says working, but the transcript has not moved. | Nothing. | **Open terminal** and look; interrupt if it hangs. |
| Busy, but not getting anywhere | 30 tool calls over 10+ minutes, few different, no edits, the same failing tests. | Asks the agent once for a new hypothesis. | **Open terminal**, or message it a different approach. |
| The agent exited | The Claude process left (crash, `/exit`). | Restarts it with `--resume`, up to three times an hour, then fails the job. | **Restart agent**. |
| The agent could not be started | Launch failed (folder trust, a bad directory). | Nothing. | **Open terminal**. |

Needs-you causes are listed with their actions in [Questions, approvals and terminal
dialogs](questions-approvals-dialogs.md) and [Acceptance](acceptance.md).

## The fleet's usage limit

On a Claude subscription a usage limit applies to every agent on the account at once. An agent
that hits it holds its own job with the reset time it reported ("resets 3pm"; when the message
gives none, a guess of 30 minutes, said to be a guess; never more than 24 hours). When two
different jobs report it, crowsnest records one **fleet limit**. Until the reset: new jobs are held, every
turn may end without a nudge, retries wait, and the standing agents are not poked. One banner with
a countdown replaces a stuck card per agent (their cards read **Held**). After the reset, the
waiting jobs get "continue" one at a time, 20 seconds apart. The reset time is read from what
the agent reported, so the banner also has **Clear hold**: if you know the limit has reset (or the
guess is wrong), it forgets the limit and lets the held jobs go, one at a time.

## Waits always have an owner

Every live job says who owns its wait and what ends it, on its card and job view ("Waits on
job-7: rechecks at 07:12"):

- **its agent**: its next turn;
- **crowsnest**: the launch, a free slot, the check, a retry at a time;
- **the reviewer**: a review round;
- **you**: a question, a dialog, a budget, the pause.

A **blocked** job must name its wake-up: a delay, a time, another job settling, or "until a human
says so". crowsnest wakes it then with one line telling it to check whether the block cleared;
after three rechecks of one block you are told. A blocked job's view has **Wake it** to wake it
now. Once a minute crowsnest checks every live job for a wait with no owner (running with no
agent, needs you with no reason, blocked with no wake-up); any it finds are on the job's timeline
and under **System → Waits without an owner**.

## What keeps the thread after a crash or compaction

At every session start (a restart, a resume, a compaction) the SessionStart hook prints the job's
brief, state, progress and its open and answered questions into the agent's context, so it knows
what it was doing. An agent can read the same with `job_get`.

## What can go wrong

- **crowsnest is down.** Every hook prints nothing and exits; the agent carries on unsupervised
  and nothing is wedged. Supervision resumes when crowsnest is back.
- **Paused.** While the automation is paused, the Stop hook lets every turn end, so no agent is
  held against a supervisor that will not act. See [the pause](budgets-limits-pause.md#the-pause).
- **A Codex agent** has no hooks: it relies on the 60-second backstop, and its spend is not
  measured.

[The agent stopped: what now?](../troubleshooting/agent-stopped.md) is the decision tree.
