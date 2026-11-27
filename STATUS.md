# Crowsnest: status

Living record kept by the lead agent. Last updated: 2026-11-27 02:20 (CST, read from the clock), during the second overnight run.

## In one paragraph

Crowsnest is one Go binary that starts a herdr session and a Claude orchestrator and serves a phone-friendly web app for running coding agents across several projects. `v0.1.0` was released on 2026-11-26 and its pipeline worked first time (binaries, cosign signature, Homebrew cask, ghcr image). Since then `main` has roughly doubled: first-run setup, attachments, spend, a command palette, voice with per-agent voices, schedules, compaction, projects with lead agents, structured tool approvals, a Now home with one attention queue, landing with a review gate, throttling, a pause switch, backup and retention, a doctor command and a guided first run. All of that is merged and green on `main`, but **`main` is not fit to tag yet**: last night's code and security reviews found that a prompt-injected agent could satisfy several human gates by itself. Fixes are in progress (below).

## Try it

```sh
brew install jclement/tap/crowsnest      # v0.1.0; the install itself is untested by me
# or, from the repo, the current main:
mise run setup && mise run dev           # builds one binary, runs it on http://localhost:2769 against herdr session crowsnest-dev
mise run dev:demo                        # scripted five-project demo, no herdr or Claude needed
crowsnest doctor                         # what is installed, signed in, and how each setting was decided
```

A narrated tour of the product (3 min 50 s, recorded from the real app in demo mode) is at `~/Downloads/crowsnest-tour.mp4`; the project that regenerates it is `promo/`.

## What is on main, and how it was verified

"Lead ran" means I ran it myself on `main` after the merge. Every merge was followed by `mise run check` and the hermetic browser suite on `main`; the latest full run was 490 unit tests and 468 browser tests (desktop Chromium, mobile Chromium, mobile WebKit), all passing. CI on GitHub is green at `0e68362`.

| Area | Verified how | Not verified |
|---|---|---|
| Jobs and the never-stop supervisor | Real-Claude stop test (refused stop, clean done, ask-answer-done), run by me several times and by every builder since | Recovery from a real usage limit, API error or crash |
| First-run setup (locked start, one-time link, argon2 owner) | Real browser against real herdr and real Claude: link, password, checklist, trust card, greeting in Chat (builder); container proof with curl | A human doing it |
| Doctor, System page, guided checklist, in-app Settings, header machine stats | `crowsnest doctor` on this machine (lead ran): accurate | A Codex-only machine; Linux |
| Tailscale (MagicDNS name auto-allowed; optional sign-in) | Proxy-shaped requests and a faked `tailscale serve` only | **A real `tailscale serve`**: nobody has run it |
| Attachments and paste | Real Claude read a pasted PNG and answered from it (builder) | Real phone camera and library; a job agent reading its inbox |
| Spend per job | Real-Claude run: crowsnest's totals equal the transcript's own sums (builder) | Codex spend ("not measured") |
| Voice: hold to talk, 5 minutes, SAY captions, speak mode, per-agent voices, one queue | **Real ElevenLabs round trip with Jeff's key**: transcription correct, first audio in 0.23 s (builder) | Safari and iOS; a human holding a key; haiku skipped the SAY line in 2 of 5 runs |
| Command palette, Now home, one attention queue, lead-first routing | Hermetic journeys at 390 px and 1440 px; screenshots reviewed by me | |
| Projects and lead agents | Real Claude lead started, took a job, finished it under the hooks (builder) | |
| Structured approvals, needs-you tray, guard hooks | Real Claude: an approval allowed through the API let the command run with no terminal dialog; the hook held 11 minutes (builder) | See "Not fit to tag" |
| Landing: review tab, reviewer job, merge / PR / discard, teardown | Real Claude: builder done, reviewer approved read-only, Land merged, worktree gone (builder) | A reviewer that asks for changes with real agents; a real `gh pr create` |
| Throttling, `crowsnest heavy`, pause switch, presence, daily digest | Unit and hermetic tests; pause checked with real Claude | Load hold on Linux |
| Scheduled work, compaction, Remote Control naming | Real Claude: a scheduled task rode a poke; Remote Control reported active | **Compaction against a real Claude**; the name as shown in claude.ai |
| Web Push | A real Chrome received one real push through Google's service once; two later headless runs delivered nothing | Firefox, Safari, any phone |
| Backup and restore, retention, read-only Machines list | Round-trip tests; real binary backup then restore | A real 30-day database; two real instances |
| Release pipeline (`v0.1.0`) | Workflow green; checksum and cosign verified; released binary runs and self-update check works (lead ran) | `brew install`; the amd64 image |
| Real-herdr suites on main | `mise run test:integration` and `mise run e2e:real` (lead ran, tonight): pass | |

## Not fit to tag: what the reviews found

Six review rounds are in `reviews/`. R1 to R3 (the supervisor and the first security passes) were fixed and verified. Last night:

