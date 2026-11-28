# R11 (Opus): usability and awesomeness, by using it (round 2 of 3)

Reviewed `main` at `135dac1` (binary `v0.1.0-508-g135dac1`) on 2026-11-28 by **using** a demo
instance (`serve --no-auth --demo`, port 28220), driven through Playwright: Chromium and WebKit at
390x844 with touch, Chromium at 1440x900; light, dark, Phosphor, Deep sea, Ember and High contrast.
Screenshots are in
`/private/tmp/claude-501/-Users-jsc-Work-crowsnest/a601de7a-f56d-42f3-a3a3-9aa2e400b841/scratchpad/shots/`
and are named below by file name (without `.png`). The scripts are in `../scripts/`.

**What I changed, all inside my own demo's throwaway state:** gave about 50 jobs (40 of them a load
test into Mobile), answered about 8 items, merged j_25, sent j_18 back with a line comment, replied
to the stuck j_6, created a project called `Work`, and froze my demo server with SIGSTOP for 12 s to
test a stalled server. This file is the only repository file I wrote.

**Deviations.** I unset `ELEVENLABS_API_KEY` for the demo, as round 1 did, so voice is **not
verified** beyond the Settings text that explains how to turn it on. On start, the demo logged
"Tailscale name allowed as a host"; that is the binary's own detection, not a command I ran.

**Long TMPDIR: fixed.** The demo started with a 106-byte TMPDIR inside the scratchpad. Its socket
went to `/tmp/crowsnest-501/…`.

Sizes: **min** (minutes), **hrs** (hours), **day**.

---

## 1. Round 1, item by item: is it really fixed?

`O-` is R10 Opus, `F-` is R10 Fable. **Fixed**: works as asked. **Partly**: some surfaces are done
and others are not. **Badly**: fixed in one place, and now the views disagree.

