# What agents can do: the CLI and MCP tools

Every agent crowsnest starts can talk back to it, through MCP tools (Claude) or the `crowsnest`
command in its pane (any agent, and scripts). Both go over a unix socket only your user can open,
and every call is identified by a token crowsnest minted for that agent, never by what the caller
says about itself. This page is what agents can do, so you know what to expect from them.

## MCP tools

Claude agents get these from `crowsnest mcp`, pre-approved (no permission prompt):

| Tool | What it does |
|---|---|
| `job_update(progress, state?)` | A heartbeat with a one-line "doing now". May set **blocked** (with a reason and a wake-up: a delay, a time, another job, or until a human says so) or back to running, or declare that nothing is left the open questions do not block. |
| `ask(question, wait=false)` | Posts a question to you. Returns at once by default; the answer is typed into the pane later. `wait=true` blocks, for irreversible steps. Can attach files from its own directory. |
| `get_question(id)` | Reads a question and its whole answer. |
| `done(result, verified, not_verified, walkthrough?)` | Declares the job finished, with evidence (and a walkthrough for review). For a coding job, crowsnest then checks it ([Acceptance](../concepts/acceptance.md)). |
| `fail(reason)` | Declares the job cannot be finished. |
| `job_create(brief, role, dir, …)` | Delegates a child job. Inside its own directory it starts at once; anything wider waits for your Allow. |
| `job_get()` | Reads its own job: brief, state, questions and answers, spend. What it reads after a compaction. |
| `job_land(merge\|pr\|discard)` | **Asks you** to land a job; only your Allow lands it. |
| `job_adopt(title, brief)` | Standing agents only: records work you gave in conversation as a job it holds. |
| `project_list()`, `project_get(slug)` | The projects, their leads and jobs: what the orchestrator reads before handing work to one. |
| `say(text)` | Speaks one sentence in browsers with speak mode on (talk mode only). |

## The `crowsnest` command, agent side

The same verbs for agents without MCP (Codex) and for scripts, run inside an agent's pane:

| Command | Does |
|---|---|
| `crowsnest job update "doing now"` | Heartbeat; `--state blocked --reason "…"` with a wake-up (`--recheck-in 30m`, …), or `--waiting`. |
| `crowsnest job done --result "…" --verified "…" --not-verified "…"` | Finish with evidence; `--attach` a file, `--walkthrough-file`, `--no-commits-reason`. |
| `crowsnest job fail "reason"` | Give up, with the reason. |
| `crowsnest job get` | Its job as text (`--json` for the full record). |
| `crowsnest job create "brief" --dir … --role …` | Delegate a child job. |
| `crowsnest job land j_N merge\|pr\|discard` | Ask you to land. |
| `crowsnest ask --file q.json [--wait]` | Ask a question (title, body, kind, options, recommend, urgency). Exit 0 answered, 3 expired or cancelled. |
| `crowsnest question <id>` | A question with its whole answer. |
| `crowsnest notify "text"` | A notice: no answer needed. |
| `crowsnest say "sentence"` | Speak (talk mode only). |
| `crowsnest heavy -- <command>` | Run a build, test suite or typecheck through the heavy-command slots. |
| `crowsnest project list`, `crowsnest project get <slug>` | Projects and their leads. |
| `crowsnest pause`, `crowsnest pause --status` | Pause the automation, or read it. `resume` is refused to an agent: only you resume. |
| `crowsnest hook <event>` | Claude Code's hooks call this; not for people. |

## Limits on agents

- **Delegation**: at most 3 levels deep, 8 children per job, 16 live agent-created jobs, and 2
  requests per agent waiting for you. A child outside the asker's own directory, with another
  engine, or without isolation from an isolated job waits for your plain **Allow** (an Allow with
  a note cancels it and passes the note back).
- **Questions**: 10 open at once per job agent (30 for the orchestrator), a rate limit, and an
  expiry of at least five minutes. Some context keys are crowsnest's own (the gates), and an agent
  question carrying one is refused.
- **Landing**: an agent can only ask.
- **The guard**: before every Bash call, a local check refuses a few commands outright: reaching a
  protected host, committing or pushing onto a protected branch (or force-pushing), stopping the
  herdr server, or driving another herdr session. See [Safety](../safety.md).

## What an agent is started with

- In its pane: `CROWSNEST_JOB`, `CROWSNEST_SOCKET`, its token, and `crowsnest` on `PATH`.
- Claude flags: `--append-system-prompt-file` (your instruction layers, then the job protocol),
  `--settings` (crowsnest's hooks and the allow rule for its MCP tools), `--mcp-config` (the
  `crowsnest mcp` server).

Nothing is written into the repository or your own Claude settings.
