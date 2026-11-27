# Spend: an estimate, not a bill

Every job shows what its agent used, and the orchestrator and each lead have a line of their own:
tokens in and out, tokens written to and read from the prompt cache, model calls, tool calls, wall
time, and an **estimated** cost in US dollars.

## Where the numbers come from

From each agent's Claude Code transcript (`~/.claude/projects/…/<session>.jsonl`, and its
subagents' files beside it), read every 10 seconds and kept in crowsnest's database, so they
survive a restart and a deleted transcript. A job restarted under a new Claude session keeps
counting.

## Why it is an estimate

The cost is what the same tokens would cost on the **Claude API at list prices** (input, output,
cache writes and reads, fast mode, long-context tiers). A Claude **subscription is not billed per
token**, so on a subscription this is not what you pay; it is a fair measure of how much work each
job took, and of which jobs are expensive. Every cost is marked "est." and the Spend sheet's title
says "(estimated)".

- Prices are built in, with the date they were checked. To correct or add one, write
  `~/.config/crowsnest/prices.json` (the README shows its shape); it reprices history too.
- A model with no known price shows its tokens and "cost unknown", never $0.
- **Codex** agents are measured in tokens from Codex's own session log, with no cost (crowsnest has
  no prices for OpenAI's models); one whose session crowsnest never learned shows "not measured",
  never zero. Budgets bound them by
  minutes and turns instead.
- A job the orchestrator or a lead holds in its own conversation is counted on that agent's line.

## Where you see it

- **On each job card**: "Spend: $0.81 (estimate)".
- **In a job's Overview**: the **Spend** section, one figure; its **Details** has the breakdown
  (tokens, calls, wall time), the share of the prompt read from the cache, the models, and a small
  chart of tokens per model call (point at a bar for that call).
- **Above the Jobs board**: today's and this week's totals ("Spend: $31.16 today · $32.11 wk
  (estimates)"); press it for the Spend sheet.
- **On Now**: each project's estimated spend today.
- **The Spend sheet** ([Open Spend](app:sheet/spend)): **Today**, **This week** or **30 days**;
  one figure **By job** or **By day**; a **Project** filter; sortable. Tick **Details** for **By
  model**, the token and call columns and **CSV**.
- **Chat's menu**: the standing agent's own line.

Days are the server's local calendar days; weeks start on Monday.

## When you use it

- To see which jobs or projects cost the most, and whether a job is burning tokens going round in
  circles (a steep chart with no progress).
- To set sensible [budgets](budgets-limits-pause.md#budgets): look at what similar jobs used.
- To notice the prompt cache is not working (a low "Prompt from cache" share costs more).

## What can go wrong

- **$0, "cost unknown" or "not measured"**: a Codex job (tokens, no cost), or a transcript
  crowsnest has not found yet.
- **Different from your Anthropic bill**: expected on a subscription; on API keys, the built-in
  prices may be out of date (fix with `prices.json`).
