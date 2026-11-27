# R4 product and design review (Fable)

Reviewed `main` at `2601308` on 2026-11-26. Read-only; this file is the only thing written.

## Verdict

Crowsnest has solved the problem it was built for in the narrow sense: an agent that ends a turn without declaring a state is caught, and the machinery behind that (claims, budgets, reconciliation) is far more careful than most tools of this kind. But it has not yet reached the goal Jeff stated for this review, which is coordinating many projects from a phone. There are three reasons. First, the supervisor turns "silently stopped" into "declared something", and on `main` nothing checks that declaration: `done` is the agent's word, with no check run by crowsnest, no diff, no review gate and no landing, so the likely day-thirty failure is a board of jobs that are done on paper. Second, the product is organised around mechanisms (Jobs, Inbox, Fleet, Terminal, a tray, a palette) and not around Jeff's question, "what needs me, per project, right now": the same queue is drawn three times with four different counts, and the home screen spends its first 40% on a composer. Third, the phone is a squeezed desktop where it matters most: rescue is a 44-column terminal, the answer buttons sit below the diff, a job cannot be given by voice from the job box, and nothing has ever run on a real phone. The good news is that the two branches in flight (`wip/landing`, `wip/doctor`) aim at several of the biggest holes. My advice is to land those, add the missing mechanical checks, then stop adding surface and reorganise what exists around one queue and one row per project.

## How I looked

- Read `BRIEF.md`, `STATUS.md`, `DESIGN.md` sections 1, 6, 7, 8.1 to 8.3, 8.5, 8.7, 12, 13, 20 and 21, the job protocol and role prompts (`internal/jobs/templates/protocol.md`, `roles/builder.md`, `roles/lead.md`) and the orchestrator template (`internal/orch/templates/CROWSNEST.md`).
- Built `main` and ran `./bin/crowsnest --demo --no-auth` on a spare port. Drove it with Playwright in WebKit at 390x844 and Chromium at 1440x900: every view, the tray, six job states, the new-job form, five Inbox items, a project page, Chat and its menu, a Fleet agent's terminal, the palette, and the Schedule, Journal and Spend sheets.
- For the in-flight branches I read commit subjects and parts of their design text only. Where a point is probably covered I say "in flight".
- Sizes (S/M/L) are my estimates. Numeric thresholds I suggest are starting points to tune, not measured values.

Tags: **P0** blocks the goal, **P1** big win, **P2** polish.

---

## 1. Coordinating many projects from one place

### 1.1 Four different "needs you" numbers on one screen (P1, S)

**Saw.** On the phone home screen at the same moment: the tray bar says 12, the Inbox badge says 11, the board's "Needs you or stuck" section says 4, the project switcher says "All 4", and Fleet says "1 need you". Each counts a different set (jobs, questions plus approvals plus dialogs, tray rows, agents).

**Why it matters.** The one number Jeff reads at a glance has to be trustworthy. With five projects he will not reconcile five counts; he will stop believing all of them.

**Recommend.** One definition, "items that will not move until you act", computed once on the server and used by the badge, the tray, the tab title, the push count and the board. Split it into two visible numbers at most: blocking (work is stopped) and can-wait (a recommended default exists, or it is a notice). Notices and acks should not count as blocking.

### 1.2 The home screen is a composer and a job list, not the state of the fleet (P1, M)

**Saw.** On the phone the first job card starts about 40% down the screen: header, project switcher, the "What needs doing?" box, attach buttons, a spend line, Schedule and Journal all come first. On desktop the right 55% of the home view is an empty "Pick a job" panel while the needs-you stack floats over it.

**Why it matters.** On a busy day the first screen should answer three things without scrolling: what needs me, what is each project doing, what finished since I last looked. Giving a job is frequent but not the first thing he does when he opens the app.

