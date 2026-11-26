# Crowsnest: status

Living record kept by the lead agent. Updated at every milestone boundary.
Last updated: 2026-11-25 23:20 (America/Edmonton)

## Where things stand

| Milestone | State | Verified how |
|---|---|---|
| Design (DESIGN.md) | done | herdr 0.9.5 facts checked in a throwaway session (headless named session, socket protocol, events, agent start/prompt, transcript location, both terminal bridges) |
| M1 skeleton, config, auth, CI | starting | |
| M2 herdr client, fleet, demo mode | not started | |
| M3 web app shell and Fleet view | not started | |
| M4 orchestrator, transcript, Chat | not started | |
| M5 terminals | not started | |
| M6 agent API (socket, CLI, MCP), questions, inbox | not started | |
| M7 jobs core: supervisor, stop hook, heartbeat, roles, Jobs view | not started | Design probe only: a `--settings` Stop hook refused a stop and a SessionStart hook injected context in a live interactive Claude |
| M8 pokes, journal, backstop, per-agent views | not started | |
| R1 review round (Codex + Opus) | not started | |
| M9 Playwright suites, including the stop test | not started | |
| M10 release workflow and devbox image | not started | |
| M11 talk mode | not started | |
| M12 budgets, review gates, Web Push | not started | |
| R2 final review and full-suite run | not started | |

## Not verified

Nothing is built yet.

## Blocked

Nothing.

## Decisions waiting for Jeff (none block the build)

- Cosign signing, the Homebrew tap and self-update are deferred while the repo is private (DESIGN.md section 19, item 4).

## In flight

- `crow-m1` (Opus) in worktree `wip/m1-skeleton`: building M1. Its move.
- Briefs for M2 and M3 are written; they start when M1 is merged. Lead's move then.
