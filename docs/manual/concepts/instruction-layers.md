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
  edit, a role reset, an edited role file). The agent keeps the old text until it restarts;
  **Restart with new instructions** does that, after a confirmation.

## When you use it

- **Global**: how you want every agent to behave ("write for a phone: lead with the answer";
  "never push to main").
- **Project**: what every agent in that repository must know (its check command, where
  screenshots go, what not to touch).
- **Role**: how a kind of work is done (what a builder reports, what a reviewer reads).

Keep repository facts in the repository's own `CLAUDE.md` when you want them for every Claude
session, crowsnest's or not; use the project layer for what only crowsnest's agents need.

## How

1. Open **Instructions** ([Open Instructions](app:sheet/instructions)), or the project page's
   **Project instructions** card.
2. Pick the layer, edit on **Edit**, check **Preview**, **Save**.
3. **History** keeps the newest 20 revisions with their diffs; **Restore** saves an old one again
   as a new revision. Saving empty text clears a global or project layer.

A layer holds at most 32 KB.

## What can go wrong

- **A running agent does not follow a new instruction.** It started before the edit. Restart it
  with new instructions from its Instructions card.
- **A lead and its jobs disagree.** The lead started before the project layer changed; its jobs
  started after. Restart the lead.
- **Something you did not write shows up.** Check **Files Claude loaded**: a `CLAUDE.md` in the
  repository or your home directory is loaded by Claude, not crowsnest.
