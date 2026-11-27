# Review changes with the diff viewer

The diff viewer shows what an agent changed, live, with syntax highlighting, and remembers which
files you have already looked at. Use it while a job is still running ("what is it doing to my
code?"), before you land a job, or to see a lead's uncommitted edits in a project's main checkout.

## Where to open it

- **A worktree job**: its **Review** tab (called **Land** once it is ready to land).
- **A job working in a directory** (no worktree of its own): its **Changes** tab.
- **A project**: the **Changes** card on its page lists the main checkout and every job checkout
  with changes, each with its line counts and how much of it you have viewed; the lead's controls
  have a **Changes** button, and Chat with a lead has a Changes link in its header.
- **The palette**: type `changes` and a job or project name for **Changes: <job>** or **Changes:
  <project>**.
- **Full screen**: **Full screen (f)** in the viewer, or **Open in a new tab**. It is its own page,
  `/diff?job=j_24` or `/diff?project=<slug>`, with nothing of the app around it; **Back** or Esc
  returns to where you were, keeping the file and the scroll position.

[Open Jobs](app:/jobs) and pick a running job to try it.

![The diff viewer full screen: which changes, the file list with Viewed boxes, a split, highlighted diff](../img/diff-1440.jpg)

## Reading it

1. **Which changes** (the buttons at the top, each with its file count):
   - **All changes**: everything since the branch left its base, committed or not;
   - **Commits on this branch**: only what is committed (what Land would merge);
   - **Uncommitted**: what the agent has not committed yet;
   - **Since you viewed**: only what changed in files since you last marked them viewed.
2. **The file list** on the left (a sheet on a phone): each file with its status (added, modified,
   deleted, renamed, binary), its line counts and a **Viewed** box. **Filter** narrows it by path.
   The panel button beside the scopes hides or shows it.
3. **The diff**: syntax-highlighted, with line numbers on both sides and the changed words
   emphasised. **Split** shows old and new side by side on a wide screen; press it again for one
   column. **Hide whitespace** and **Wrap** do what they say. The arrows in a gap ("7 unchanged
   lines") show 20 more lines up or down, or the whole gap. Generated files, lockfiles, images and
   very large files start folded, saying why.
4. The viewer is **live**: as the agent edits and commits, the list and the diff follow within
   seconds, without reloading the page. **Refresh** reads it again now.

## Viewed marks

Tick **Viewed** on a file when you have read it. The file folds away, and the count at the top
("3 of 10 viewed") goes up. **Hide viewed** leaves only what you have not read; **Mark all
viewed** and **Clear marks** do it for every file.

A mark belongs to the file's **content**, not its name. If the agent changes a file after you
marked it, the file comes back, flagged **changed since you viewed it**, and **Show since viewed**
shows only the new edits, not the whole file again. A mark made in one scope counts in the others
when it is the same change. Marks go away when the job lands, is discarded or is deleted.

On the Land tab, the confirmation says how many files of what would merge you have **not viewed**
("2 of 7 files not viewed"). To make that a rule, turn on **Require all files viewed before
Land** in Settings ([Open Settings](app:sheet/settings)); Land then refuses until every committed
file is viewed.

## Keys

`j` and `k`: next and previous file. `n` and `p`: next and previous change. `v`: mark the current
file viewed (again to unmark). `f`: full screen; Esc leaves it. They do nothing while you type in a
box. All shortcuts: [Keyboard shortcuts](../reference/keyboard-shortcuts.md).

## On a phone

One file at a time, with **Previous**, **Viewed** and **Next** at the bottom under your thumb, and a
swipe moves between files. The file list opens as a sheet.

## What it does not do

- **Comments on lines** are not built: send what you want changed in the job's message box.
- It never changes the repository: it reads it through a temporary copy of git's index, so the
  agent's index, branch and commits are untouched, and none of the repository's hooks or filters
  run.
- Limits: the first 1,500 files are listed; a single file's diff is cut at 512 KB; untracked files
  over 2 MB are listed but not read.

## What can go wrong

- **A file shows "changed since you viewed it" but looks the same**: whitespace changed. Turn on
  **Hide whitespace**.
- **Nothing appears for a lead**: its main checkout has no uncommitted changes; a lead's committed
  work is already in your checkout.
- **Land says files are not viewed**: the setting is on; view them, or turn it off in Settings.
