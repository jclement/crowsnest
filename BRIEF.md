# Crowsnest: brief from Jeff (2026-11-25, typed at the front door; he is asleep, work unattended)

Name: **crowsnest** (chosen by the front door; Jeff asked for "a sick AF project name". Rename is cheap if he hates it.)
Folder: ~/Work/crowsnest (already created with `new-project`, trusted). Read the `new-project`, `builder`,
`code-craft`, `go-cli`, `web-apps`, `mise-projects` skills and follow Jeff's personal-project conventions.

## What Jeff asked for, verbatim
> I still struggle with managing herdr/agents remotely. needs a sick AF project name
>
> What I want is a project that's a single go binary.
> env, config or cli args to set
> - username, password or --no-auth (if tailnet/network is trusted)
> - bind address/interface (localhost by default, only on tailnet)
> - herdr session name to create/connect to (by default, named after this new project)
>
> I want this thing to start up herdr and claude with specific instructions linked in that it's running in this tool
> so...
> - easily interact with the main orchestrator (post new, respond to questions, see conversation - ideally as nicely formatted html type stuff - tailing log?)
> - see questions (same idea as meatprompt - markdown, code highlighting, mermaid, multiple/voice, comments, etc)
> - and also a tab that uses that js terminal thing to connect to herdr via websocket
> - bonus if it actually show a list of herdr spaces and tabs and let you toggle between those and interact with those agents directly.
> pretty tailwindy UI. mobile responsive.
> maybe also add talking mode (push to talk, speak) - enabled if elevenlabs api key offered up. probably only interacts with main orchestrator.
>
> this is an experiment to get a better browser based development experience. it'll hopefully replace claude-agent, earshot, meatprompt.
> spin up a fable agent to help design this properly.
> then use opus to build it. multiple rounds of review and testing (codex, and opus).
> you should be able to build this and test it entirely while i sleep here. Want a good test suite, but also use playwrite, etc. to test the heck out of [it]
> It should automatically start the claude "orchestrator" session and send pokes to the orchestrator. any questions

## Process (Jeff's)
1. **Design first, by a Fable agent** (this one). Produce `DESIGN.md` in the repo: architecture, config surface, auth model,
   how herdr is driven (socket API / CLI; read `herdr --skill`, https://herdr.dev/llms.txt, and the front door's own scripts in
   ~/Developer/claude-agent/frontdoor/bin for how an orchestrator is poked, how questions are relayed, how Earshot's
   `SAY:` lines work, and how Meat Prompt's question format looks), data model, API and websocket protocol, UI layout,
   security (a web terminal into herdr is remote code execution: auth, CSRF/origin checks on websockets, bind defaults,
   secrets never logged), test strategy, and a milestone plan. Keep it decisive; list open questions with your recommended answer.
2. **Opus builds it** in milestones, in worktrees under herdr, one agent per unit of work, at most two heavy jobs at once
   (everything builds/tests through `heavy`).
3. **Multiple review rounds**: Codex (`codex`) and Opus reviewers as agents in herdr tabs (never hidden subagents), findings fixed
   and re-reviewed. A review is an agent in a tab of the reviewed branch's worktree workspace.
4. **Testing**: Go unit/integration tests, plus Playwright end to end against a real running instance with a real herdr session
   (throwaway session name, never Jeff's real herdr session or the `orchestrator` pane), including mobile viewport.
5. Work unattended. Do not ask Jeff anything with a dialog. A true blocker or expensive decision goes through Meat Prompt
   (`meatprompt` skill); otherwise pick, act, and record the choice in DESIGN.md.

## Rules that apply
- Commit and push freely to this new repo's own branches/main (a personal project; create a GitHub repo only if `gh` is set up and
  Jeff's other projects do so, private by default). No secrets in the repo. Never touch production hosts.
- Do not modify claude-agent, earshot, or meatprompt; crowsnest must run alongside them without disturbing them. Do not run
  anything against Jeff's live herdr session or the running `orchestrator`. Test with a separate herdr session name.
- Report at the end with what was verified and what was not.
