# Work with Claude's Remote Control

Claude Code's **Remote Control** puts one running Claude session in the Claude app (phone or
desktop) and on claude.ai/code. crowsnest starts the orchestrator and every project lead with it
on, so the same conversation is in both places.

## Which to use when

| | The Claude app (Remote Control) | crowsnest |
|---|---|---|
| Sees | One agent's conversation | Every project, job, question, approval, spend |
| Good at | A long back-and-forth with one agent; the native app and its notifications | The queue across projects; supervision; approvals; landing |
| Blind to | Jobs, other projects, the supervisor | Nothing in the session; its chat is a plainer client |
| Reach | Anywhere | Your tailnet |

In short: crowsnest for the overview, decisions, approvals, many projects and landing; the Claude
app for a long conversation with one agent.

## Open a standing agent in the Claude app

1. Find **Open in Claude**: on Chat's header for the agent you are talking to, on a lead's card on
   its project page, and beside the orchestrator's and running leads' rows on Now.
2. Press it. The Claude app (or claude.ai/code) opens on that session.

The button appears only when crowsnest has read the session's link (from Claude's banner or its
transcript). Otherwise a line says which session name to look for in the Claude app, for example
`Remote Control: find "quark crowsnest" in the Claude app.` The names are:

- the orchestrator: `<instance> crowsnest` (plus the session name when it is not `crowsnest`);
- a lead: `<instance> <project name>`;
- job agents (only with `--job-remote-control`): `<instance> <job id> <title>`.

The **?** beside the button (**Crowsnest or the Claude app?**) shows the table above.

[Open Chat](app:/chat) to find the orchestrator's button.

## Work you give in the Claude app is still a job

When you ask a standing agent for work in the Claude app (or in Chat), it records the work as a
crowsnest job it holds itself, with `job_adopt`, and tells you the job id. So that work is on the
board, supervised, and in your queue like any other.

## Turn it off or on

- Settings → Agents → **Remote Control** (the orchestrator; on by default) applies when the
  orchestrator restarts. `--no-remote-control` does the same from the command line.
- Settings → Agents → **Remote Control for jobs** (`--job-remote-control`; off by default) gives job
  agents a session too; applies when crowsnest restarts.

[Open Settings](app:sheet/settings).

## What can go wrong

- **No Open in Claude button**: the login cannot use Remote Control (an API key, a plan without
  it, an organisation policy). The agent just starts without it; its status says so.
- **A different conversation in the app after New conversation**: a new conversation gets a new
  session link; a restart on the same conversation keeps the old one.
- **Under `--demo`** the links are made up and lead nowhere.
