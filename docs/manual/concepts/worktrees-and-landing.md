# Worktrees and landing

## Worktrees

A builder job in a git repository runs in its own **worktree**: herdr makes a new workspace with a
fresh checkout on a new branch (`job/<n>-<words from the title>`), beside the main checkout, and
the agent works there. So several agents can change the same repository at once without stepping
on each other, and your main checkout is never touched while they work.

Whether a job gets a worktree:

- a **builder** with a directory in a git repository: yes, by default;
- other roles, or a project whose **Own worktree** default is **No, work in the directory**: no,
  the agent works in the directory itself;
- a job with no directory: there is nothing to isolate; the orchestrator or a lead holds it.

At launch crowsnest records the job's **base**: the branch the main checkout was on, and the
worktree's first commit. Everything below compares against that.

## Landing

When a builder says done in its worktree, its branch and worktree are still there. **Landing** is
the last step, and it is always yours:

- **Merge** merges the branch into the base in the main checkout, as a merge commit
  `Merge job j_N: <title>` (or fast-forward only, with the `--merge ff` setting).
- **Push and open PR** pushes the branch and, when the remote is on GitHub and `gh` is signed in,
  opens a pull request with the walkthrough as its body (no reviewers or assignees are set). Other
  hosts are pushed, and the result says to open the request there.
- **Discard** throws the branch away.

Each asks first, in words that say exactly what will happen, naming the commit you reviewed:
"Commit 9eb9effd67, as reviewed: Merge job/91-… into main with a merge commit, in the main
checkout …". If the branch moved past that commit since you looked, nothing is landed.

After a merge or discard, crowsnest **tears down** in one step, each step on the job's timeline:
stops the agent, removes the worktree (refused if it has uncommitted work), closes the workspace,
deletes the branch (`-d` after a merge, so git confirms it is merged; kept for a pull request),
and removes the job's files. If a step fails, the job is landed but not torn down and says why;
**Tear down again** retries.

## What Land refuses

Land changes nothing and says why when:

- the base branch is **protected** (`--protected-branch`): use **Push and open PR** instead;
- the main checkout is on another branch, or has uncommitted changes to tracked files;
- the job's worktree has uncommitted work (it would be lost);
- there is nothing to merge;
- the merge conflicts (it is aborted and the conflicting files named);
- crowsnest's **evidence** does not support this revision: the check failed, is missing, is still
  running, is stale (a commit came after it), or the base moved on (**Needs rebase**). You can
  override this explicitly with a ticked box in the confirmation; it is journalled as "Landed
  over crowsnest's evidence". **Ask an agent to rebase** starts a job that merges the base in,
  resolves, checks and commits.

No repository hook runs when crowsnest lands (pre-merge and post-merge hooks included), and its
own git calls ignore the repository's pager, fsmonitor and external diff settings.

## An agent never lands

An agent can only **ask** to land (`job_land`, or `crowsnest job land`): that becomes a question to
you, pinned to the commit it was asked at, and only your plain **Allow** lands it. A note on the
Allow is a condition crowsnest cannot follow, so it drops the request instead. Such questions are
never answerable from a notification.

## The sweep

Finished worktree jobs that were never landed pile up. The board's **N to land** button, and
**Worktrees to land** in the palette, open **Worktrees still around**: each finished job with a
worktree, oldest first, with its age. Choose some (or **Choose all**) and **Discard chosen**; a
worktree with uncommitted work is refused and reported, never forced.

## Seeing the changes

The diff viewer shows a job's changes live while it works and before you land it: syntax
highlighted, split or unified, with **Viewed** marks tied to each file's content, so a file the
agent changes after you read it comes back. It also shows a lead's uncommitted edits in a
project's main checkout. See [Review changes with the diff viewer](../how-to/review-changes.md).

## What can go wrong

- **"Needs rebase"**: someone merged into the base after the branch started. **Ask an agent to
  rebase**, or merge it yourself in the worktree.
- **The worktree has uncommitted work**: open the job's terminal, commit or stash, then land.
- **The main checkout is dirty or on another branch**: that is your working copy; crowsnest will
  not touch it. Clean it up and land again.
- **A lead working in the main checkout** has no worktree and no Review tab: its commits are already
  in your checkout.

Step by step: [Review and land a job](../how-to/review-and-land.md).
