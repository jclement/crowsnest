# Crowsnest: status

Living record kept by the lead agent. Last updated: 2026-11-26 07:05 (America/Edmonton), end of the overnight build.

## In one paragraph

Crowsnest is built and on `main` (tip `8d93449` plus this file): one Go binary that starts a herdr session and a Claude orchestrator and serves a phone-friendly web app with Jobs, Chat, Inbox, Fleet, Terminal and talk mode. The centre is the job supervisor: an agent that ends a turn without declaring done, blocked or waiting is refused by a Stop hook and sent back to work, and this was proven against real Claude. Three review rounds by Codex and Opus found and fixed about 90 issues. It has not been used by a human yet, never touched a real phone, and the last review still holds an honest "not fully sound" on crash-recovery edges (below).

## Try it

```sh
cd ~/Work/crowsnest
mise run setup && mise run build
./bin/crowsnest --demo --no-auth                 # scripted fleet, no herdr or Claude: look around
CROWSNEST_PASSWORD=... ./bin/crowsnest --username jeff --session crowsnest   # the real thing, http://localhost:2769
```

For a phone on the tailnet: `--bind tailnet`, and `tailscale serve` in front if you want the microphone (it needs HTTPS). `README.md` has every flag; `DESIGN.md` is the design as built; `docs/TESTING.md` says what each test tier proves.

## What is built, and how each part was verified

"Lead ran" means I ran it myself on the final tip `8d93449`; otherwise it is the builder's run, which I read but did not repeat.

| Part | State | Verified how |
|---|---|---|
| Config, auth, sessions, origin and host checks | merged | Lead ran `mise run check` (21 Go packages with `-race`, 232 web tests): green. Hermetic browser suite covers login, lockout, cross-origin POST and WebSocket refusals |
| herdr client, fleet cache | merged | Lead ran `mise run test:integration` against real herdr in throwaway sessions: 7 tests pass. Also passed on Linux in a container (builder) |
| Orchestrator, transcript, Chat | merged | Lead ran `mise run test:claude` (real Claude, haiku) and `mise run e2e:claude` (real Claude through the browser): chat round trip passes |
| Terminals (herdr TUI and single pane) | merged | Lead ran `mise run e2e:real` (real herdr, browser): 11 pass. A Linux-only hang was found by CI and fixed |
| Questions, Inbox, agent CLI and MCP tools | merged | Real Claude asked through the MCP tool, the answer was given in the Inbox, and it reached the agent (lead ran, both real-Claude suites). `crowsnest ask --wait` proven through the browser |
| **Jobs and the never-stop supervisor** | merged | **The stop test, lead ran, real Claude: (a) an agent told to stop without declaring was refused and nudged; (b) a job that called `done` finished cleanly with evidence; (c) ask, answer, done.** Same three journeys pass through the browser |
| Pokes, journal, backstop for agents without hooks | merged | Real Claude received and answered a manual poke (lead ran). Timer and event pokes, and the backstop, only against the fake herdr |
| Talk mode | merged | Browser test against a fake ElevenLabs passes (lead ran, in the hermetic suite). **Never spoken to the real ElevenLabs: no key was available** |
| Hermetic browser suite | merged | Lead ran `mise run e2e`: 202 pass on desktop Chromium, mobile Chromium and mobile WebKit at 375 px, with accessibility checks |
| Release pipeline (GoReleaser, workflow) | merged | `mise run snapshot` built four archives and the darwin one served the app (builder). **The release workflow has never run: it needs a tag, which I left to you** |
| Devbox image | merged | Built locally (about 1 GB, arm64); container proof by the builder: health, login, refusals, non-root, mise installed Claude Code. **Nothing pushed to ghcr; amd64 build untried** |
| CI on GitHub | see "CI" below | |

## Reviews

Findings and verdicts are in `reviews/`. Three rounds, each by Codex (correctness) and Opus (security), each followed by a fix builder who had to show the regression test failing before the fix.

| Round | Found | Outcome |
|---|---|---|
| R1 | Codex 2 high, 8 medium; Opus 1 high, 3 medium | All fixed and verified by both reviewers |
| R1b | Codex 2 high, 7 medium; Opus 1 high, 2 medium | All fixed and verified |
| R2 (jobs supervisor) | Codex 9 high, 7 medium; Opus 1 high, 2 medium | 14 of 17 Codex findings verified fixed, 3 partly; security mostly fixed |
| R3 | Codex 4 high, 4 medium; Opus 1 high, 2 medium | Fixed by the last builder, **not re-reviewed**: there was no fourth review round |

Codex's closing judgement in R3, before the last fixes: the never-stop promise was "not yet sound" for crash boundaries (crowsnest itself dying mid-restart). The last fix builder addressed those findings and the stop test still passes, but no independent reviewer has looked at that final diff.

## Not verified

- No human has used it. Nothing was tried on a real phone, real Safari or iOS (WebKit only in Playwright).
- Real ElevenLabs. Codex jobs (only against the fake). The orchestrator taking a job from the text box with real Claude.
- Recovery from a real usage limit, API error, auth failure, crashed agent or compaction: unit-tested with the documented hook shapes only.
- Real permission dialogs from Claude (none appeared in `auto` mode); dialog detection has real screens for idle, draft and trust only.
- The release workflow and the image job on GitHub; the amd64 image; anything on Linux beyond a container run of the Go suite, the demo binary and the herdr integration tests.
- TLS serving has a test; `tailscale serve` in front has not been tried.

## Known holes (also in DESIGN.md section 20)

- A job can still end up stopped with nobody told when: no browser is open and `--notify-url` is not set (Web Push is not built); the agent declares itself `blocked` (a resting state by design); a usage limit lasts for days; herdr itself is unreachable.
- Two open review findings: poke reasons can be lost in a narrow window after an event-stream overflow; the token sweeper can miss a pane id herdr reused.
- Agents in one session run as one user and are not isolated from each other: an agent that reads another's token file can act as it. The token scheme stops accidents, not a hostile agent.
- The Terminal view is herdr's own TUI, so it shows every machine in your herdr config (on quark: `bedev`). Anyone who can sign in to crowsnest reaches those too.
- Not built: budgets, review gates, Web Push (planned as M12), scheduled tasks, passkeys.

## Decisions waiting for you

1. **Cut a tag to prove the release workflow?** `mise run release` is yours. I did not tag: a tag also publishes the devbox image to ghcr. Recommendation: `v0.1.0-rc.1` when you have looked at the app.
2. **Cosign, the Homebrew tap and self-update** are in the config but off while the repo is private (keyless cosign writes the repo identity to a public log, permanently). Turn on when you make it public.
3. **The name.** The front door picked "crowsnest"; a rename is cheap now and less so later.

## Things I did that you should know

- Accepted Codex's "trust this folder" prompt for `~/Work/crowsnest` so the Codex reviews could run; that trust is saved in Codex's settings.
- Declined two prompts rather than approve them for you: a convoluted `git stash` command from a builder, and your git guard hook when it could not evaluate a command. Both builders did the work another way.
- Real-Claude tests ran on haiku in throwaway herdr sessions named `crowsnest-test-*`, all stopped and deleted. Your `default` session and the `orchestrator` were not touched; claude-agent, earshot and meatprompt were not modified.
- Commits: 187 on `main`, signed, pushed to the private repo `jclement/crowsnest`. No tag, no release, nothing published.

## In flight

Nothing. All worktrees, branches, agents and review tabs I created are closed.
