---
every: 15m
---

# Anything stuck or waiting on the human?

A quick look, every quarter hour. Keep it cheap: no new work, no messages unless something needs
the human.

1. List the live jobs (`crowsnest job list`) and look for any that are stuck, blocked or
   `needs_you`, and for questions to the human that have been open more than an hour.
2. If something needs the human and they have not been told since it started, send one `ask`
   with `kind: "notice"` naming each item and what it is waiting for. Never repeat a notice for
   the same item.
3. If nothing needs anyone, reply `noop`.