| Item | Verdict | Evidence |
|---|---|---|
| O-B1 the next decision slides under a double tap | **Fixed** | A double tap 250 ms apart answered one item only. The next item's buttons are inert, and a "Next up:" line explains the change (`j3b-webkit-after-doubletap-750ms`) |
| O-B2 the picker warns about the home folder, then allows it | **Fixed** | Select this folder is off at `~` (`j7-webkit-picker`). New: `~/Work`, a parent of repositories, is accepted with only "not a git repo" (`j7b-webkit-form-filled`) |
| O-B3 a sent-back job disagrees with itself | **Badly** | The card, the header ("Changes requested") and "1 to land" now agree (`j5b-j_18-sent`). But the **queue still says "Ready to land"** for j_18 on Now, the Inbox, the tray and the palette (`sweep-desk-ember-_inbox`, `load-desktop-chromium-contrast-_`; the API's attention detail reads "Ready to land: job/91-…") |
| O-B4 queued jobs counted as running | **Badly** | Now's rows are right ("1 running · 41 queued"). The Jobs chips say **"59 running"** while the API has 12 running and 44 queued. The Jobs line says "15 of 32 running · 1 waiting". Projects says "Unfiled: … 7 running". These are three numbers for one fact (`counts.mjs`, `kbd.mjs` output, `j6-phone-projects-phosphor-dark`) |
| O-B5 / F-B1 a Settings section link scrolls the frame | **Fixed** | Choosing a Section leaves `scrollY` at 0 and the header in place (`j8-chromium-limits`). A different root-scroll bug is new; see N-B1 |
| O-B6 a decision that cannot load is a skeleton forever | **Fixed** | Offline: "Cannot load q_8: this browser is offline…" with Retry. Back online, it loads by itself (`ob6-offline-open`) |
| O-B7 Website's Land always refused | **Fixed** (demo half) | j_25 merged in 3 taps and 2.4 s (`j5c-webkit-j_25-merged`). I did not trigger a refusal, so the refusal card is **not re-tested** |
| O-B8 / F-B4 demo refuses a long TMPDIR | **Fixed** | See above |
| O-B9 the palette keeps "give" in the brief | **Partly checked** | `@mobile add haptics…` became "Give job to Mobile: …" and landed on Mobile's lead (`j2-webkit-palette-at`). I did not re-test a leading "give" |
| O-B10 raw internals in toasts and snippets | **Partly** | Project creation is clean ("Project Work created ~/Work"). Raw text remains elsewhere: "VERDICT: approve" (N-P1); chat quoting the protocol (O-F18) |
| O-F1, O-F2 / F-F7 label first, one label, one id | **Partly** | The label leads every row, and repeats are gone (`j1-phone-chromium-light-s1`). The pane name is **still shown** in many places: board cards (`job-14` beside `j_14`), the tray card ("job-13 wants to fetch a web page"), the decision chips ("worker job-11"), the palette ("ap_5 · job-13") and the reply box ("Message job-6…") (`tray-phone-jobs`, `tray-phone-row-tapped-dark`, `j3c-chromium-question_q_1-s1`, `j10-palette-allow`, `j4-chromium-dark-s1`) |
| O-F3 / F-F2 a stuck agent: reply first | **Fixed** | The banner's action is **Reply**; the agent's words and the box sit right under it (`j4-chromium-dark-s1`). After a reply the job flips to Running (`j4-chromium-dark-reply-sent`). The headline still contradicts the message under it (F-new 9) |
| O-F4 a scary confirm on a plain question | **Fixed** | "Apply it" answered in one tap (`j3c-chromium-question_q_1-tapped`) |
| O-F15 "Ready to land" on an unchecked job | **Partly** | The card reads "Ready to land (not checked)". The Land card reads "Check: **Unverified**", and the Result block under it reads "**Verified**" (`j5c-webkit-j_25-merged`) |
| O-F17 raw tool names and temp paths in titles | **Partly** | Titles are fixed ("GitHub: create issue", "Edit: src/screens/InboxScreen.tsx"). Absolute paths remain in four places: Now's expanded approval card (4 lines of `/private/tmp/…`, `j3-chromium-danger-allow1`), the decision screen's diff header (head-truncated `/private/tmp/claude-501/-Use…`, `j3b-webkit-approval`), the answered-item view (`decide-answered-ap1`) and the project page header (3 lines, `j6-phone-project-crowsnest-s1`) |
| O-F18 protocol text in Chat | **Partly** | The injection is now one system line ("Job j_10 handed to this agent"). The orchestrator's own reply still opens with **"On You hold job j_10 (role lead). Its b…:"** in bold (`sweep-phone-deepsea-_chat`) |
| O-F8 / F-F8 Settings is hard to find | **Fixed** | Reached through More in 2 taps (1.7 s), as well as the rail and `g s` (`j8-chromium-more`) |
| O-F9 Voice is invisible when off | **Fixed** | The Voice section says how to turn talk mode on |
| O-F11 Settings shows "default" instead of the value | **Partly** | Switches say "Off (default)". Number fields still show **"0" with a "default" chip** while the effective limit is 32, stated two lines above (`j8-chromium-limits`). The Tailnet field shows both "default" and "Off (default)" |
| O-F10 palette verbs and ranking | **Partly** | Approvals carry their actions, Do not disturb is there, Settings sections are rows, and `@project` works. Still missing: no Nudge, Cancel or Snooze for a job. "nudge" ranks "Settings: Budgets…" first. "snooze" and "cancel j_22" offer **Give job first**, so Enter starts a paid job named "cancel j_22" (`j9.mjs` output) |
| F-B2 line-comment bar under the pager in a phone sheet | **Not re-tested on a phone** | On the desktop the bar ("Comment (c)") shows at y=851 and a second click opens the composer (`j5b-j_18-line-click2`) |
| F-B3 no "Sending…", heartbeat blind to a frozen server | **Fixed** (decision screen) | With the server SIGSTOPped: "Sending…" at 300 ms, "Still sending…: you can leave this page" by 6 s, and the header reads **STALLED**. The answer went through on resume (`fb3-frozen-6000ms`, `fb3-resumed`). The **New job form has none of this** (N-B4) |
| F demo: queue grows, not git, not trusted | **Fixed** | The queue stayed between 15 and 19 for about an hour. Project pages open without a trust warning (`j6-phone-project-crowsnest-s1`) |
| F "7 of 7 vs 0 of 10" counts | **Fixed** | "3 of 3 committed files not viewed" beside "0 of 3 viewed" (`j5-j_18-review`) |
| F-F1 diff below the fold | **Improved** | At 1440 the first code line is at y=655 (was ~850). I did not measure the phone |
| F-F3 Give a job 2.7 screens down | **Fixed, with a catch** | The header "+" opens New job from any view: 3 taps and 0.7 s to "Job j_26 given · Open j_26". But the project is hidden behind "Options · To the orchestrator" (F-new 3) |
| F-F4 the palette can't route; a miss is a job | **Partly** | See O-F10 |
| F-F5 two cards for one approval | **Not fixed** | Now's row and the tray card show "job-13 wants to fetch a web page", an open note box, ✓/✕ buttons and "Elevated". The decision screen shows "Allow WebFetch: …?", "Add a note (sent with a deny)" and "Approval (elevated)". The row says "Blocking" (`tray-phone-row-tapped-dark` against `j3b-webkit-after-doubletap-750ms`) |
| F-F6 answer bar, toasts, "3 of N", Skip | **Fixed** | The footer is about 215 px (was ~470). Toasts are at the top; "3 of 11" and Skip are there (`j3c-chromium-question_q_1-s1`) |
| F-F9 the merge confirm is off-screen | **Fixed** | "Yes, merge" at y=477. Three plain bullets. Discard is a quiet red text link |
| F-F14 / O-F20 worktrees offered; the keyboard covers the picker | **Partly** | Worktrees are gone from the picks. The picker's filter **is still focused on open on touch**, in both engines, with `pointer: coarse` true (`coarse.mjs` output, `j7-webkit-picker`) |
| F-F10 / O-F22 keyboard | **Partly** | There is a skip link, `j`/`k` work on the Inbox and Jobs, and `g` jumps focus `main`. Not done: `j`/`k` on Now, `g s` leaves focus on `<body>`, Esc does not close the Inbox's open item, and focus after answering on a Now row falls to `<body>` (`kbd.mjs` output) |
| O-F7 "Blocked" bell | **Fixed** | "Notifications off" (`sweep-desk-ember-_inbox`) |
| O-F12 a wait names no limit | **Not done** | 40 jobs queued in Mobile, with 17 free slots instance-wide, each card says only "Queued · queued just now". The real reason is in the API (`wait_owner: "its standing agent"`) and nowhere on screen (`load-phone-webkit-jobs-mid`) |
| O-F16 Since you left repeats Needs you | **Fixed**, a smaller repeat left | Open questions are gone. An answered question now shows as two rows, "Asked" and then "Answered" (`j1-phone-webkit-light-s2`) |
| O-F19 Agents and the queue disagree | **Fixed** | "3 waiting on you" (`sweep-phone-deepsea-_agents`) |

---

## 2. The ten journeys, timed against round 1 (phone unless noted)

| Journey | Round 1 | Now | Notes |
|---|---|---|---|
| 1. Land on Now cold | 1.5 s; projects at 1.3 screens; job box at 2.3 | Content in 130–180 ms; **projects at 0.6 screen** (y=518); Since you left at 1.5; job box at 2.0, but "+" gives a job from the header | Above the fold: the count sentence, a smaller hint, 3 rows and "Review all 16" |
| 2. Give a job, typed | Scroll + 3 taps, 2.2 s | **3 taps, 0.7 s** (+, type, Give job) | No project choice is visible in the sheet |
| 2. Give a job, palette | No project | 2 taps + typing, `@mobile …`, 2.3 s | Works on the phone too |
| 2. Give a job, voice | Not verified | Not verified | No key; Settings explains how to turn talk mode on |
| 3. Dangerous approval from Now | 3 taps, 1.5 s | 3 taps (row, Allow once…, Yes, allow it), ~1.6 s | The card still prints the full temp path |
| 3. Approval, decision screen | 2 + auto-advance into a trap | 2 taps (row, Allow once), 0.74 s to open; safe advance | "1 of 13" while the badge says 19 |
| 3. Question, decision screen | 2 + confirm | **2 taps**, no confirm | |
| 3. From the tray (phone bar) | — | 2 taps to an expanded card | A different card from the decision screen (F-F5) |
| 4. Stuck agent: find, why, act | 2 taps; why under 7 buttons | 2 taps, 1.8 s; **Reply** is the banner's action; its words sit right under it | Rescue folded as "Other ways to get it moving" |
| 5. Review, comment, send back (desktop) | ~8 actions | Click a line, click again, type, Save, Viewed, Send: 6 | The composer opens with Save below the fold at 1440 |
| 5. Land (phone) | 5 taps, 6.4 s | **3 taps, 2.4 s** (Review, Merge, Yes, merge) | Two toasts for one merge |
| 6. Five projects | Counts only | Counts only | Now answers "which is on fire" well; "what did each do today" is still a number |
| 7. New project with the picker | 4 taps, 3.7 s; home accepted | 5 taps, 4.7 s; home refused | A parent folder (`~/Work`) is accepted |
| 8. Settings: theme, limit, voice, tailnet | 2 taps via account; broke the header | 2 taps via More; Section select; header stays | Limits shows "0" for a limit of 32 |
| 9. "Accepted" from where it appears | Palette only on a phone | **1 tap** on the "?" beside the check, 1.0 s; the palette's first hit is right too | |
| 10. The palette for all of the above | Many gaps | Settings, @project, DND, approvals: yes. Nudge, Cancel, Snooze: no | |
| Dropped connection | Banner; back in 17 s | Decision screen excellent (see F-B3). Chromium offline: back to LIVE 20 s after reconnect | The New job form is the gap (N-B4) |

---

## 3. Broken (new or left over)

**N-B1. On a phone, the whole app frame scrolls away on Now, Jobs and Settings.** (390, both engines, every theme; **min**)
*Saw:* `rootscroll-_-after-swipe` (Now), `rootscroll-chromium-inbox` (Jobs, after scrolling: a fully blank screen).
- Swipe to the end of Now, then swipe once more. The document itself scrolls 382 px: the header
  leaves, the tab bar floats mid-screen, and a blank band fills the bottom.
- On Jobs the document scrolls **2,069 px** and the screen is entirely blank. Settings in WebKit:
  9,254 px.
- A real touch gesture does it: Chromium's touch scroll reproduced it. So does
  `window.scrollTo`.
- *Cause, measured:* `.sr-only` spans (`position: absolute`) inside `<main>`'s scroll container
  have no positioned ancestor. Their static positions run off the bottom of the document. The
  offenders are "Spend: (estimate)" on every job card, the ownership sentences on Now, and the
  job box's label.
- *Why it matters:* it is the first thing a thumb does on the two busiest phone views, and it
  looks like a crash.
- *Fix:* `position: relative` on the scroll container (`main`), or `overflow: clip` on
  `html, body`. Add an e2e that swipes past the end and asserts `scrollY == 0`.

**N-B2. A sent-back job is still "Ready to land" in the queue.** (all sizes; **hrs**) The rest of
O-B3.
*Saw:* `sweep-desk-ember-_inbox`, `load-desktop-chromium-contrast-_`.
- After I sent j_18 back, its card and header say Changes requested. The attention item (Now, the
  Inbox, the tray and the palette) still reads "Can wait · Ready to land", with detail "Ready to
  land: job/91-retry-with-back-off".
- *Fix:* build the attention item's label and detail from the same `landable` / `nextStep` logic
  on the server, or drop the job from the queue while a fix job runs. A Go test: send back, then
  attention has no "Ready to land".

**N-B3. "Running" means three things on one screen.** (Jobs, Projects, Settings → Limits; **min–hrs**) The rest of O-B4.
*Saw:* `counts.mjs` and `kbd.mjs` output; `load-phone-webkit-crowsnest-_jobs`;
`j6-phone-projects-phosphor-dark`.
- At one moment: the API had 12 running and 44 queued. The Jobs "All" chip said **59 running**,
  and Mobile's chip counted its 41 queued as running. The Jobs line said "15 of 32 running · 1
  waiting". Settings said the same. The Projects footer said "Unfiled: … 7 running" (3 running and
  4 queued).
