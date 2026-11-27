# Testing crowsnest

What is tested, how to run it, what each tier proves, what nothing proves yet, and the bugs the
M9 test campaign (2026-11-26) and the real-Claude browser campaign (`wip/e2e-claude`,
2026-11-27) found and fixed. DESIGN.md section 15 is the strategy; this is the
working guide.

## The tiers

| Tier | Run | Needs | Takes | In CI |
|---|---|---|---|---|
| Go unit and Vitest | `mise run test` (part of `mise run check`) | nothing | ~1 min | yes |
| Go integration (real herdr) | `mise run test:integration` | herdr | ~20 s | no |
| Hermetic browser suite | `mise run e2e` | Playwright browsers (`npx --prefix web playwright install chromium webkit`) | ~5 min under `heavy` | yes, job `e2e` |
| Real-herdr browser suite | `mise run e2e:real` | herdr | ~20 s | no |
| Real Claude, Go | `mise run test:claude` | herdr, Claude logged in; haiku | ~8 min | no |
| Real Claude, browser | `mise run e2e:claude` | herdr, Claude logged in, mise; haiku, about $1.60 API-equivalent a run (each spec prints its measured spend) | ~5 min | no |
| Clean clone builds | `mise run test:clone` | network for `npm ci` | ~1 min | no |
| Screenshots | `mise run screenshots`, `screenshots:chat`, `screenshots:inbox`, `screenshots:jobs` | Playwright | seconds each | no |

