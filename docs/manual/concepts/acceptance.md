# Acceptance: what "accepted" proves

An agent saying "done" is a claim. For a coding job, crowsnest checks the claim with its own
evidence before the job counts as done, so a board full of "done" means something.

## Which jobs are checked

A job whose agent works in a git checkout (its own worktree, or its directory). Not reviewers,
not rebase jobs (their parent is checked instead), not the orchestrator or leads, and not jobs with
no directory or outside git: those keep a plain done.

## What happens at done

1. **Before anything is recorded**, the tree must be clean (no uncommitted or untracked files)
   and a job in its own worktree must have commits, or say why not. Otherwise `done` is refused
   with the files named, and the agent fixes it. Uncommitted work is lost work.
2. **The check.** crowsnest runs the project's **check command** itself, in the job's worktree, as
   a heavy command (it waits for a slot like any build), with `CI=1`, without crowsnest's or your
   credentials in its environment, cut off after 30 minutes (`--check-timeout`). The job is **In
   review** with **Checking** meanwhile. **Only committed work counts**: the tree must be clean
   when the check starts, and if it is dirty or HEAD moved when the check ends (the agent, or the
   check itself, changed what was tested), the run is not evidence and runs again (three times in a
   row at most, then it needs you). It runs in the worktree, not in a fresh copy, because a fresh
   copy would lack the ignored files a check needs (`node_modules`, a virtual environment, build
   caches). **Run the check again** needs a clean tree too. A result crowsnest could not record is
   not evidence either.
3. **Pass** → **Accepted**. If the review gate is on, a reviewer then reads that exact revision;
   otherwise the job is done.
4. **Fail** → back to **Running** with **Check failed (round N)**, and one line typed to the agent:
   the command, the exit code, the commit, where the full output is, and its last line, asking it
   to fix, commit and call done again. After three failed rounds (`--check-rounds`), or a check
   that could not run at all, the job **needs you**: "The check failed: crowsnest did not accept
   it", with **Run the check again**.

**No check command** → **Unverified**: done, with a clean tree and commits, but nothing was run.
It is shown apart from Accepted on purpose.

## The check command

Set on the project page's **Acceptance and budget** card, **Check command**:

- **empty** (automatic): `mise run check` when the checkout's mise configuration has a `check`
  task; otherwise nothing;
- **a command**: run with `sh -c` in the worktree;
- **`none`**: no check; jobs finish as Unverified.

The card says what runs now, for example "Runs now: nothing (no mise check task …). Jobs with no
check finish as unverified."

## What "accepted" proves, and what it does not

**It proves:** at that commit, the tree was clean, the branch had commits, and the project's own
check command exited 0 when crowsnest ran it, not when the agent said it did.

The job view says it plainly under the check: "Accepted means the project's own check passed on
this commit, in a clean tree. The branch can change that check, so it is not independent evidence:
the reviewer and you are the independent part." When the branch changed its own check definition
(its mise tasks, its test configuration), the panel names those files: then the pass is the
branch's word about itself, and you should read those changes.

**It does not prove:**

- that the change does what you asked: the check knows nothing of the brief;
- anything the check does not run (a check that only lints proves only lint);
- anything about a later commit: any commit after the check makes the evidence **stale**, and the
  job view says so;
- that the base has not moved: if it has, the job shows **Needs rebase**, and Land refuses until
  it is rebased or you override;
- in the default mode, that nothing touched the files during the run. The check runs in the job's
  own working tree, which has the dependencies and caches a check needs. crowsnest checks the tree
  is clean, with the same content, at the start and the end, and holds the agent's tools while it
  runs. But a process the agent left running could change a file and put it back in between.

## Where the check runs

The job's evidence says which: **checked in the working tree** (the default) or **checked in an
isolated copy of** a commit. Turn on **Isolated check** on a project's Acceptance card to have
crowsnest check a temporary copy of exactly the commit instead, made for the check and removed
after it. Nothing the agent changes or leaves running reaches that copy. The copy has no ignored
files (no `node_modules`, virtual environment or build cache), so give a **prepare command**
(`npm ci`, `uv sync`) that crowsnest runs in the copy first. If the copy cannot be made, or the
prepare command fails, the check is an error that says so; it is never quietly run in the working
tree instead.

The result's **Verified** and **Not verified** lists are labelled "reported by the agent": they are
its account, not crowsnest's evidence. Read **Not verified** before you land.

## Where you see it

- The job's Overview, beside its state: **Checking**, **Accepted**, **Unverified**, **Check failed
  (round N)**, **Evidence stale**, **Needs rebase**. The card folds it into its one chip:
  **Ready to land**, **Fixing check**, **Check failed**, **Re-check to land**, **Needs rebase**.
- The job view's **crowsnest's check** panel: the verdict, the command, the commit, the exit code
  and duration, the end of its output, **Run the check again**, and **Ask an agent to rebase** when
  needed.

## The review gate

For builder jobs in a worktree, after the check passes, a **reviewer** agent reads the change on
that revision and reports `VERDICT: approve` or `VERDICT: changes`. Changes go back to the builder
for a round; if the reviewer still objects after the last round, or the review could not run, the
job **needs you** ("The review needs you", **Open the review**). The `--review` setting chooses:
`auto` (the other engine when installed, else the same), `same`, `claude`, `codex`, or `off`.

## What can go wrong

- **The check passes locally but fails here.** It runs with `CI=1` and without your shell's
  environment; make the check command self-contained (a mise task is ideal).
- **Every job is Unverified.** The project has no check command; set one.
- **The check takes a slot and waits.** Checks share the heavy-command slots with builds; see
  [Budgets, limits and the pause](budgets-limits-pause.md).
