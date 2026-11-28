# Give a job and write a good brief

Three ways in: the job box (typed), your voice, or the command palette. Each makes the same
supervised job. Concepts: [Jobs and their states](../concepts/jobs.md).

## From the job box

The job box is at the bottom of **Now** (**Give a job**). The Jobs board is a list and has no box
of its own: its **New job** button opens the same form in a sheet, given to the project the board
shows (a project page's **New job** does the same). On a phone the **+** in the header opens that
form from any view.

1. **For** (on Now) or the project buttons (on Jobs): who gets it.
   - **The orchestrator (no project)**: it holds the job, does it or delegates it.
   - **A project**: then choose **To the lead** (the default: the lead holds it and plans) or **As a
     job** (a job agent starts in the project's directory, with the project's defaults).
2. **What needs doing?**: the brief. Its first line becomes the title.
3. Optional: press the summary button under the box (it reads, for example, **To the
   orchestrator**) for **Where** (a directory or repo; empty for the orchestrator or the project's
   directory; **Browse…** opens the [folder picker](projects.md#choosing-the-folder)), **Role**,
   **Done when** and **Engine**. With **Codex** the form says plainly what it does not get from
   crowsnest (no hooks: a stop it cannot refuse, no spend measurement, no spinning detection, no
   usage-limit reset times, answer receipts only to "delivered"); budgets still bound it by minutes
   and turns.
4. Attach files if they help: paste a screenshot, drop a file, or press **Attach files**.
5. **Give job** (`⌘ Enter`, `Ctrl Enter` elsewhere). A toast says where it went ("Given to
   Crowsnest's lead", "Starting an agent in …", "Sent to the orchestrator"), with a link.

A job with a **directory in a git repository** gets a builder in its own worktree by default. A
job with **no directory** is held by the orchestrator (or the project's lead) in its own
conversation.

[Open Jobs](app:/jobs) to give one now.

## By voice

With talk mode on (an ElevenLabs key; [Talk and listen](voice.md)), Now's job box has a mic.

1. Hold the mic (or tap to lock it on, tap again to stop) and speak: "Website: fix the checkout
   validation".
2. The transcript appears, editable. crowsnest works out the project from the words ("Website:",
   "for Website, …") and takes the name off the brief; if it hears two projects, or none and none is
   chosen, it asks **Which project?** with buttons.
3. It reads the job back ("Website: Fix the checkout validation. Create the job?"), aloud too when
   speak mode is on.
4. Nothing exists until you press **Create**. Then "Created j_42 for Website", with **Undo** for 8
   seconds.

## From the command palette

1. `⌘K` (`Ctrl K` elsewhere), or **Search or run…** in the header.
2. Type the brief. A sentence (three words or more) offers **Give job: <your text>** first; Enter
   gives it to the orchestrator. A word or two reads as a search: what matches comes first, and
   **Send to orchestrator** and **Give job** come last.
3. `⌘ Enter` instead opens the full job form (the Jobs board's **New job** sheet) with the text
   filled in. `→` on Give job lists every project: **Give it to <project>'s lead**.
4. Start with `>`, `job ` or `give ` to offer only Give job (the word itself is not kept).
5. Start with `@<project>` (`@website fix the login test`; the first letters are enough) to give
   it to that project's lead.

Or type the project's name in the palette, press `→` for its actions, and choose **Give a job to
<project>'s lead…**.

## Writing a good brief

The brief is everything the agent knows about what you want. It reads it once, at the start, and
again after a compaction. Write it as you would for a capable colleague who has never seen the
code:

- **The outcome, first line.** "Retry widget fetches with back-off" beats "look at fetch".
- **Why**, in a sentence: what is wrong today, who notices.
- **Where**: the files, package or screen, if you know.
- **Done when**: what you will check. "Tests pass and the README says how" gives the agent a target
  and gives you something to verify. Use the **Done when** field.
- **What not to touch**: "do not change the public API", "no new dependencies".
- **What you are unsure of**: say so, and tell it to `ask` rather than guess on that point.

Leave out: how to run the tests (put that in the [project's
instructions](../concepts/instruction-layers.md) once), and step-by-step instructions the agent can
work out from the code.

Good:

```text
Retry widget fetches with back-off

Fetch fails on a flaky network and the widget shows an error. Retry three times with
exponential back-off (100, 200, 400 ms) in internal/fetch, and test both the retry and the
give-up. Do not change Fetch's signature.
```

## What can go wrong

- **The job sits in Queued.** It is held (slots, load, pause, usage limit); the card says why.
  **Start anyway**, or wait.
- **"Waiting for your approval: an agent asked for it"**: an agent delegated a job outside its own
  directory; allow it in the Inbox.
- **The wrong project.** A job joins the project whose directory contains its **Where**. Choose
  **For** explicitly.
- **It went to the orchestrator when you meant a builder.** With no **Where**, a job is held by the
  orchestrator or lead. Give a directory (or choose **As a job** for a project) for a builder.