- "1 waiting" beside 44 queued reads as a bug.
- *Fix:*
  - the chips: "N need you · M running · Q queued", the same function as Now's rows;
  - the limit line: name what it counts ("15 slots in use");
  - drop "1 waiting", or say what waits.

**N-B4. Giving a job offline says "Giving…" for ever, then creates it anyway.** (phone; **hrs**)
*Saw:* `net2-offline-give-8000ms`, `net-offline-give`.
- Offline, Give job showed "Giving…" for 8 s and more, with no offline notice. The decision screen,
  by contrast, says "this browser is offline" at once.
- On reconnect the job was created and the sheet closed by itself.
- In my first run I pressed Esc while it said "Giving…". The job (j_71) was created anyway. A
  person would give it again: a **duplicate paid job**.
- *Fix:* reuse the decision screen's pending states in the job form ("Not sent: offline",
  "Still sending…: it goes through when the server answers"). Send an idempotency key with the
  create, so a retry cannot make two.

**N-B5. A missing job is a skeleton for 5 to 10 s.** (phone; **min**)
*Saw:* `missing-webkit-10s`, `sweep-phone-deepsea-_jobs_job_j_999`.
- `/jobs?job=j_999` shows a skeleton at 1.5 s and 5 s. "There is no job j_999." arrives only
  between 5 and 10 s, after three 404s.