- **R4, product reviews (Fable and Codex)**. Shared verdict: the supervisor turned "silently stopped" into "declared something", but nothing checked the declaration, so the risk was a board of jobs that are done on paper; the app was organised around mechanisms rather than "what needs me, per project, right now"; nothing has run on a real phone. In response: the Now home and single attention queue, lead-first routing, backup and retention are merged; acceptance checks and budgets, and the phone work, are being built.
- **R5, code (Codex) and security (Opus) review of everything since `v0.1.0`**. Fine against network attackers. Not fit to tag because an agent could satisfy human gates itself: approve its own land request (the approval was not bound to the request), have a land or delegation request allowed from a lock-screen notification button, and start jobs by writing scheduled-task files. Also: common git spellings downgraded dangerous commands to low risk; Land merged whatever the branch was at click time rather than the reviewed commit; restore was not all-or-nothing. **All of these are with fix builders now; none is fixed on main yet.**

## In flight

| Agent | Branch | Doing |
|---|---|---|
| `crow-fix5` | `wip/fix-r5` | The R5 findings: gates bound to their request, no notification buttons on gate questions, task files that start jobs need approval, risk and guard normalisation, protective git flags, restore safety |
| `crow-accept` | `wip/accept` | Make "done" mean something: a check command run by crowsnest and pinned to a commit, Land only the reviewed commit, budgets that pause and ask, a spinning detector, a fleet-wide usage-limit state, every wait with an owner and a wake-up |
| `crow-phone` | `wip/phone` | The phone: a decision screen with a sticky answer bar, rescue buttons instead of the terminal, giving a job by voice with confirmation, "Open in Claude" Remote Control links, per-project approval policy, and a ten-minute real-phone checklist (`docs/PHONE-CHECK.md`) |

After those land: a verification pass by the reviewers on the R5 fixes, the full real-Claude suites on the merged tip, and this file updated with the result.

## Decisions and things only Jeff can do

1. **Use it from a real phone for ten minutes.** Both product reviews rank this first. It needs `tailscale serve` in front (crowsnest prints the command; I have not run it, because it publishes the instance to your tailnet), the app added to the Home Screen on iOS, then push, voice and the terminal key bar tried for real.
2. **The ghcr image is still private**: an anonymous `docker manifest inspect ghcr.io/jclement/crowsnest:0.1.0` returned "unauthorized" when last checked. Package settings, Change visibility, Public.
3. **The next tag.** `main` is far beyond `v0.1.0`. I will not tag it: the reviews say not yet, and releases are yours. When the fixes above are merged and verified, `v0.2.0` is the natural cut.
4. **Pomicons** (11 glyphs in the bundled terminal symbols font) are under an OFL licence with a reserved font name; the builder judged redistribution fine because the font carries Nerd Fonts' name. Say if you would rather drop that range.

## Things I did that you should know

- **Declined, never approved, prompts from your git guard hook.** It misreads literal git text inside a shell heredoc (test data such as `git push *`) as a real git operation, and stopped four builders. I pressed "No" each time and told the builders to write such files with their file tools. My watcher script now declines that one specific prompt automatically; any other dialog still stops and waits for a human.
- **Kept the machine awake** with `caffeinate` for the night (it had slept for seven hours earlier and stalled two builders).
- **Removed a 43 MB built binary** that a builder had committed at the repo root back in the questions milestone. It is out of the tree and ignored; it remains in history and in the `v0.1.0` tag.
- **Stopped one leftover test herdr server** from the first night (a throwaway home under `/tmp`, so it never showed in your session list). `/tmp/cn-*` scratch directories from test runs are still there.
- **Fixed five tests that failed only on a loaded or slow machine** (CI and local): each was a timing or ordering assumption in the test, reproduced before being changed. One CI failure was a real environment bug: the demo's Land needed a git identity the CI runner does not have.
- Repeated "Connection lost mid-response" API errors silently ended several builders' turns. A watcher now resumes any builder that goes idle without having written its report. This is the same failure crowsnest's own supervisor exists to catch; these builders are plain herdr agents, not crowsnest jobs.
- The real ElevenLabs key was read from `~/Downloads/eleven.env` for the voice check and the film's narration (about 2,400 characters), never printed or committed. It is also in the gitignored `mise.local.toml` so `mise run dev` has voice.

## Known holes (DESIGN.md has the full list)

- Agents in one session run as one user and are not isolated from each other; the token scheme stops accidents, not a hostile agent.
- Codex agents get no hooks: no stop-hook guarantee, no structured approvals, no spend measurement.
- Two older review findings remain open and documented: poke reasons can be lost in a narrow window after an event-stream overflow, and the token sweeper can miss a pane id herdr reused.
- Sign in with Tailscale trusts any local process that can open a loopback connection; leave it off on shared machines and on hosts that also run another local proxy.
