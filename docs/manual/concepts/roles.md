# Roles

A **role** is a reusable prompt that says what kind of agent a job gets. It is added to the agent's
system prompt (Claude's `--append-system-prompt-file`), with the job protocol after it.

## The four built in

| Role | What it does | Default for |
|---|---|---|
| **builder** | Implements the job in its directory (usually a fresh worktree on its own branch), tests it, commits, leaves a clean tree, and reports evidence: what it ran and saw pass, and what it did not verify. Never pushes or merges: landing is yours. Writes a walkthrough of the change for your review. | A job given with a directory |
| **lead** | Plans, delegates to child jobs, verifies, and is never the only reviewer of its own work. | A job with no directory (the orchestrator or a lead holds it) |
| **reviewer** | Read-only: reads the change against its intent and reports `VERDICT: approve` or `VERDICT: changes` with findings. Never edits, commits or switches branches. | The review gate's reviewer |
| **tester** | Drives the real thing and reports what it saw. | Nothing by default |

Every role ends with the same rules: report with the job tools, run builds and test suites through
`crowsnest heavy`, and ask with `ask(wait=true)` before anything irreversible or expensive.

## When you choose one

Usually you do not: the default fits. Choose one in the job box's options (press the summary button
under **What needs doing?**, which reads, for example, **To the orchestrator**; then **Role**) when you want, say, a tester to try the app, or a reviewer to
read someone else's branch. A project can set its own default role (**Edit** on the project page).

## Changing a role

1. Open **Instructions** (account menu → Instructions, or [Open Instructions](app:sheet/instructions)).
2. Pick the role under **Roles** (each says built-in, edited or from file). Edit its text on the
   **Edit** tab; **Preview** shows it rendered; **Diff vs built-in** shows what you changed.
3. **Save**. Every save is a revision (the newest 20 are kept); the **History** tab lists them,
   each with its **Diff** and **Restore**.
4. **Reset to built-in** goes back to the text crowsnest ships.

A change applies to agents started after it. An agent already running keeps what it started with;
its Instructions card says "Instructions changed since start" and offers **Restart with new
instructions**.

## Roles from files

A file `~/.config/crowsnest/roles/<name>.md` adds a role, or replaces a built-in one of the same
name. A file always wins over text saved in the app, so a role kept in a dotfiles repository is
never silently overridden from a phone. In the app such a role is read-only, marked "from file"
with its path, and flagged **from file: changed, review** until you mark that exact text read. New
roles are made only as files. Files are re-read at every start; no restart needed.

## What can go wrong

- **An edited role does not take.** The agent was started before the edit; restart it with new
  instructions, or wait for the next job.
- **Your edit is ignored.** A file with that role's name exists; the app shows its path. Edit the
  file.

See also [Instruction layers](instruction-layers.md), which explains the global and project
layers that wrap a role.