- A push for a job that was since removed lands here.
- *Fix:* treat 404 as final on the first response.

**N-B6. An answered item's page names the wrong project.** (desktop; **min**, once seen)
*Saw:* `decide-answered-ap1`.
- `/decide?item=approval:ap_1`, after it was denied, reads "approval · **Unfiled** · j_14". j_14
  is Website's job.
- The chip is the raw lowercase label "approval", and the path is the full temp path.
- Opening a stale push after answering on another device lands here.
- *Fix:* fill the project from the job when the item has left the queue, and use the shared label
  words.

**N-B7. The folder picker still raises the keyboard on a phone.** (phone, both engines; **min**)
The round-1 fix is claimed but not in effect: the filter box has focus as the picker opens, with
`pointer: coarse` true (`coarse.mjs`). Most likely the dialog's focus trap focuses the first input.
*Fix:* give the dialog an explicit initial focus (the heading or the list) on coarse pointers.

---

## 4. Friction

Ranked by how often it costs you.

**F-new 1. One approval, four vocabularies and two cards.** (all sizes; **hrs**; F-F5 still open)
*Saw:* `j1-desktop-chromium-light-s1`, `tray-phone-row-tapped-dark`, `j3b-webkit-approval`,
`j10-palette-allow`.
- One WebFetch approval is "Blocking" on Now, "Elevated" in the palette and on its card, and
  "Approval (elevated)" on the decision screen.
