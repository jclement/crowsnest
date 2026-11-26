# Crowsnest: status

Living record kept by the lead agent. Updated at every milestone boundary.
Last updated: 2026-11-26 03:05

## Where things stand

| Milestone | State | Verified how |
|---|---|---|
| Design (DESIGN.md) | done | herdr 0.9.5 facts checked in a throwaway session (headless named session, socket protocol, events, agent start/prompt, transcript location, both terminal bridges) |
| M1 skeleton, config, auth, CI | merged | Lead re-ran `mise run check` on main: green. Builder's smoke (login, lockout, cross-origin and rebinding rejections) in its report. CI green on GitHub |
| M2 herdr client, fleet, demo mode | merged | Lead re-ran `mise run check` and `mise run test:integration` (real herdr, throwaway session) on main: green. Dialog and draft detection only against hand-made screens so far |
| M3 web app shell and Fleet view | merged | `mise run check` green on main (91 web tests). Screenshots at 375 and 1440 reviewed by the lead. Runs on fixtures until M2 lands |
| M4 orchestrator, transcript, Chat | merged | Lead re-ran `mise run check` on main: green (146 web tests, 13 Go packages). Builder ran a real-Claude test (haiku, throwaway session): orchestrator started, answered a prompt, restart and new conversation worked. Chat seen in `--demo` by Playwright |
| M5 terminals (and reconciling web types with the real API) | merged | `mise run check` green on main; builder's real-herdr integration tests (pane bridge, TUI bridge, takeover) and Playwright against a real throwaway session passed. Not tried on a real phone |
| M6 agent API (socket, CLI, MCP), questions, inbox | merged | `mise run check` green on main (173 web tests, 16 Go packages). Builder's real-Claude test: the orchestrator called the `ask` MCP tool with no permission prompt, the question was answered through the browser API, and the answer arrived in its transcript |
| M7 jobs core: supervisor, stop hook, heartbeat, roles, Jobs view | merged | `mise run check` green on main (221 web tests). **The stop test passed against real Claude** (haiku, throwaway session, real worktrees): (a) an agent told to stop without declaring was refused by the Stop hook, nudged once, then asked a question and declared it was waiting; (b) a job that called `done` finished with evidence and zero nudges; (c) a non-blocking `ask` was answered through the browser API, the answer reached the agent, and the job finished. Not tried for real: rate-limit, auth-failure, crash and compaction recovery; Codex jobs; the orchestrator taking a job |
| M8 pokes, journal, backstop, per-agent views | building | |
| R1 review round (Codex + Opus) | first round fixed and verified by both reviewers; second round of fixes in progress | Re-review (`reviews/R1b-*.md`): all first-round findings fixed. New findings in the newer code: Codex 2 high, 7 medium; Opus 1 high, 2 medium, 5 low. Being fixed now |
| M9 test campaign: hermetic, real-herdr and real-Claude browser suites | building | |
| M10 release workflow and devbox image | release pipeline and devbox image merged | `mise run snapshot` produced four archives and the darwin one served the web app (builder). Image built locally (1036 MB, arm64), container proof by the builder: health, login, refusals, non-root, mise installed Claude Code in 7 s. Neither workflow has run on GitHub (needs a tag); nothing pushed to ghcr; multi-arch build untried |
| M11 talk mode | merged | `mise run check` green on main; Playwright spec against a fake ElevenLabs passed (hold to talk, transcript sent, SAY reply fetched and played). **No real ElevenLabs call has been made**: no key was available. Safari and iOS untried |
| M12 budgets, review gates, Web Push | not started | |
| R2 final review and full-suite run | not started | |

## Not verified

- Nothing has been tried on a real phone (key bar, on-screen keyboard, Safari).
- The release workflow on GitHub (needs a tag). Linux binaries are built but have not been run.
- Dialog detection has only seen hand-made screens plus one real permission dialog shape; the folder-trust path with a real untrusted directory has not been tried.
- TLS serving and a clean-clone `mise run setup`.
- No pokes or journal yet.
- Talk mode has never spoken to the real ElevenLabs API.
- Browser notifications and `--notify-url` are unit-tested only. `ask --wait` has not been tried against a real Claude.

## Blocked

Nothing.

## Decisions waiting for Jeff (none block the build)

- Cosign signing, the Homebrew tap and self-update are deferred while the repo is private (DESIGN.md section 19, item 4).

## In flight

- `crow-fix2` (Opus), worktree `wip/fix-r1b`: second round of review fixes, including agent-socket identity tokens. Its move.
- `crow-m8` (Opus), worktree `wip/m8-pokes`: pokes, journal, the backstop for agents without hooks. Its move.
- `crow-m9` (Opus), worktree `wip/m9-e2e`: the test campaign; fixes what it finds. Its move.
- After those: a final review round (R2) on the merged tip, the full suite once more, and the wrap-up report. M12 (budgets, review gates, Web Push) only if time remains.

## Things Jeff should know

- The Terminal view is herdr's own TUI, so it shows every machine in the user's herdr config (on quark: `bedev`, over SSH). Anyone who can sign in to crowsnest can reach those too.
- I accepted Codex's "trust this folder" prompt for `~/Work/crowsnest` so the Codex reviews you asked for could run. That trust is saved in Codex's settings.
