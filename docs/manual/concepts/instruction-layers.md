# Instruction layers: what an agent actually loads

You keep standing instructions in the app; crowsnest adds them to every agent it starts. It never
edits your `CLAUDE.md`, `AGENTS.md`, rules or skills: those still load exactly as Claude loads
them. crowsnest's layers travel in the prompt file it already writes for each agent, so nothing
lands in a repository or in your own Claude settings.

## The layers, in order

| Layer | Who gets it | Where you edit it |
|---|---|---|
| **Global** | Every agent crowsnest starts: job agents, leads and the orchestrator | Instructions → **Every agent** |
| **Role** | Job agents of that role | Instructions → **Roles** ([Roles](roles.md)) |
| **Project** | The project's lead and every job agent in the project | Instructions → **Projects**, or **Project instructions** on the project page |
| **Job** | That job's agent: the brief, as a file it is told to read | The job box |

Empty layers are left out. A standing agent (orchestrator or lead) gets global and project only:
its own instructions template already says what it is.

## What an agent actually loads

So, for a builder job in project Crowsnest, the agent's context holds, from crowsnest:

1. your **global** layer;
2. the **builder** role (built-in, your edit, or your file);
3. the **Crowsnest** project layer;
4. crowsnest's job protocol (the job tools, the rules about done, asking and heavy commands);
5. the brief (`brief.md` in the job's directory under crowsnest's state), which the first prompt
   tells it to read.

and, from Claude Code itself, as usual: your user `~/.claude/CLAUDE.md`, the repository's
`CLAUDE.md` files and `.claude/rules`, your skills and settings.

## Seeing it on an agent

Every job, every lead and the orchestrator (Chat's menu) has an **Instructions** card:

- **Layers at start**: each layer it started with, with its revision and size. These are recorded
  at every start, restarts included.
- **Files Claude loaded**: each `CLAUDE.md` or rules file Claude reported loading (by path, memory
  type and reason; crowsnest never reads or serves their text).
- A note that **skills and slash commands are not listed**: Claude Code reports them to no hook.
- **Instructions changed since start**, when a layer it started with has changed since (a global
  edit, a role reset, an edited role file), or, for the orchestrator and leads, when crowsnest's
  own prompt file, hooks and settings changed (an upgrade). **Restart with new instructions**
  restarts it now, after a confirmation.

## When instructions change

- **The orchestrator and leads pick them up by themselves.** crowsnest checks every 30 seconds
  (and when it adopts a running agent after its own restart) whether what it would give the agent
  now differs from what it started with. If so, it restarts the agent **on the same
  conversation** at its next quiet moment: idle, nothing typed in its input box, no dialog on
  screen, not in the middle of a held job. At most once every 10 minutes, never while the
  automation is paused, and journalled as a restart for new instructions. A Claude conversation
  that is resumed keeps the system prompt it began with, so crowsnest also hands the agent its
  current instructions in full once, as the restart's start-up context, saying they replace the
  old ones.
- **Turn it off** with Settings → Orchestrator upkeep → **Restart for new instructions**
  (`--no-instruction-restart`; applies at once). Then the Instructions card's banner and **Restart
  with new instructions** are how they update, as for jobs.
- **Job agents are never restarted by themselves**: a restart mid-job would lose its turn. A job
  keeps what it started with; restart it from its Instructions card if it matters, or let the next
  job get the new text.

## When you use it

- **Global**: how you want every agent to behave ("write for a phone: lead with the answer";
  "never push to main").
- **Project**: what every agent in that repository must know (its check command, where
  screenshots go, what not to touch).
- **Role**: how a kind of work is done (what a builder reports, what a reviewer reads).

Keep repository facts in the repository's own `CLAUDE.md` when you want them for every Claude
session, crowsnest's or not; use the project layer for what only crowsnest's agents need.

## How

1. Open **Instructions** ([Open Instructions](app:/settings#instructions)), or the project page's
   **Project instructions** card.
2. Pick the layer, edit on **Edit**, check **Preview**, **Save**.
3. **History** keeps the newest 20 revisions with their diffs; **Restore** saves an old one again
   as a new revision. Saving empty text clears a global or project layer.

A layer holds at most 32 KB.

## What can go wrong

- **A running job agent does not follow a new instruction.** It started before the edit. Restart
  it with new instructions from its Instructions card.
- **A lead or the orchestrator still has the old text.** It restarts only when idle with an empty
  input box; it may be busy, at a dialog, paused, or within 10 minutes of its last such restart.
  Or **Restart for new instructions** is off in Settings. Restart it from its Instructions card.
- **Something you did not write shows up.** Check **Files Claude loaded**: a `CLAUDE.md` in the
  repository or your home directory is loaded by Claude, not crowsnest.
