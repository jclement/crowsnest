# Crowsnest: status

Living record kept by the lead agent. Updated at every milestone boundary.
Last updated: 2026-11-26 01:15 (America/Edmonton)

## Where things stand

| Milestone | State | Verified how |
|---|---|---|
| Design (DESIGN.md) | done | herdr 0.9.5 facts checked in a throwaway session (headless named session, socket protocol, events, agent start/prompt, transcript location, both terminal bridges) |
| M1 skeleton, config, auth, CI | merged | Lead re-ran `mise run check` on main: green. Builder's smoke (login, lockout, cross-origin and rebinding rejections) in its report. CI green on GitHub |
| M2 herdr client, fleet, demo mode | merged | Lead re-ran `mise run check` and `mise run test:integration` (real herdr, throwaway session) on main: green. Dialog and draft detection only against hand-made screens so far |
| M3 web app shell and Fleet view | merged | `mise run check` green on main (91 web tests). Screenshots at 375 and 1440 reviewed by the lead. Runs on fixtures until M2 lands |
| M4 orchestrator, transcript, Chat | merged | Lead re-ran `mise run check` on main: green (146 web tests, 13 Go packages). Builder ran a real-Claude test (haiku, throwaway session): orchestrator started, answered a prompt, restart and new conversation worked. Chat seen in `--demo` by Playwright |
| M5 terminals (and reconciling web types with the real API) | merged | `mise run check` green on main; builder's real-herdr integration tests (pane bridge, TUI bridge, takeover) and Playwright against a real throwaway session passed. Not tried on a real phone |
| M6 agent API (socket, CLI, MCP), questions, inbox | building | |
| M7 jobs core: supervisor, stop hook, heartbeat, roles, Jobs view | not started | Design probe only: a `--settings` Stop hook refused a stop and a SessionStart hook injected context in a live interactive Claude |
| M8 pokes, journal, backstop, per-agent views | not started | |
| R1 review round (Codex + Opus) | running | Reviewing main at 86a6f9d (M1 to M5); findings land in `reviews/` |
| M9 Playwright suites, including the stop test | not started | |
| M10 release workflow and devbox image | release pipeline merged; image building | `mise run snapshot` produced four archives and the darwin one served the web app (builder). The release workflow has never run: it needs a tag |
| M11 talk mode | not started | |
| M12 budgets, review gates, Web Push | not started | |
| R2 final review and full-suite run | not started | |

## Not verified

- Nothing has been tried on a real phone (key bar, on-screen keyboard, Safari).
- The release workflow on GitHub (needs a tag). Linux binaries are built but have not been run.
- Dialog detection has only seen hand-made screens plus one real permission dialog shape; the folder-trust path with a real untrusted directory has not been tried.
- TLS serving and a clean-clone `mise run setup`.
- No questions, jobs, pokes or voice yet.

## Blocked

Nothing.

## Decisions waiting for Jeff (none block the build)

- Cosign signing, the Homebrew tap and self-update are deferred while the repo is private (DESIGN.md section 19, item 4).

## In flight

- `crow-m6` (Opus), worktree `wip/m6-questions`: agent socket, CLI, MCP, questions, Inbox. Its move.
- `crow-m10b` (Opus), worktree `wip/m10-devbox`: the devbox container image. Its move.
- `crow-r1-codex` (Codex) and `crow-r1-opus` (Opus), tabs in the crowsnest workspace: read-only reviews of main. Their move; then a fix builder.
- Next for the lead: M7 jobs core (brief written) when M6 lands; M8 pokes; M9 Playwright; R2.

## Things Jeff should know

- The Terminal view is herdr's own TUI, so it shows every machine in the user's herdr config (on quark: `bedev`, over SSH). Anyone who can sign in to crowsnest can reach those too.
- I accepted Codex's "trust this folder" prompt for `~/Work/crowsnest` so the Codex reviews you asked for could run. That trust is saved in Codex's settings.