- The card's title is "job-13 wants to fetch a web page"; the decision screen asks "Allow WebFetch:
  …?". The card opens the note field; the decision screen hides it behind "Add a note".
- Pick one set: the label chip (Dangerous / Blocking / Approval / Can wait) everywhere, the risk as
  a second chip, and one title ("Allow WebFetch: …?").
- Merge `ApprovalCard` into the decision screen's body.

**F-new 2. Pane names are still everywhere.** (all; **hrs**)
- Board cards show both `job-14` and `j_14`.
- Also: "Message job-6…", "worker job-11", "ap_5 · job-13" in the palette, and "Waits on job-7"
  on job j_7 itself (`j6-phone-project-crowsnest-s2`).
- *Fix:* show the job id only. Keep the pane name for Agents and the Terminal tab.

**F-new 3. The phone's "+" hides the one choice you make every time.** (phone; **min**)
*Saw:* `j2-webkit-plus-filled`.
- The sheet has the brief, then "Options · To the orchestrator". Choosing Website takes a tap to
  open Options, then a select.
- Now's own job box shows "For" up front.
- *Fix:* a "For" chip row above the brief (the five projects and Orchestrator), remembering the
  last choice. Accept `@website` in the text, as the palette does.

**F-new 4. Jobs' filter chips are unlabelled numbers.** (390 and 1440; **min**)
- "All **17** 16" and "Crowsnest **8** 4": a red number and a teal number, with no word or legend
  (`tray-phone-jobs`). Even their accessible names are wrong (N-B3).