Run builds and suites through `heavy` on Jeff's machines. From inside a herdr pane, run the herdr
tiers with the pane's `HERDR_*` variables removed (`env -u HERDR_ENV -u HERDR_PANE_ID -u
HERDR_SOCKET_PATH …`); the helpers scrub them for every child anyway.

### Safety, enforced in the helpers

- Every real-herdr test uses a session named `crowsnest-test-<random>`; the helpers refuse any
  other name, scrub `HERDR_*` from every child, and stop and delete only that session.
- `web/e2e/support/instance.ts` gives every instance its own scratch directory under `/tmp`
  (short, for unix socket paths), and a throwaway `HOME` for real herdr, except the real-Claude
  suite, which needs the real `HOME` for Claude's login (as `test:claude` does).
- The real-Claude suites work only in scratch directories inside the worktree and remove them at
  the end. A scratch git repository is its own root, so Claude asks its folder-trust question
  there; `e2e:claude` answers it from the dialog's decision packet, as a person would. Builder
  jobs get herdr worktrees where herdr puts them (`~/.herdr/worktrees/<scratch repo>/…`, the real
  HOME): Land removes them, and each spec removes its scratch repository's leftovers through
  `git worktree list`. Answering trust records the scratch folders in `~/.claude.json`.

## What each tier proves

### Hermetic browser suite (`web/e2e/hermetic`, `mise run e2e`)

Playwright against `./bin/crowsnest --demo`. Every test starts its own instance (about a third
of a second), so tests run in parallel (four workers locally, three in CI) and never see each
other's answers. Three projects: `desktop-chromium` (1440×900), `mobile-chromium` and
`mobile-webkit` (375×812, touch). Last full run (2026-11-27, `wip/e2e-claude`, under `heavy`): **529 passed, 95 skipped, 5.2 min**. The skips are
deliberate: desktop-only tests (the 20 s sign-out heartbeat, the Node-side refusals, talk mode
with Chromium's fake microphone, dark-theme axe) and phone-only ones (the key bar, the 375 px
layout checks).

| Spec | Journeys |
|---|---|
| `setup.spec.ts` (no login, no `--no-auth`: first-run setup) | a fresh instance is locked: health says `setup_required`, the API (me, fleet, jobs, orchestrator, login) answers `403 SETUP_REQUIRED`, every page lands on `/setup`, and without a link the token field is empty and says where the token is; a wrong token is refused with the server's message and the form keeps its fields; the link read from the instance's log prefills the token and leaves the address bar at once, setup signs in, names the instance (title and `/auth/me`), the orchestrator starts, health stops saying `setup_required`, a second setup is `410`, no request log line holds the token, and the chosen password signs in after the cookies are cleared; the page at 375 px (no sideways scroll, 44 px targets) and axe; a password change in the Account sheet signs a second browser context out and refuses the old password, while this browser stays signed in |
| `auth.spec.ts` (login on) | deep link survives the login detour; reload keeps the session; wrong password; five failures then 429 with `Retry-After` and the form's message, even for the right password; signing out ends another tab's terminal and event stream within a heartbeat and kills the cookie server-side; cross-origin POST, POST without `X-Crowsnest`, `Origin: null`; cross-origin, origin-less and unauthenticated terminal WebSockets (opened from Node so Origin can be forged), each with a positive control; forged `Host` (and a rebinding-shaped login) refused |
| `jobs.spec.ts` | a job given with options launches (queued → running on the timeline); one given without options goes to the orchestrator (Cmd/Ctrl+Enter); a missing directory is refused and the brief kept; board order; message a running agent; answer a job's question inline (back to running, the answer by its label); "Answer in Inbox"; every stuck cause shows its reason and action (Retry now, Open terminal, Nudge); nudge; cancel behind a confirmation; done with verified and not verified; failed; blocked; empty board; unknown job |
| `chat.spec.ts` | send → Working → reply → Idle, Markdown rendered; tool card expands; a send while working is "queued"; the draft warning and "Send anyway" (server refusal played by `page.route`); Restart and New conversation behind confirmations, the new one empty and working; Show thinking; a long conversation (turns appended to the demo transcript) sticks to the bottom only when the reader is there, with an uncovered "N new messages" pill |
| `inbox.spec.ts` | confirm (blocking, diff, mermaid SVG), choice, multi, manual, ack and notice each answered, note included; cancel; a question body with `<script>`, `onerror`, `javascript:` links, an iframe and a mermaid `click` runs nothing (window flag) and renders no script, iframe or handler; a terminal dialog answered from its card, a stale screen hash refused with 409 and the card refreshed, then the real press; badge and tab title follow a question asked over the agent socket and an answer from another tab, live; empty Inbox |
| `fleet.spec.ts` | the tree, needs-you first; collapse; filter and clear; an agent's Conversation then Terminal; reload lands on it; a dialog card in the detail; a Codex agent opens on its terminal; a shell pane's terminal; an unknown agent in the URL |
| `terminal.spec.ts` | connect, type, output; a resize reaches the process (`size`); reconnect by itself after the server is killed and restarted on the same port; an exited terminal waits for Reconnect; the phone key bar sends `\x1b`, `\t`, arrows (either cursor mode), `\r`, and sticky Ctrl+C as `\x03` (bytes read from the WebSocket frames), without taking focus from the terminal; a ninth terminal gets the cap's message, and Reconnect works once one closes |
| `terminal-fonts.spec.ts` | the demo terminal's glyph test line, with the installed Nerd Font switched off: each fallback character (powerline, devicons, box drawing, braille, a sextant, maths) makes the browser fetch its face's chunk, matches pixel for pixel that chunk's glyph drawn by a probe face, and passes `document.fonts.check`; the `|` after them (and a two-cell emoji) sits in its Unicode 11 column within a pixel; fonts come from `/assets/` with the immutable header; no Material Design chunk is fetched; no CSP violation. Screenshots `terminal-glyphs-1440.png` (desktop Chromium) and `terminal-glyphs-375.png` (phone WebKit) |
| `projects.spec.ts` | projects (DESIGN.md 21) against the demo's projects: the list shows each lead's state and counts and a project's page its lead, jobs, schedules and instructions; a project made from a directory (the name follows the directory); a lead started from the page takes a job given "to the lead" (filed in the project, no directory, held by `lead-<slug>`); Chat with a lead (the target in the URL, a reply); the board filtered by project, remembered; a role's instructions edited with the diff against the built-in; a schedule made in the app and Run now. Screenshots `projects-*` at 375 and 1440 |
| `talk.spec.ts` | talk mode against a fake ElevenLabs per test: hold to talk, transcript sent with the voice flag, the SAY reply fetched from `/voice/tts` and played; the browser cannot make crowsnest speak arbitrary text; Cancel sends nothing |
| `layout.spec.ts` | at 375 px on nine views (board, a job, chat, inbox, a question, a dialog, fleet, an agent, terminal): no sideways scroll, every tap target ≥ 44 px; the bottom nav never covers the last row or the composer; theme cycles system → light → dark, and a reload is dark at its first frame; the offline banner when the server dies, gone when it returns; axe on every view and the login page with no serious or critical violations (light everywhere, dark on desktop) |

What it cannot prove: anything herdr or Claude does. The demo's fake herdr and scripted
orchestrator stand in for both.

The specs added since M9, in one line each (their file headers say more):

| Spec | Journeys |
|---|---|
| `now.spec.ts` | Now and the one attention queue at 390x844 and 1440: every obligation found from Now with its project and job; one number (badge, title, Now, Inbox, tray, API) agrees; answering a question and a low approval in place; "since you left" per browser; the nav order |
| `phone.spec.ts` | the decision packet from a push link (the answer bar in reach, the note kept through a reload, rotation and backgrounding, stale after the answer, the receipt shown), approvals from their packets, a diff's packet, rescue without a terminal |
| `tray.spec.ts` | the needs-you tray on every view at both sizes without covering the composer, the terminal or the nav; Allow once, Deny with a note, a dangerous allow's second confirmation, a question from the tray; the keyboard path; axe |
| `landing.spec.ts` | the Review tab and Merge with its confirmation; review rounds; a job waiting for a slot and Start anyway; pause and resume; the digest; the worktree sweep; throttling settings |
| `accept.spec.ts` | Accepted, Unverified and Check failed on cards and the job; crowsnest's evidence and Run the check again; Land offers no override for accepted work; budgets raised; a blocked job's owner and wake-up; the fleet's usage limit banner |
| `policy.spec.ts` | a project's approval rules (preview, add, remove), Allow and remember for this job, a batch allow, the auto-allowed call on the timeline |
| `palette.spec.ts`, `attachments.spec.ts`, `push.spec.ts`, `remote.spec.ts`, `spend.spec.ts`, `doctor.spec.ts`, `ops.spec.ts`, `phone-voice.spec.ts` | the command palette; attachments in Chat, jobs and questions; Web Push against a fake push service; Open in Claude links; spend chips, charts and the breakdown sheet; the guided first run and System page on scripted machines; backups, machines and Tailscale sign-in on System; a job by voice from Now with a fake ElevenLabs |


### Real-herdr browser suite (`web/e2e/real`, `mise run e2e:real`)

11 tests: the demo Fleet and conversations with no fixtures; on a throwaway session, the TUI
renders herdr and runs a command, the pane view runs a command, a takeover ends the first
controller; a tab created with `herdr tab create` appears in Fleet without a reload; a pane
running a script that prints a Claude-shaped permission dialog and reports itself blocked
(`herdr pane report-agent`) is listed in the Inbox, its risky option badged, and pressing "3"
delivers exactly `3` to the script (it writes what it read to a file); with the login on, the
real server refuses terminal WebSockets without a session, cross-origin or without an Origin,
and opens them done properly.

### Go integration (`mise run test:integration`)

The socket client, `worktree.create`, the pane and TUI bridges, takeover (M2, M5), and since M9
the fleet cache against a real session: with the poll set to a minute, a new workspace (seen in
~0.3 s), a new tab, an agent reporting itself blocked and then idle are all followed by herdr's
events, with a revision bump and a published `fleet` event.

### Real Claude (`mise run test:claude`, `mise run e2e:claude`)

`test:claude` (Go): orchestrator start, prompt, Restart, New; a manual poke; the `ask` MCP tool;
the stop test (an undeclared stop refused, `done`, ask → answer → done).

`e2e:claude` (browser, haiku; `web/e2e/claude`, shared helpers in `rig.ts`). Each spec file
starts its own crowsnest on a throwaway session over its own scratch git repository, with
`--review` set explicitly (`auto` would pick Codex when it is installed). The suite plays the
human only through the browser.

| Spec | Journeys |
|---|---|
| `land.spec.ts` (`--review claude`; the repo has a `mise` `check` task) | a job given from Now for a project ("As a job", builder: its own herdr worktree), Now's project row counts it running, its card is in progress on the project's board; the agent commits and calls done; crowsnest's check passes, a Claude reviewer approves, the job is Accepted with its evidence pinned to HEAD; the Review tab shows one commit ahead of main and the file's diff; Merge's confirmation names the reviewed commit; main has the commit, the job is merged, the worktree is gone and the timeline says "Teardown finished". A builder whose done fails the check (answer.txt must be 42; the brief says 41): "Not accepted" on the timeline, the agent fixes it, Accepted with "passed · exit 0" and the card says Accepted |
| `supervise.spec.ts` (tester jobs) | at 390x844: a question asked with the ask tool reaches Now's queue, its row opens the decision packet, the answer (red, with a note) goes in from the answer bar, the same link then shows the outcome and the receipt turns **Received** on the page without a reload, and the job applies the answer; an undeclared stop is nudged ("Nudged" on the timeline), then cancelled from the UI; a turn ended with a question in prose is refused (`prose_question` nudge) and the agent asks it for real (in the Inbox); pause from the Account menu holds a job given meanwhile ("Waiting to start … paused", still queued, no pane), Resume lets it launch and run to done |
| `approvals.spec.ts` (`--permission-mode manual`) | a Bash call becomes a pending approval (the job `needs_you`/`approval-tool`), its card in the tray shows the command, Allow once runs it and the job ends done (follow-up requests allowed from their decision packets); a second call denied from the tray with a note: the agent quotes the note in its result and the file is never written. The terminal is never touched |
| `lead.spec.ts` (no orchestrator) | a project created in the UI from the scratch repo; Start lead; the lead stops at the folder-trust prompt, its card says "Open lead-orchard's terminal", the prompt is answered from Needs you; the lead comes up idle; Remote Control is active and the card offers Open in Claude (a claude.ai link, new tab); Chat with lead gets a reply; a job given "To the lead" from the board is held by `lead-orchard` and finished |
| `claude.spec.ts` (with the orchestrator) | a chat reply renders; a draft typed into the orchestrator's input through herdr is detected and refuses a send with 409 DRAFT, and is gone when cleared; `crowsnest ask --wait` run by the orchestrator in its pane blocks, is answered in the Inbox, the receipt is Received at once (the long-poll), and the orchestrator reports the answer |
| `firstrun.spec.ts` | a true first run: setup link → password → checklist → Start → the folder-trust card on the checklist → Chat with the greeting |

Real screens it captured are Go fixtures: `internal/fleet/testdata/screens/real_claude_2.1.332_*`
(the idle prompt, a typed draft, the folder-trust prompt). Screenshots: `web/screenshots/e2e-claude-*.png`.

The project lead (`cmd/crowsnest/lead_integration_test.go`) runs the built binary with
`--no-orchestrator` on a throwaway session: the lead knows a codeword only its project's
instruction layer gives, holds and finishes a job given to the project, reports Remote Control
active, and has the repository's `CLAUDE.md` recorded through InstructionsLoaded. The SAY line is
checked to be in the lead's prompt file; haiku's obeying it is logged, not asserted (it skipped
it once in three runs).

### First-run setup (Go)

- `internal/auth/owner_test.go`: argon2id hash and verify (PHC string, fresh salt, malformed and
  over-costly hashes refused without allocating); username and password rules; the stored-owner
  service: setup required, setup, a second setup refused, login with the right and wrong halves,
  a login before setup, the session surviving a restart, the login limiter on stored logins and
  on wrong current passwords; a password change signing other sessions out; `ResetPassword` and
  `ResetSetup` taking effect on a live service; eight setups racing on a file database, one wins.
- `internal/setup/setup_test.go`: the token's shape and freshness; the happy path (owner, name,
  the session it returns, Done); a blank name takes the default; invalid fields cost no attempt;
  five wrong tokens then 429 even for the right one, rotation after ten wrong tokens (the old
  token refused, the new one works); four right-token submissions racing (one wins, the rest
  `ErrDone`); an owner stored by another process.
- `internal/httpd/setup_test.go`: the locked server refuses every other route (me, login, fleet,
  events, jobs, account, admin/update) and both terminal WebSockets before any upgrade, redirects
  pages to `/setup` and serves the files it needs; setup obeys the Origin, `X-Crowsnest` and Host
  rules; wrong token, short password, success (hardened cookie; the token in no response or log),
  `503 STARTING` between completion and the full server, `410` after; the full server signs the
  setup browser in and its login works; the Account routes: password change (wrong current is
  403, not 401; other sessions and this browser's old cookie die; the new cookie works), refused
  for a configured login, and the instance name (stored, live in `/auth/me`, refused under
  `--name`).
- `cmd/crowsnest/setup_test.go`: the start-up decision table (nothing, stored owner, flag and env
  logins with and without a stored owner, `--no-auth`, `CROWSNEST_NO_AUTH`, `SUPPRESS_AUTH`); the
  stored name against `CROWSNEST_NAME`; `serve` with no login: herdr (a fake behind the connect
  hook) is not called and the agent socket does not exist until setup is completed over HTTP,
  then herdr is connected once and the setup browser is signed in on the same port;
  `reset-password` from stdin and from a `--password-file` (a world-readable one refused), its
  refusals without a database or an owner, sessions dying; `reset-setup` refusing without `--yes`
  off a terminal and while a server answers on the port, then `--force`.
- Vitest: `routes/SetupForm.test.tsx` (prefill, the submit rule, the mismatch note, the posted
  body, a wrong token and a 410), `components/account/AccountSheet.test.tsx` (password change,
  wrong current password, a configured login and `--name` read-only, saving a name),
  `lib/password.test.ts` (the strength hint; the fragment token taken out of the URL once).

### Other Go additions in M9

- `internal/httpd/tls_test.go`: `--tls-cert/--tls-key` with a self-signed pair made in the test:
  HTTPS answers, a login sets the `__Host-` cookie (Secure, HttpOnly, `Path=/`, no Domain) that
  works on the next call, plain HTTP on the port gets no app, an untrusting client fails the
  handshake.
- `mise run test:clone`: a fresh clone of HEAD in a temporary directory runs `setup` and `build`
  and the binary prints its version.

## Not covered (be careful what you conclude)

- **A real phone.** The phone projects are desktop browsers at 375×812 with touch emulation: no
  on-screen keyboard (the key bar's focus rule is checked through `document.activeElement`, not a
  real keyboard), no `visualViewport` resizing, no safe-area insets, no iOS autoplay rules.
- **Safari itself.** WebKit installed and runs (`mobile-webkit`), which is Safari's engine, not
  Safari on iOS or macOS. WebKit on Linux in CI is Playwright's build.
- **Real ElevenLabs.** Talk mode runs only against the fake; no real key was used.
- **Codex jobs.** Launch arguments are unit-tested; no Codex agent ran in any tier.
- **Rate-limit and crash recovery against real Claude.** StopFailure (limit, server error), a
  dying agent restarted with `--resume`, compaction re-injection: unit tests with a fake clock
  only.
- **Permission dialogs from real Claude in the terminal.** `approvals.spec.ts` runs in `manual`
  mode, but the PermissionRequest hook answers before the dialog matters; nobody presses the
  terminal's dialog. The hand-made permission fixtures stand. The real trust prompt was captured.
- **Codex as the reviewer.** `--review auto` picks Codex when it is installed; the suites set
  `claude` or `off`.
- **Land's pull request and discard, a rebase job, budgets in the browser, the usage limit**: the
  Go real-Claude tests and the hermetic suite only (`test:claude` has the budget stop; nothing
  real opens a pull request).
- **Voice, push notifications and the phone's lock screen against real agents**: hermetic only.
- **Linux.** Every local tier ran on macOS (quark). CI runs `check` and the hermetic suite on
  Ubuntu; the herdr and Claude tiers have never run on Linux.
- **The login rate limit's global cap** (20 a minute across IPs) and the 128-connection cap:
  unit tests only.
- **Multiple browsers on one pane over a long session, SSE overflow, a crowsnest restart while a
  question is on the long-poll**: unit tests or not at all.
- **First-run setup on real herdr.** The hermetic spec and the Go serve test run setup against
  the fake herdr; on real herdr it was proved through the devbox image (REPORT of wip/setup), not
  by a suite.
- **What a cancelled or failed job's open questions become**: they stay open in Needs you (see
  REPORT of `wip/e2e-claude`); nothing asserts either way.

## Bugs found and fixed by the campaign

| # | Bug | Found by | Fix |
|---|---|---|---|
| 1 | A job's answered question showed the option's key ("→ fri"), not its label ("→ Friday"); manual answers repeated the note | `jobs.spec.ts` | `cd47723`: the job detail carries each question's options; `jobAnswerText` words answers as the Inbox does |
| 2 | On a phone the "N new messages" pill sat under the sticky composer and could not be tapped (WebKit caught it; Chromium tapped through) | `chat.spec.ts` | `402b8f8`: the pill hangs above the composer area |
| 3 | Light theme failed WCAG AA contrast all over (muted text 3.6–4.5:1, warning, accent, success and danger chips on their tints, the demo chip on the dark header); the fleet shell row and the key bar had names that did not contain their visible text; the dialog's screen text scrolled but could not be reached by keyboard | `layout.spec.ts` (axe) | `4d18415`: darker light tokens, a chrome warning token, names that start with the visible text, `tabIndex` on the screen text |
| 4 | A dialog press, an answer, a cancel or a nudge lost its confirmation toast, and on a phone the answer sheet sometimes stayed open: the mutation hooks await a refetch that unmounts the caller, and React Query drops an unmounted caller's callbacks. Also the cause of a pre-existing failure in `mise run screenshots` | the fixtures screenshot run; a phone flake in `inbox.spec.ts` | `c7ca910`: `mutateAsync(...).then` in DialogCard, AnswerForm, the cancel button and the stuck banner |
| 5 | Pressing an option on a dialog of an agent herdr detected but did not start by name (any Claude started by hand) was always a 502: `agent.send_keys` answers `agent_not_ready` | `e2e/real/herdr.spec.ts` | `3b1a916`: the press falls back to `pane.send_keys` (same bytes, already bound to the screen hash) |
| 6 | A send from Chat while crowsnest was typing into the same pane (a poke, an answer, a brief) read the half-typed text as someone's draft and was refused with 409 DRAFT; the message was never sent | `e2e:claude` (2 of 4 runs before the fix) | `420c350`: one typist per pane in `orch.Prompt` (the draft check and the typing under a per-pane lock) |

Found by the real-Claude browser campaign (`wip/e2e-claude`):

| # | Bug | Found by | Fix |
|---|---|---|---|
| 7 | After Land merged a branch, the whole app fell to "Something went wrong!": a Review refetch landing between the merge and the teardown got `"files": null` (`landing.Order` turned an empty list into nil) and the Review tab read `files.length`. The demo's land is instant, so the hermetic suite never refetched in that window | `land.spec.ts` | `82151d3`: `Order` always returns a list; a Go test merges a branch and checks the JSON |
| 8 | `--permission-mode default` (the mode DESIGN.md 8.7 names, and still taken by Claude 2.1.332) stopped crowsnest at start; a project's permission mode refused `manual`, the name the flag offers | `approvals.spec.ts` | `2abd7cd`: both take both |
| 9 | An agent that took its answer and called done inside one 15 s receipt interval left the live pass before its transcript was read: its answer said "Delivered: not yet seen" for good, with "Did it arrive?" offered | `supervise.spec.ts` (the receipt on the decision packet) | `b27cd10`: the tick also reads the transcripts of finished jobs with an answer delivered in the last hour |
| 10 | A lead stopped at its start-up prompt said "Open the orchestrator's terminal and answer it" | `lead.spec.ts` | `e37490a`: the detail names the agent and points at its card under Needs you |

Also fixed: the demo seeded an "unresponsive" job with no agent, so its Nudge could only fail
(`cd47723`); the chat screenshot spec counted a reply the demo made on its own (`c7ca910`).

## Found, not fixed (in DESIGN.md section 20)

- The orchestrator is poked about job agents of jobs the human gave, and about jobs already
  finished, costing a turn each.
- The Jobs form has no model field (the real-Claude suite adds `model: haiku` to the browser's
  own POST /jobs).
- A cancelled job's open question stays open in Needs you (REPORT of `wip/e2e-claude`).
- The e2e code is not linted (`mise run lint` covers `web/src`); the repo's React-hooks rules
  misfire on Playwright fixtures.

## Writing a new test

- A user journey goes in `web/e2e/hermetic/<view>.spec.ts`, importing `test` and `expect` from
  `../support/fixtures`: you get a fresh `app` and `baseURL`. Use `detailRegion` for the
  sheet-or-panel split, `expectNoSidewaysScroll`, `smallTapTargets`, `expectAccessible`.
- To act as an agent, use `../support/agent.ts`: `askAsAgent` (the agent socket) and
  `appendTurns` (the demo orchestrator's transcript).
- Need the login? `test.use({ appOptions: { demo: true, auth: TEST_LOGIN } })`.
- Assert the state a toast reports as well as the toast; never only a transient message.
