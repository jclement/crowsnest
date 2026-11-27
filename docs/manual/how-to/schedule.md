# Schedule recurring work

Concepts: [Schedules and pokes](../concepts/schedules-and-pokes.md).

## Open the Schedule sheet

Any of: the Jobs board's **Schedule** button, Chat's **Conversation menu** → **Schedule**, a
project page's **Manage**, or the palette ([Open Schedule](app:sheet/schedule)). It lists every
task with its cadence, last run, next run, an on/off switch, and **Edit**, **Delete** and **Run
now**. The **Project** filter narrows it.

![The Schedule sheet: the daily digest, then every scheduled task](img/schedule-1440.jpg)

## Make one in the app

1. **New schedule**.
2. **Name** (lower case, for example `evening-review`).
3. **When**: **Every N minutes** (at least 5), **Every N hours**, **Every N days**, **Daily at a
   time**, **With every poke**, or **When something changes**.
4. **Who**: **The orchestrator**, **A project's lead** (choose the **Project**), or **A job**
   (choose where it runs, its role and engine).
5. **Body**: what to do, in markdown, as you would brief a colleague. **Preview** shows it.
6. **Create schedule**. It runs at its next due time; **Run now** runs it at once.

A poke schedule becomes one line of the target's next poke ("scheduled task evening-review is due
… read <file> and do what it says"); a job schedule starts a supervised job with the body as its
brief.

## Or keep it in a file

Put a markdown file in `~/.config/crowsnest/tasks/` (or the orchestrator directory's `tasks/`):

```markdown
---
every: 1d          # 15m, 60m, 4h, 1d, 3d, poke or change
after: 17:00       # optional: not before this local time
as: job            # optional: start a job instead of telling the orchestrator
dir: ~/Work/site   # as: job only: where the job runs
role: builder      # as: job only
project: website   # optional: poke that project's lead, or file the job under it
---
# Link check

Run the site's link check and fix what is broken.
```

Files are re-read at every check: no restart. They show "from file" and read-only in the sheet;
the on/off switch still works. A file with the same name as an app schedule wins.

**A file that starts a job (`as: job`) or pokes a lead (`project:`) waits for you**: the sheet
shows it as **new or changed: review** with its text. Read it, then **Approve as it reads now**.
Any later edit asks again. This is because agents can write files, and a task file is a way to
start work unattended.

## The daily digest

At the top of the sheet: switch **Daily digest** on and set its time. The orchestrator writes the
day's digest into the Inbox; **Past digests** lists earlier ones.

## What can go wrong

- **It never ran**: switched off? paused? awaiting review? shadowed by a file of the same name? The
  row says.
- **It ran twice**: it cannot on one calendar day; a "with every poke" task rides on every poke by
  design.
- **The file is listed as a problem**: bad frontmatter, an unknown key, `every: 1w`, or an unknown
  project. The Journal has the reason.