- *Fix:* "17 need you · 16 running" in the chip at 1440. On a phone, keep the red badge and drop
  the teal number.

**F-new 5. Queued jobs never say what they wait for.** (O-F12; **hrs**)
40 Mobile jobs say "Queued", with 17 slots free. The truth is "waiting for Mobile's lead to take
it" (`wait_owner`). Put it on the card ("Queued for Mobile's lead"). On Now's project row, say "41
queued for the lead (idle)": that is the actionable fact, because the lead is idle.

**F-new 6. Palette: one verb should never start a paid job.** (desktop; **hrs**)
- "snooze" and "cancel j_22": **Give job** is the first row.
- "deny": Send to orchestrator is second.
- "nudge": the first row is "Settings: Budgets, upkeep and notices".
- *Fix:*
  - job verbs on job rows: Nudge, Cancel, Reply, Snooze, Merge;
  - `<verb> j_N` parsed as a command;
  - Give and Send never first for one word.

**F-new 7. The line-comment composer opens below the fold.** (1440; **min**)
- Click a line near the bottom, then click again. The composer opens at y≈790, and its Save
  button is off-screen (`j5b-j_18-line-click2`).
- I could not find its button by name. Save by keyboard is not hinted.
- *Fix:* scroll the composer into view; label the button "Save comment"; hint "⌘↵ to save".

**F-new 8. The walkthrough doesn't look openable.** (desktop and phone; **min**)
- "Walkthrough" is a bare heading bar, a `<summary>` with no chevron (`j5-j_18-review`). Opened,
  it is excellent (`j5b-j_18-walkthrough-open`).