**Recommend.** Make home a "Now" view:
1. The needs-you queue, answerable in place (the tray's row component already does this well).
2. One row per project: lead state, running and queued counts, the oldest thing waiting on him, the last thing that finished, and one line of what it is doing now.
3. "Since you last looked": finished and failed jobs (the digest on `wip/landing` is the natural source).

Shrink the job box to one line with a mic; on a phone make it a floating action button. On desktop, show the first needs-you item in the right panel instead of the empty state. Move Spend, Schedule and Journal into a menu.

### 1.3 Needs-you items do not say which project or job they belong to (P1, M)

**Saw.** The tray's top item is "Bash: git push --force origin main · Dangerous approval · reviewer". The next is "Apply the fleet cache refactor? · builder". No project, no job title. The force-push approval does not appear in the board's "Needs you or stuck" section at all, because the board lists jobs and that approval hangs off a pane.

**Why it matters.** With five projects there will be five agents called `builder` and five called `reviewer`. "reviewer wants to force-push" is not a decision he can make without knowing where.

**Recommend.** Every approval, question, dialog and stuck notice carries a project chip and the job title first, the agent name second. Name job agents `<project>/<role>-<n>` rather than by role. Show a job's pending approvals on its card and in its detail.

### 1.4 Fleet and Jobs are two unlinked trees of the same agents (P1, M)

**Saw.** Fleet groups by herdr workspace ("website", "crowsnest", "lead crowsnest") and shows rows like "builder · claude · …/src/crowsnest". No job title, no progress line, no link to the job. Jobs shows the same agents as a chip on a card. The bottom nav has six items at 390 px: Jobs, Projects, Chat, Inbox, Fleet, Terminal.

**Why it matters.** Fleet is herdr's model; Jobs and Projects are Jeff's. Keeping both at the top level makes him translate between them, and six tabs is the limit of what fits.

**Recommend.** Four tabs on a phone: Now, Projects, Inbox, Chat. Fleet becomes an "Agents" section inside a project (and an all-agents debug view from the menu) where each row shows the job it holds and its progress line. Terminal moves inside a job or agent (see 3.1).

### 1.5 The same queue is drawn three times (P2, M)

**Saw.** The tray (every view), the Inbox list, and the top section of the board all list things waiting on Jeff, in three orders with three row designs. On a phone the tray bar and bottom nav are always present, and the Inbox tab is a third entry point to the same items.

**Recommend.** One queue component and one order. On a phone the Inbox tab is the tray: drop the persistent bar on the Inbox and Now views, keep it elsewhere. Keep History in the Inbox.

### 1.6 Project scope exists only on the Jobs board (P1, M)

**Saw.** The project switcher is a row above the job box. Chat has its own target select. Inbox, Fleet and the tray are unscoped. On the phone header the instance and session chips are truncated to "q…" and "crow…", which carry no information.

**Recommend.** Put one scope switcher in the header (All, each project) in place of those chips, remembered on the server, and apply it to Now, Inbox, Chat's default target, Agents, Spend and Schedule.

### 1.7 The orchestrator and the leads overlap, and the overlap costs turns (P1, M)

**Saw.** Under "All" the job box defaults to "To the orchestrator"; under a project it defaults to the lead. DESIGN 21.2 says "The orchestrator is still poked for every job, project jobs included", and section 20 records that it is poked for jobs the human gave from the browser and "answers `noop` or worries about jobs it did not start. It costs a turn each time." A stuck job tells the lead (a poke) and Jeff (a push) at the same moment.

**Why it matters.** A turn of an orchestrator near its 150k compaction threshold is not free, and two agents hearing the same event invites two agents acting on it. Nothing in the UI shows who owns a job.

**Recommend.** State the division and enforce it:
- **Leads own their project.** They hear about their jobs, verify `done`, re-plan stuck work.
- **The orchestrator is the router and the cross-project summariser.** It hears about unfiled jobs, jobs it created, and escalations (a lead that is off, stuck, or failed to unstick something). It is not poked for project jobs.
- **Escalation is a ladder:** job agent, then lead, then Jeff. A stuck job reaches Jeff when the lead could not clear it within a set time, or at once for auth, billing, and anything dangerous.
- Show the chain on every job: "you → lead Crowsnest → builder j_12".

With projects in use, consider making the orchestrator optional by default; `--no-orchestrator` already works.

### 1.8 "Blocked" sits under "In progress" and nobody is told (P1, S)

**Saw.** "Upgrade the Postgres driver · Blocked: Waiting for the staging database to come back" is listed under IN PROGRESS. STATUS lists agent-declared `blocked` as a way a job can stop with nobody told. See 2.4 for the mechanism.

**Recommend.** A third board group, "Waiting on something else", showing how long and when it will be checked again.

### 1.9 Cards show last activity, not how long Jeff has been the bottleneck (P2, S)

**Saw.** "Decide the release date for v0.2 · Needs you · active 29 min ago". For a needs-you job the useful figure is "waiting on you 29 min". Cards also lead with internal ids (`j_4`) and role names.

**Recommend.** For needs-you items show the wait; sort by it within a priority band.

---

## 2. The agentic-coding loop

Ranked by how much pain each removes.

### 2.1 `done` is the agent's word, and the supervisor pushes agents toward saying it (P0, M; partly in flight)

**Saw.** A finished job's detail shows Result, Verified and Not verified, all text the agent wrote. On `main` there is no diff, no commit list, no check run by crowsnest, no review and no landing action. The protocol says "Never claim done over a failing suite", which is an instruction, not a check. Meanwhile the Stop hook refuses every undeclared stop, so the cheapest way for an agent to stop being nudged is to call `done`.

**Why it matters.** This is the second half of Jeff's complaint waiting to happen: "it says it finished and it had not". A supervisor that only enforces declarations rewards premature ones.

**Recommend.** A mechanical gate on `done`, run by crowsnest, before any review:
1. Each project has a check command (for Jeff's projects, `mise run check`). On `done`, crowsnest runs it in the job's worktree through `heavy`. Red means `done` is refused and the output goes back to the agent.
2. Refuse `done` for a builder with a dirty worktree or no commits on its branch.
3. Refuse `done` while the job has an open blocking question.
4. Make `done_when` a checklist the agent must answer item by item with evidence, shown on the result.

Then the review gate and Land from `wip/landing`. Suggested additions to that branch: structured findings (severity, file, line) with the open count on the card; the builder cannot close a finding itself; after two rounds it goes to Jeff.

### 2.2 The commonest real stop, a question asked in prose, is nudged instead of converted (P1, S)

**Saw.** The demo's own orchestrator reply ends "Want me to start the next milestone?" in prose. For a job agent that is the classic stop. The Stop hook input carries `last_assistant_message`, but the refusal text is generic ("You have not finished job j_12 and have not said why you are stopping"). The stuck card for `j_6` says "Ended its turn 4 times in a row without declaring anything" and offers Nudge; what the agent last said is a tab away in Conversation.

**Recommend.**
- On Stop, if the last message reads as a question to the human, refuse with a specific reason: "You asked the human a question in your reply; they will not see it. Call `ask` with it, give a recommendation, and carry on with what it does not block."
- Put the agent's last message on the stuck card and in the push.
- Make the nudge specific: quote the last progress line and ask which of done, blocked, ask or continue applies.

### 2.3 Nothing limits cost, and a busy loop looks healthy (P0, M; concurrency in flight)

**Saw.** Spend is well measured ($14.87 today in the demo) and nothing acts on it: budgets are designed in 6.7 and not built. The nudge budget only counts turns with no tool call, so an agent that makes tool calls forever never trips it. Each job that hits a usage limit retries by itself at 5, 15 and 30 minutes, though on a subscription a limit applies to all of them at once. On `main` nothing caps how many agents run (in flight: slots, a load hold, `crowsnest heavy`).

**Recommend.**
- Default soft budgets per role in minutes and estimated dollars: warn at 80%, pause at a safe point at 100% and ask "raise or stop" with one tap to raise. Plus a daily cap for the instance.
- A "spinning" detector: many tool calls or dollars with no new commit and no changed progress line.
- A fleet-wide limit state. When one agent reports a usage limit, mark the account limited until the reset time, show one banner, hold new starts and retries, and resume everything once. On a subscription the limit matters more than the dollar estimate; show time to reset beside spend.
- Land the slot limits with conservative defaults and show queue position on the card.

### 2.4 `blocked` is an unconditional exit from supervision (P1, S)

**Saw.** `job_update(state: blocked, reason)` satisfies the Stop hook, and the backstop and idle checks only watch `running` jobs. An agent that wants to stop can say "blocked: waiting for staging" and rest for ever.

**Recommend.** `blocked` requires a `recheck_in`. Crowsnest wakes the agent then with "check whether <reason> has cleared". After two rechecks or a few hours it escalates up the ladder in 1.7. Tell Jeff at normal urgency when a job blocks.

### 2.5 Briefs are one text box, and that makes weak briefs (P1, M)

**Saw.** The title is the first line; "done when" is behind a disclosure; there is no model field (noted in section 20), no budget and no check command. From a phone, by voice, a brief will be one loose sentence.

**Recommend.** Let the lead sharpen it. The lead restates the job in a few lines (goal, done when, out of scope, how it will be checked), writes that to the job record where Jeff can see and amend it, and starts without waiting. Per-project defaults carry the rest: check command, model per role, budget, protected branches.

### 2.6 Approval fatigue under the default permission mode (P1, M)

**Saw.** The tray holds "Bash: go test ./internal/fleet/…" as a low-risk approval needing a tap. A browser allow is once only, by design (8.7). So Jeff either taps all day or runs everything in a looser mode, where the risk tiers stop helping.

**Recommend.** Keep "no permanent grants from a phone" and add three narrower things:
- A per-project allowlist held by crowsnest (test and check commands, edits inside the worktree). Matches are allowed by the hook at once and journalled.
- "Allow and remember for this job": scoped to the job, gone when it ends.
- Batch: "Allow 3 low-risk calls from builder".

Dangerous stays card-only with the second confirmation.

### 2.7 Landing and merge pile-ups (P0, M; in flight)

**Saw.** On `main` a finished job's branch and worktree simply remain; nothing merges or removes them. `wip/landing` adds merge, pull request, discard, teardown and a sweep.

**Recommend, on top of that branch.** A landing queue per project that serialises: rebase on the target, re-run the check, merge, remove the worktree. A conflict goes back to the builder as a message. Show "3 ready to land" on the project row. Give each project a land policy (merge locally, open a pull request, never) and its protected branches; the guard's protected-branch list defaults to none, which is wrong for any repository like Barreleye where `develop` must not be committed to.

### 2.8 Two writers in one checkout (P1, S)

**Saw.** A lead runs in the project's main checkout and is told to "do small, quick things yourself". Builders get worktrees, but `isolate: false` jobs and reviewers in a builder's worktree share a tree with another agent.

**Recommend.** One writing agent per checkout, enforced by the guard (an edit in a checkout another agent holds is refused with the reason) and shown on the project page. Consider making the lead read-only in the main checkout.

### 2.9 Leads are the longest-lived agents and get the least upkeep (P1, M)

**Saw.** The orchestrator has compaction at 150k tokens, an optional daily fresh conversation with a hand-over, and spend metering. Section 20 says leads have none of these: no compaction by crowsnest, no hand-over, and their own conversation is not metered.

**Recommend.** Run the same tender for leads (the code is already generalised), at a lower threshold. Give each lead a notes file it maintains and that SessionStart re-injects. Meter leads; otherwise the largest spender on the page shows nothing.

### 2.10 The never-stop promise depends on undocumented Claude Code behaviour (P1, S)

**Saw.** Section 20: the transcript format and hook shapes were probed on 2.1.332; StopFailure, Notification, PermissionDenied and SessionEnd are covered by unit tests only.

**Recommend.** Record the Claude Code version each real-Claude test last passed on. When the installed version differs, show a banner and offer a short canary run (`doctor --deep` on `wip/doctor` is the natural home).

### 2.11 A crowsnest restart drops every pending approval back to the terminal (P1, S)

**Saw.** 8.7: pending approvals are superseded when crowsnest restarts, since the hooks' long-polls died with it. The hook itself already tolerates two minutes of failed polls.

**Why it matters.** After every upgrade Jeff gets a set of raw terminal dialogs instead of cards.

**Recommend.** Keep an approval pending across a restart if its hook polls again within that window; supersede only the ones that do not.

### 2.12 Scheduled pokes count as run when delivered (P2, S)

**Saw.** Section 20: a task's run is "the poke carrying it was delivered", not that the work happened.

**Recommend.** Make `as: job` the default for scheduled work so it has evidence and spend. Keep poke-type for "look at the board".

---

## 3. Phone use

### 3.1 Nothing has run on a real phone (P0, S to start)

**Saw.** STATUS and section 20: no real iPhone, no real Safari, Web Push to Apple never exercised, `tailscale serve` never tried, the microphone and audio unlock checked only in Playwright's WebKit.

**Recommend.** Before any more features: one day of real use from the phone, over `tailscale serve`, installed to the Home Screen with push on. Keep a list. Everything else in this section is ranked below that.

### 3.2 Rescuing a stuck agent means a 44-column terminal (P1, M)

**Saw.** At 390 px the Terminal view reports 48x24 and a pane's terminal 44x24 at the default 13 px. The key bar shows Esc, Tab, Ctrl, arrows and Enter; paste is half visible and font size is off the right edge in the pane view. Claude Code's interface and herdr's are not usable at that width. The stuck job's actions are Nudge, a message box and Cancel.

**Recommend.** Design rescue as actions, not as a terminal:
- On any stuck or needs-you job: Interrupt, Nudge, Message, Restart, Restart with a fresh context, Cancel, and the dialog's own options when one is up (the job detail for `j_5` offers only "Open terminal"; the options are in the Inbox).
- A read-only "last screen" as reflowed text.
- When a real terminal is needed: open the pane (not herdr's TUI), at 80 columns with horizontal pan and a smaller default size.

### 3.3 Answering a question means scrolling past its evidence (P1, S)

**Saw.** Question `q_1` on the phone: title, chips, text, a 700 px diagram, a diff, and only then the options. The tray answers in one tap, but its preview prints raw markdown ("The refresh now `**coalesces**` events") and cuts the body.

**Recommend.** A sticky answer bar at the bottom of the question sheet: the options, the note, the mic. Render or strip markdown in the tray preview.

### 3.4 What a lock-screen notification should do (P1, M)

**Saw.** 8.7: action buttons appear only where the platform allows (Chrome on desktop and Android). On iOS a push is plain, so every approval is unlock, open, find, tap. Payloads are a 60-character title and 160-character body. All push kinds are on by default, including `done`.

**Recommend.**
- The body carries the decision: project, job, the command or question, the recommended option. Jeff decides on the lock screen even if he must open the app to tap.
- The tap lands on that card with the primary action under his thumb.
- Default to pushing only blocking items; collapse the rest per project ("Crowsnest: 3 things can wait") and leave finished jobs to the digest. Presence and do-not-disturb are in flight; push volume is what will get notifications switched off by week two.
- Worth a spike, not verified by me: whether a relay behind `--notify-url` can give actionable notifications on iOS.

### 3.5 A job cannot be given by voice from the job box (P1, S)

**Saw.** The job box has attach and camera buttons and no microphone. The mic exists only in Chat's composer and the Inbox note. So a spoken job goes to whichever agent Chat targets and relies on it to file the job.

**Recommend.** A mic on the job box, and on a phone make it the main button on the Now view: hold, speak, see the transcript with a project chip ("Job for: Crowsnest"), confirm.

### 3.6 Reading a result and a diff (P1, M; partly in flight)

**Saw.** The finished job shows the result text and no diff on `main`. Where a diff does appear (a question's `files`), the phone shows two line-number columns and wraps code mid-word at about 38 characters. In a running job's detail, a full screen of Spend (a token table, a cache meter, a bar chart captioned "Point at a bar for the call", which is a hover instruction on a touch screen) comes before the timeline.

**Recommend.** On a phone: unified diff, one narrow gutter, horizontal scroll per hunk, files collapsed with their counts first. In job detail put progress, the last few assistant lines and the timeline first; collapse Spend to one line. The walkthrough on `done` from `wip/landing` is the right reading surface: make it the first thing shown.

### 3.7 Crowsnest and Remote Control (P1, S for the link)

**Saw.** Crowsnest starts the orchestrator and each lead with `--remote-control "<instance> <project>"` and watches the pane for the "active" banner, which includes the session URL. The web app shows only a text note on the lead's controls ("Remote Control (name): active"); there is no link to the session anywhere in `web/src`.

**How they should divide.**

| | Remote Control (Claude app) | Crowsnest |
|---|---|---|
| Sees | One agent's conversation | Every project, job, question, approval, spend |
| Good at | A long back-and-forth with one lead; native app, native notifications | The queue across projects; supervision; risk-tiered approvals; landing |
| Blind to | Jobs, other projects, the supervisor's state | Nothing in the session, but its chat is a weaker client |
| Reach | Anywhere | The tailnet |

**Recommend.**
- Capture the session URL from the banner and put "Open in Claude" on each lead, in Chat's header and on the project row. This is the cheapest high-value item in this review.
- Do not compete with the Claude app at chat. Keep Chat lean and spend the effort on the queue.
- Test the two-surface cases with a real session: a permission answered in the Claude app (should supersede the card), and crowsnest typing a poke while Jeff is mid-message in the app (does draft detection see it?). I could not test either.
- Keep job agents' Remote Control off; ten jobs would flood the session list.

### 3.8 Smaller phone items (P2, S each)

- **Chat composer.** Four buttons share the row with the text box, which is left about 40% of the width; the placeholder wraps to two lines. Put the box on its own row.
- **Fixed chrome.** Header, tray bar and bottom nav are always present; on Jobs the switcher and composer add more. The list gets about half the screen.
- **No offline shell.** The service worker caches nothing, so off the tailnet the app does not open. Cache the shell and the last snapshot, marked "as of 3 min ago".
- **Footer tagline** takes two lines on a phone.

---

## 4. Voice

### 4.1 It is dictation plus a one-sentence reply, not a hands-free loop (P1, L for the full loop)

**Saw.** One press per utterance. The transcript is confirmed by showing it for 1.5 seconds. Questions have a "Read aloud" button and are answered by tapping; only the note is dictated. There is one chime. Stopping speech is a button. The mic goes to whichever agent Chat targets. Agents are told apart by voice alone, and a line older than two minutes is dropped silently.

**Why it matters.** Walking or driving, every one of those needs eyes or a thumb. And a web page cannot hold a microphone with the screen locked on iOS, so "hands-free" has a platform ceiling worth admitting early.

**Recommend, in order.**

1. **Cheap fixes (S each).** Speak back a short echo of what was heard, with "say cancel" or a tap to stop. Pressing talk stops playback (a cheap barge-in). Distinct earcons for heard, sent, needs-you arrived, job done and error. A short spoken project name before each line ("Crowsnest: …"): five voices are hard to tell apart by timbre, and I would reverse the "never a name prefix" decision in 13.5.
2. **Walk mode (L).** A full-screen page that holds a screen wake lock and runs the needs-you queue aloud in priority order: "Crowsnest, builder asks: apply the fleet cache refactor? Recommended: apply it. Say apply, not yet, skip or later." Match the spoken answer to option labels and numbers, read it back, allow a two-second cancel, then answer. Listen automatically after each item. Never accept a spoken allow for elevated or dangerous approvals; say "this one needs your eyes". Summarise what was missed rather than dropping it. In this mode free speech goes to the orchestrator as router.
3. **Skip a wake word.** It is a large build for a web page and the wake lock plus auto-listen covers the walking case.

For driving, check first whether the Claude app's own voice mode works with Remote Control sessions; if it does, that is the better tool and crowsnest need only make its leads answer "what needs me?" well. I have not verified that it does.

### 4.2 Voice has not been heard on a phone (P1, S)

**Saw.** Section 20: one real ElevenLabs check from a Mac; Safari on an iPhone never. Part of 3.1.

---

## 5. First five minutes and day thirty

### 5.1 The real first hurdle for the phone is HTTPS (P1, S; partly in flight)

**Saw.** The microphone, Web Push and clipboard paste all need a secure context. `tailscale serve` is the documented answer and has never been tried. `wip/doctor` adds a first-run checklist, Tailscale name detection and a doctor section with the serve command.

**Recommend.** Add a phone checklist to that first run: a QR code for the HTTPS address, "Add to Home Screen", "Turn on notifications", "Send a test push", "Say something".

### 5.2 State piles up and nothing removes it (P1, M; worktrees in flight)

**Saw.** The board has "Finished today" and no history view. No code on `main` archives or deletes jobs, and nothing removes a worktree (the sweep is on `wip/landing`). Each job leaves a state directory. The board and the spend meter read only the newest 300 jobs.

**Recommend.** A History view with search by project and text. Archive finished jobs after a set number of days. Remove a worktree when its job lands and a week after a discard. A storage card on the System page and a `crowsnest gc`.

### 5.3 No backup (P1, S)

**Saw.** One SQLite file per session holds projects, instruction layers and their revisions, schedules, questions, push keys and subscriptions. I found no backup or export command in the README.

**Recommend.** `crowsnest backup` (an online SQLite backup plus the state directory), run nightly by a built-in schedule, with restore documented.

### 5.4 More than one machine (P1, L; needs Jeff's decision)

**Saw.** Remote machines are a stated non-goal: one process, one herdr session, one machine. Jeff works on at least three (quark, bedev, relic). The instance name in titles and pushes shows this was foreseen.

**Why it matters.** "Coordinate lots of projects from one place" becomes one installed app, one inbox and one set of notifications per machine.

**Recommend.** Decide now, because it shapes the home screen. Either crowsnest runs on one machine and the others are out of scope, or one instance's web app can add other instances and merge their needs-you queues and project rows. The second is the product Jeff described; I would do the simplest version of it (a list of peers, a merged queue, each item labelled with its machine) before more single-machine polish.

### 5.5 Security for an always-on shell (P1)

The fail-closed defaults, origin checks, risk tiers and guard are good. What I would change:

- **The Terminal view reaches every machine in herdr's config (S).** Section 20 says so. The guard stops agents from reaching protected hosts; it does not cover a human, or anyone holding a stolen session, in that terminal. If Jeff's herdr config names a production host, a phone session is a path to it. Default the Terminal view to this session's panes and put herdr's TUI behind a flag or a fresh sign-in.
- **Sign-in (M).** One password, no passkeys, no "sign out everywhere", and a peer can keep new logins locked out. On a tailnet, use Tailscale's identity as the login: stronger, and nothing to type on a phone.
- **A persistent injection path (S).** Section 20: any agent can write `~/.config/crowsnest/roles/*.md`, and that text joins every future job's system prompt. A page one agent reads can so instruct every later agent. Record a hash of each role file when Jeff accepts it and raise a needs-you item when one changes.
- **Instruction edits from a phone session (P2, S).** They steer every agent; ask for the password again.

### 5.6 Settings live in too many places (P2, M)

**Saw.** Flags, environment, a config file, a database layer (on `wip/doctor`), `kv` rows, and `localStorage` per browser for speak mode, per-agent speaking, snooze, the chime, the chosen project and palette history.

**Why it matters.** Snoozing an item on the phone does not snooze it on the desktop, and the push still arrives.

**Recommend.** Move snooze and preferences to the server. Keep only audio unlock and font size per device.

---

## 6. What to cut, freeze or relabel

| Thing | Why | Do |
|---|---|---|
| **Codex as a general job engine** | No hooks, so backstop only; spend "not measured"; tested only against a fake; launched with `--ask-for-approval never`. The never-stop promise does not hold for it, and the engine picker does not say so. | Limit Codex to the reviewer role until verified with a real run. Label it "limited supervision" in the picker. |
| **Fleet as a top-level view** | herdr's model, not Jeff's (1.4). | Fold into Projects; keep a debug view. |
| **herdr's TUI as a phone tab** | 48 columns, and it reaches other machines (3.2, 5.5). | Remove from the phone nav. |
| **Global and role instruction editor with revisions, diffs and restore** | A second source of truth for agent guidance, edited from a phone and outside git, beside CLAUDE.md and the claude-agent layers Jeff already keeps. The "file wins, app text is shadowed" rule is a sign of the overlap. | Keep the per-project text and the "what this agent loaded" card. Freeze the rest; offer export to files. |
| **Task files and app schedules together** | Two sources, one name space, shadowing rules. The `poke` and `change` cadences are hard to predict. | Keep app schedules; import files once. |
| **Spend detail** | Per-call bar charts, by-model and by-day tabs and CSV for an estimate that is "not a bill" on a subscription. | Keep one figure per job and per day. Spend the effort on budgets and the limit state (2.3). |
| **Daily fresh conversation with hand-over** | Off by default and never run against a real Claude. | Verify it or remove it. |
| **Three notification channels** | The in-page Notification API, `--notify-url` and Web Push. | Keep Web Push and `--notify-url`. |
| **Snooze per row in the browser** | Device-local; presence is in flight. | Replace with server-side presence. |
| **Command palette on a phone** | A full-screen sheet duplicating the nav. | Keep on desktop; on a phone give the header slot to the project scope. |
| **Demo data** | Job `j_5` says the dialog is `rm -rf node_modules`, its dialog card says `rm -rf dist`; the agent "docs-writer" holds a review job in Crowsnest and sits in the "website" workspace. | Make the demo one consistent story across five projects; it is the product's first impression and the test bed for section 1. |

---

## Do these next, in order

1. **Use it from the real phone for a day** over `tailscale serve`, installed, with push and voice (3.1, 4.2, 5.1). P0, S.
2. **Make `done` mean something**: the check command run by crowsnest, a clean tree and commits, then land the review gate and Land (2.1, 2.7). P0, M.
3. **Stop runaway cost**: budgets that pause and ask, a fleet-wide usage-limit state, a spinning detector, slot limits (2.3). P0, M.
4. **Fix the commonest stops at the source**: turn a prose question into an `ask`, show the last message on the stuck card, make `blocked` carry a recheck (2.2, 2.4). P1, S.
5. **One queue, one number, every item labelled with project and job** (1.1, 1.3, 1.5). P1, M.
6. **Home becomes "Now"**: the queue, one row per project, since-you-left (1.2, 1.6). P1, M.
7. **Settle orchestrator and leads**: no orchestrator pokes for project jobs, lead-first escalation, lead upkeep and metering (1.7, 2.9). P1, M.
8. **Phone actions**: rescue without a terminal, a sticky answer bar, a mic on the job box, "Open in Claude" links (3.2, 3.3, 3.5, 3.7). P1, M.
9. **Approval policy**: per-project allowlist, remember-for-this-job, batch allow, approvals that survive a restart (2.6, 2.11). P1, M.
10. **Day-thirty basics**: decide the multi-machine story, backup, retention, the Terminal view's reach, Tailscale sign-in (5.2 to 5.5). P1, M to L.

After those: walk mode for voice (4.1), then the cuts in section 6.

## What I did not look at

- **A real session.** Everything I saw ran in `--demo` with the fake herdr and scripted agents. I did not run real herdr, real Claude or Codex, so every claim about agent behaviour comes from the design text, the prompts and the limitations list.
- **A real phone.** Playwright WebKit at 390x844 only. No Home Screen install, no push delivery, no microphone, no audio.
- **Voice in use.** I read the design and saw the controls; I heard nothing and recorded nothing.
- **Sign-in and first-run setup.** I ran with `--no-auth`.
- **The two in-flight branches.** Commit subjects and parts of their design text only; I did not build or run them. Items marked "in flight" may be fully covered.
- **Code correctness.** This is not a code review; I read prompts and templates and grepped for specific facts.
- **`README.md` and `docs/TESTING.md`.** I searched the README for specific points and did not read either in full.
- **Parts of the demo walk.** On the phone my script failed to open the account menu, so the Voice, Notifications, Instructions and Account sheets were captured on desktop only, and I did not study those captures. Of the desktop captures I studied the Jobs home; my desktop findings are otherwise from the design text.
- **DESIGN.md outside the sections named above**, including the security model (section 4), the API and the key decisions list.
