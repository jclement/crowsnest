# Crowsnest: status

Living record kept by the lead agent. Updated at every milestone boundary.
Last updated: 2026-11-26 00:40 (America/Edmonton)

## Where things stand

| Milestone | State | Verified how |
|---|---|---|
| Design (DESIGN.md) | done | herdr 0.9.5 facts checked in a throwaway session (headless named session, socket protocol, events, agent start/prompt, transcript location, both terminal bridges) |
| M1 skeleton, config, auth, CI | merged | Lead re-ran `mise run check` on main: green. Builder's smoke (login, lockout, cross-origin and rebinding rejections) in its report. CI green on GitHub |
| M2 herdr client, fleet, demo mode | merged | Lead re-ran `mise run check` and `mise run test:integration` (real herdr, throwaway session) on main: green. Dialog and draft detection only against hand-made screens so far |
| M3 web app shell and Fleet view | merged | `mise run check` green on main (91 web tests). Screenshots at 375 and 1440 reviewed by the lead. Runs on fixtures until M2 lands |
| M4 orchestrator, transcript, Chat | building | |
| M5 terminals (and reconciling web types with the real API) | building | |
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

- The Fleet view has only been seen on fixtures; against the real `/fleet` it is known not to match yet (being fixed in M5).
- No chat, terminal, questions or jobs yet.
- TLS serving, `mise run dev`, and a clean-clone `mise run setup` have not been exercised.

## Blocked

Nothing.

## Decisions waiting for Jeff (none block the build)

- Cosign signing, the Homebrew tap and self-update are deferred while the repo is private (DESIGN.md section 19, item 4).

## In flight

- `crow-m4` (Opus), worktree `wip/m4-orch`: orchestrator, transcript, Chat. Its move.
- `crow-m5` (Opus), worktree `wip/m5-term`: terminals; first reconciles the web app's fleet types with the server's real shapes (M2 and M3 drifted). Its move.
- M6 brief written; M7 (jobs core) brief next. Lead's move when M4 lands.