- *Fix:* add a chevron and a one-line preview ("3 files · Tested: both paths · Risk: …"). Link each
  file name to its place in the diff (Fable's Delight 6, half done).

**F-new 9. The stuck banner still contradicts the agent.** (phone; **min**)
- "Stopped three times without saying why: Ended its turn 4 times in a row…" sits above the
  agent's own "I'm not sure how to proceed without changing herdr's event shape"
  (`j4-chromium-dark-s1`).
- The timeline says "unresponsive after three nudges". That is three counts in one view.
- *Fix:* when the last message is prose, the headline is "Asked in its message"; use one count.

**F-new 10. Offered, then refused.** (phone; **min**)
- On j_18, while its fix job runs, Overview offers **Run the check again**. Pressing it gives
  "Not done: job j_18: fix job j_30 is making the changes…" (`toastz-chromium`).
- *Fix:* hide or disable it with that reason as the caption. Apply the same rule to every action:
  don't offer what the server will refuse.

**F-new 11. The check card still opens with two paragraphs of caveats.** (phone Overview; **min**)
The Land tab moved them behind "?", but Overview's "crowsnest's check" card still shows 9 lines of
caveat above the output (`toastz-webkit`). Move them behind the same "?".

**F-new 12. Settings shows 0 for a limit of 32.** (O-F11 rest; **min**) "Running jobs at once: 0
[default]" sits right under "Limit 32 at once". Show the field as "32 (automatic: a quarter of the
cores)" with the 0 explained in its help.

**F-new 13. Keyboard leftovers.** (desktop; **hrs**)
- Focus falls to `<body>` after answering in a Now row, and after `g s`.
- `j`/`k` don't move through Now's Needs you rows.
- Esc doesn't close the Inbox's open item (`kbd.mjs`).

**F-new 14. Toasts are big and come in pairs.** (phone; **min**)
- A "Job given" toast is about 110 px with "Open j_28" on its own line; two of them cover the top
  third of Now (`j2-webkit-palette-given`).
- A merge raises two toasts: "Merge: done — Merged into main (merge commit, …)" and "The worktree
  is gone…".
- The Landed card then says "**Merged**: Merged into main".
- *Fix:* one-line toasts with the action inline; one toast per action; a newer "given" replaces
  the last.

**F-new 15. Small contradictions inside one card.**
- "Waiting on your answers: 1 open question" beside a "2 questions" chip (j_15, `tray-phone-jobs`).
- "1 of 13" on the decision screen while the badge says 19 (`j3b-webkit-approval`).
- An Edit approval's risk reason is "Too long to show whole on a lock screen" (`j3b-webkit-approval`).
  That is a push-formatting fact, not a risk.

**F-new 16. Stopping a lead is one tap from the Projects list, behind a checkbox icon.** (phone; **min**)
"☐ Stop" on every row reads as an unticked checkbox (`j6-phone-projects-phosphor-dark`). Use a
stop-square glyph, and put the action in the project page or behind a confirm.

**F-new 17. "What did each project do today?" is still a number.** (phone; **hrs**)
Now gives "1 done today · $11.40". Neither Projects nor the project page has "today: merged j_25
(rounding), j_18 sent back, 2 asked". Since you left has the facts but not by project. Group it by
project, or add a "Today" line to each project row.

---

## 5. Polish

- **"VERDICT: approve" / "VERDICT: changes"** raw in the Review card and in Since you left
  (`j5c-webkit-j_25-merged`, `j1-phone-webkit-light-s2`). Strip it with the other protocol
  prefixes. (min)
- **Chat** quotes the protocol in the orchestrator's reply ("On **You hold job j_10 (role lead).
  Its b…**"). Strip it where replies are rendered, not only in system rows. (min)
- **"The agent is waiting on this answer"** on questions, **"… for this answer"** on approvals.
  (min)
- **Since you left** shows "Asked" and "Answered" as two rows for one question. Make it one row:
  "Asked → answered: Postgres". (min)
- **Machine popover**: unlabelled figures (46.3G/64.0G, 6.3, 346G, 5) next to icons
  (`j8-chromium-more`). Add words ("memory", "load", "disk free", "agents"). (min)
- **Theme previews clip their own buttons** ("Give / iob", "Chec…") at 390
  (`j8-chromium-settings-top`). (min)
- **Phosphor**: "Jobs running · 8 in / all" wraps mid-phrase on the project page
  (`j6-phone-project-crowsnest-s2`). (min)
- **"lead idle"** on Now, **"Lead idle"** on Projects. (min)
- **WebKit page error on navigation**: "/api/v1/presence due to access control checks". The
  presence beacon fails on unload. It is harmless but noisy. (min)
- **The Inbox's empty pane** says "Questions and dialogs open here", but approvals and jobs do too.
  (min)
- **Ready-to-land detail** is a branch name ("Ready to land: job/91-retry-with-back-off"). Say what
  landed or will land. (min)

## 6. Delight (only where it costs no density)

- **Remember the last project** in the "+" sheet, shown as the first chip. Most mornings it is one
  project. (min)
- **The Land beat.** The Landed card says "Merged: Merged into main (merge commit, 9a33cfb9f3)".
  Make it "Landed in main: 3 files, +29 −3, 2 h 10 min from brief", with the next ready job one
  tap below. Land is the payoff; it still ends flat. (hrs)
- **A "Today" line per project** (F-new 17) is also the best end-of-day moment. (hrs)
- **Queue zero.** Lane A says "Nothing needs you" now names what is still running; the demo never
  emptied, so I did not see it. (not verified)
- **Long-press the snooze clock** for 10 min / 1 h / tonight. It is still a bare clock with no
  hint. (hrs)
- **`y` / `n`** on the decision screen, beside the number keys. (min)

---

## 7. The ten changes I would make before the tag, in order

1. **N-B1**: `position: relative` on the scroll container. Two of the four main phone views can be
   scrolled to a blank screen. (min)
2. **N-B2 + N-B3 + O-F15**: one state everywhere.
   - the queue reads `landable`;
   - "running" never includes queued (Jobs chips, the Projects footer, the limit line);
   - one word for "not checked" (not Unverified beside Verified);
   - question counts agree. (hrs)
3. **N-B4**: the New job form gets the decision screen's pending states and an idempotency key.
   (hrs)
4. **F-new 1**: one approval card and one label vocabulary across Now, the tray, the decision
   screen and the palette. (hrs)
5. **F-new 2**: drop pane names (`job-N`) from every user-facing surface but Agents and Terminal.
   (hrs)
6. **F-new 3**: "For" up front in the "+" sheet, remembering the last project. (min)
7. **F-new 6**: palette verbs for jobs (Nudge, Cancel, Snooze, Reply), `<verb> j_N` parsing, and
   catch-alls never first for one word. (hrs)
8. **O-F17 / O-F18 leftovers + Polish**: no absolute temp paths (Now's card, the diff header, the
   project page, answered items), no "VERDICT:", no protocol quote in Chat. (hrs)
9. **N-B5, N-B6, N-B7, F-new 12**: 404 at once; the right project on answered items; no keyboard
   on picker open; the effective limit in Settings. (min each)
10. **F-new 5 + F-new 13**:
    - queued cards say who they wait for;
    - focus after answering on Now;
    - `j`/`k` on Now, and Esc closes the open item. (hrs)

## 8. How it feels right now

Much better, and the difference is felt in the thumb:
- Land is three taps.
- A question is two.
- A double tap can no longer approve something you never saw.
- A stuck agent puts its own words and a reply box first.
- A frozen server says "Stalled" and "still sending" instead of going quiet.
- Settings is in More.
- Every page I opened was quick, and 70 jobs scrolled cleanly in High contrast and Phosphor.

The round-1 fixes are real and mostly well made. What is left is the same family as before, now
easier to see because the big problems are gone: **the app says one fact several ways**.
- Ready to land and Changes requested at once.
- 59, 15 and 12 "running".
- Blocking, Elevated and Approval (elevated) for one approval.
- `job-13` beside `j_13`.

Then there is one new physical bug, the frame that scrolls away on Now and Jobs, which a phone user
will hit in the first minute and which is a one-line fix. Fix those and the offline job form, and
it stops feeling like an instrument with slightly mislabelled dials and starts feeling like one you
can read at a glance at 11 pm.

## Not verified

- Voice (no key), a real phone, Firefox, tablets, landscape.
- F-B2 on a phone (the comment bar in the job sheet).
- The Land refusal card.
- The malformed-transcription fix.
- Queue zero.
- Viewed auto-advance (F-F11).
- The phone's first-code-line position.
