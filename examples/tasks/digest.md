---
every: 1d
after: 17:00
---

# Daily digest

Once a day, after 17:00 local time, tell the human what happened today in one notice.

1. Gather today's picture: jobs finished, failed or cancelled today (with their results), jobs
   still running, questions still open, and anything you were asked to do that is not finished.
2. Send one `ask` with `kind: "notice"`, titled `Digest <YYYY-MM-DD>`, with three short
   sections: **Done today**, **Waiting on you**, **Still running**. Keep it to what the human
   would act on.
3. If talk mode is on, begin your reply with `SAY:` and one sentence that sums it up.
