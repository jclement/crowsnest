# R10 usability review, by using it (Fable)

Round 1 of 3. Reviewed the built binary at `dcc6fdd` (`v0.1.0-467-gdcc6fdd`) on 2026-11-28, in
demo mode, driven with Playwright. This file is the only thing written to the repository.

**How I looked.** Phone 390x844 with touch in Chromium and WebKit; desktop 1440x900 and
1920x1080 in Chromium; light and dark; the Crowsnest and Phosphor themes looked at, the other three
only cycled. Taps are counted from my scripts; seconds are automation timings, so treat them as
lower bounds. Screenshots are named in backticks and live in
`/private/tmp/claude-501/-Users-jsc-Work-crowsnest/26db2a91-a713-4f85-8985-70b19347bc42/scratchpad/shots/`
(the scripts are in `../scripts/`).

**Sizes:** minutes, hours, a day.

## Verdict

The decisions are good and the plumbing works: I gave jobs, answered every kind of item, sent line
comments back, and merged a job from a phone, and nothing errored. Long titles never overflowed,
stale links resolve to a clear message, and the contextual "?" answers "what does Accepted mean"
in one tap.

What costs you is density of the wrong kind. On the busiest screens the thing you came for is
below explanation, identifiers and disabled buttons. On a phone the first line of a diff is 1.3
screens down, a stuck agent's own words are 1.5 screens down, and Give a job is 2.7 screens down.
Four things are actually broken, and all four are small.

## Broken

| # | Where | What I saw | Why it matters | Fix | Size |
|---|---|---|---|---|---|
| B1 | Settings, every size and engine | Any section link (`/settings#tailnet`, or tapping "Tailnet" in the index) scrolls the **document** as well as the view: `window.scrollY` becomes 41 to 45 and the app header is at `top: -45`. The header (search, account) is cut off and a blank band shows under the tab bar. `j8-chromium-tailnet`, `j8-chromium-limits`, `hash-*-index-tailnet` | Settings is reached by these links from the palette, the manual and its own index, so this is the normal way in. On a phone the palette button is gone until you reload | Scroll the `main` container yourself (`main.scrollTo`) or give the app root `overflow: clip`; do not call `scrollIntoView` on a child of a clipped root. A Help page opened with an anchor did not shift the shell in my run, though I did not confirm that anchor exists on the page | minutes |
| B2 | Job detail, Review tab, phone | Tap a line number in the diff inside the job sheet: the line highlights and nothing else appears. The "Comment / Clear" bar is drawn at y=909 in an 844 px viewport, under the Previous / Viewed / Next bar. `j5d-chromium-embedded-select`, `j5b-chromium-p-line-selected`. In full screen the same bar is visible and works (`j5c-webkit-tap2`) | The headline feature of the last round, line comments, cannot be started from the place a phone lands on. Only the per-hunk "Ask for change here" works there | Position the selection bar above the file pager in the embedded viewer, as the full-screen route does | minutes |
| B3 | Decision screen, phone, slow or frozen server | I froze the server (SIGSTOP) and tapped an answer. For 35 s: both choices grey, no spinner, no "Sending", no banner, and the header heartbeat still reads "Live updates connected". When the server resumed the answer went through. `stall-end`. With the browser set offline the result was the same for 20 s (`misc-offline-answer`); the note I had typed was kept, which is right | On a train this is the common case. You cannot tell "sent", "sending" and "lost" apart, so you will tap again or leave | Show "Sending…" on the tapped button at once; after 5 s say "Still sending: the server has not answered" with Cancel; make the heartbeat go still when a request is that late, not only when the stream does. I did not find how long the stream's own time-box is; 35 s was not enough to trip it | hours |
| B4 | `crowsnest serve --demo` | With a long `TMPDIR` it exits: "the agent socket path … is longer than 103 bytes … set XDG_RUNTIME_DIR". Setting `XDG_RUNTIME_DIR` changes nothing, because under `--demo` the socket goes in `TMPDIR` (`cmd/crowsnest/questions.go:94`). macOS's own `TMPDIR` is long, so this is one deep folder away for anyone | The demo is the first thing a new user runs, and the error names a fix that does not work | In demo mode put the socket in a short directory (`/tmp/cn-<pid>`), or make the message name `TMPDIR` | minutes |

Smaller things that do not work as shown:

- **The demo never settles.** The queue went 16 → 29 in 25 minutes because the script re-asks the
  same questions: four copies of "Publish the site as soon as the hero is in?" sat in Needs you
  (`desk-palette-give-actions`, behind the palette). Leave the demo open over lunch and it looks
  broken. Cap each scripted question at one open copy. (minutes)
- **Counts disagree on one screen.** The Land card says "7 of 7 files not viewed" and the toolbar
  under it says "0 of 10 viewed" (`j5-chromium-p-changes`, `desk-j24-review`). One counts
  commits, one counts uncommitted work too. Use one number, or say "7 committed files". (minutes)
- **A job chip and its detail disagree.** j_25 is "Ready to land" on the board and "Done ·
  Unverified" with "No check command for this project" inside, beside a "Run the check again"
  button that has nothing to run. Hide the button when there is no command. (minutes)
- **The demo's first project page opens on a warning.** Crowsnest "has not been told to trust this
  folder", and its Changes card says the checkout "is not in a git repository" (R8 H13, still
  there). `j7-chromium-project`. Pre-trust the demo folders and make them repositories. (hours)
- **Agent protocol text leaks into the decision screen.** "The agent's last message" shows
  `SAY: M1 to M3 are merged…` and `[crowsnest job j_10] You hold job j_10 (role lead). Its b…` in
  bold (`stall-end`). Strip the `SAY:` prefix and crowsnest's own injected headers before
  rendering. (minutes; possibly demo data only, I did not check a real transcript)
- **A malformed transcription leaves "Transcribing…" forever** and throws
  `Cannot read properties of undefined (reading 'text')`. I caused it with a mock, so a real
  ElevenLabs error may be handled; a bad body is not. (minutes)

## Friction

Ranked by how much it will cost you each day.

**F1. The diff is below the fold, on both sizes. (hours)**
Job j_24, Review tab. Phone: the first line of code is at y=1098 of 844 (`j5-chromium-p-changes`).
Desktop 1440x900: it is at about y=850 (`desk-j24-review`). Above it: title, four chips, the owner
chain, a three-line absolute path, "Waits on job-24: its next turn (an undeclared stop is
refused)", the tabs, a Land card with three disabled buttons and three sentences explaining why
each is disabled, then a toolbar of 15 controls in four ragged rows.
Fix: while a job is running, the Land card is one line ("Lands when it finishes"). Fold the
toolbar to Files, the scope switch and a "…" menu on a phone. Make the header collapse on scroll.

**F2. A stuck agent: the reason is one tap away, the fix is not on the screen. (hours)**
Inbox → "Profile the fleet cache" → job sheet (`j3-chromium-item6`). The banner says "Stopped
three times without saying why" and offers **Nudge**. The timeline below says it was nudged three
times already. Then eight rescue buttons of equal weight. What the agent actually said ("I'm not
sure how to proceed without changing herdr's event shape") and the message box are 1.5 screens
down (`p-j6-s1`). The agent did say why, and the answer is a sentence from you.
Fix: put the agent's last words and the message box directly under the banner. Recommend one
action per cause and never the one that already failed three times. Fold the other seven under
"More ways" (Codex said this in R8; it is still true).

**F3. Give a job is 2.7 screens down on the phone. (hours)**
Now is 2012 px in a 750 px view; the job box is last (`p-now-s2`). Two swipes, tap, type, tap. The
palette is faster (tap search, type, tap the row) but nothing tells a new user that the magnifier
gives jobs, and its hint reads "⌘Enter for the full form" on a touch screen
(`j2-chromium-palette-give`).
Fix: a "+" in the phone header or a fifth-tab "New job" that opens the same sheet the Jobs board
uses. Hide keyboard hints on touch devices.

**F4. The palette cannot choose a project, and a search miss is a job. (hours)**
Typed "Fix the flaky login test on Website": the row is "Give job … To the orchestrator", and its
actions are only "Open the full form" and "Give job and open it" (`desk-palette-give-actions`).
With five projects, routing is the one decision you make every time.
Typed "tailnet", "backup", "nudge": the first row is "Give job: tailnet". Enter starts a real,
paid job named after a failed search.
Fix: `@website fix the login test` (or the project list as the Give row's actions), remembering
the last project. Do not offer Give job as the top hit for one or two words; put Settings
sections in the index so "tailnet" and "backup" find their section.

**F5. Two different cards for the same approval. (hours)**
From Now, a row expands in place: a red card, "Allow…", then an alert "This is dangerous. Allow it
anyway?" (`probe-webkit-1`). From the Inbox the same item opens the decision screen: a teal
"Dangerous approval" chip, a neutral "Allow once" beside an identical "Deny", then an inline
confirm (`j3-chromium-item0`). Different words, different colours, different confirm. The
decision screen's back arrow always says "Back to Now", including when you came from the Inbox.
Fix: one component. Take the red treatment from the Now card and the "If you choose" block from
the decision screen. Back goes where you came from.

**F6. The answer bar takes half the phone, and the toast covers it. (hours)**
On a question the fixed bar (note box, two full-width buttons, "Open the full job") is about
480 of 750 px, so "What the agent wrote" gets four lines and its diagram is cut
(`j3-chromium-item2`). After an answer the app advances to the next item, which is good, and the
"Allowed" toast then sits on top of the next item's Allow and Deny for a few seconds
(`j3b-webkit-approve-after`). The next item after a routine approval was the force push, with its
Allow under your thumb in the same place.
Fix: collapse the note to "Add a note"; put two choices side by side; drop the duplicate job
link. Show toasts at the top on the decision screen. After an auto-advance, hold the buttons for
half a second. Add "3 of 29" and a Skip, so you can pass an item without snoozing it.

**F7. The danger word is the part that gets truncated. (minutes)**
Phone rows on Now and in the tray read "Crowsnest · Review the rate-limiter fix on fix…": the
label ("Dangerous", "Blocking") comes after the project and job title and is cut off on the two
most urgent rows (`p-cr-now`, `j3b-webkit-tray-open`). On the desktop the label is said twice:
"Dangerous · Dangerous approval", "Approval · Approval" (`d-cr-now`).
Fix: label first, then project, then job; drop the kind when it repeats the label.

**F8. Settings is hard to find and very long on a phone. (hours)**
It is in neither the desktop rail nor the phone's More sheet (More has System, not Settings); it
is only in the account menu, and has no `g` shortcut (`j8-chromium-more`,
`j8-chromium-account`). On a phone it is about 14,000 px, 18 screens; the theme gallery alone is
3,000 px, so Notifications starts at 3,700 (`j8-chromium-settings-top`). The index at the top is
15 loose links in ragged rows, and with B1 each one breaks the header.
Fix: add Settings to More and to the rail's foot, and `g s`. On a phone show the index as a list
and open one section per screen. Make the theme previews a two-across grid of small swatches.

**F9. Landing: the confirm is off-screen, and Discard is as loud as Merge. (minutes)**
Phone, Land tab (`j5e-chromium-land-tab`): two paragraphs of acceptance caveats come before the
buttons (the same two are on Overview). Tap Merge and the confirmation opens below as a wall of
larger text with two absolute paths; "Yes, merge" is under the fold
(`j5e-chromium-merge-tapped`). Discard is a solid red button beside Merge.
Fix: caveats behind the "?" that already exists. Confirm in a bottom sheet with the button in
reach: "Merge job/91… into main? Removes the worktree." Discard as a quiet text button.

**F10. No list keys on the desktop. (hours)**
`j`/`k` do nothing on Jobs or the Inbox; `y`/`n` and Esc do nothing on the decision screen (they
work only after `g n` into the tray). Tab from a fresh Now passes eleven chrome controls before
the first item. Focus stays on `<body>` after every `g` jump.
Fix: `j`/`k`/Enter on Jobs, Inbox and Needs you; `y`/`n`/`1-9` on the decision screen, with the
existing second step for danger; Esc goes back. Focus the first row after navigation.

**F11. Viewed, then Next, is two taps a file. (minutes)**
Ten files, twenty taps (`j5c-webkit-tap2`). Make ticking Viewed advance to the next unviewed
file. On a phone turn Wrap on by default: long lines
are cut at the edge and need a sideways scroll.

**F12. Toolbar icons that look like accept and reject. (minutes)**
The phone diff toolbar has a bare "✓" and "✕" side by side: Mark all viewed and Clear marks
(`j5b-chromium-p-line-selected`). One tap on ✓ marks every file viewed, which is the thing that
gates landing. Give them words or move them into the Files sheet.

**F13. Jobs board: the toolbar is a third of the phone. (minutes)**
New job, spend, running, to land, Schedule, Journal and help take 290 px in three right-ragged
rows before the first job (`p-cr-jobs`); on the desktop's 448 px column they wrap the same way
(`d-cr-jobs`). Keep New job and the counts on one line; move Schedule and Journal to a menu.

**F14. New project offers worktrees as project folders. (minutes)**
"Remembered directories" lists `job-120-checkout-redesign`, `job-121-price-rounding` and two more
before `crowsnest` and `mobile` (`j7-chromium-new`). Filter out anything under a worktree root.
On a phone the form focuses the path field first, which raises the keyboard; make Browse the
primary control there. The picker itself is good (`j7-chromium-picker`).

## Polish

- **Banners with a button squeeze their text on a phone.** The first-run hint is five lines
  beside its buttons (`p-cr-now`), seven in Phosphor (`j8-chromium-now-phosphor`); the stuck,
  check-failed and trust banners do the same (`j3-chromium-item5`, `j7-chromium-project`, where
  the trust banner is 470 px tall). Stack the action under the text below 640 px. (minutes)
- **Absolute paths everywhere.** Three lines of mono on every job header, in "If you choose", in
  approval titles ("Edit: /tmp/…/demo-src/mobile/src/screens/InboxScreen.tsx"), and in the merge
  confirm. Project lists truncate the tail and keep the useless head ("/tmp/claude-501/r10/
  crowsnest-demo-2921922139/stat…", `d-cr-projects`). Show paths relative to the project, `~` for
  home, and truncate in the middle. (hours)
- **One id, four times.** "Job j_24", chip `j_24`, chip `job-24`, "builder j_24"
  (`j5-chromium-p-job`). Once in the header is enough.
- **One tab, three names.** The same tab is "Changes" on j_6, "Review · 2 comments to send" on
  j_24 and "Land" on j_18. On a phone the long one pushes Conversation and Terminal off the strip
  with no sign the strip scrolls ("Termir", `j3-chromium-item6`). Call it Review; show the
  comment count as a dot.
- **"If you choose" often says nothing.** "Friday: The agent is told you chose this. Next Monday:
  The agent is told you chose this." "Yes: Presses 1 in the agent's terminal." Show the block
  only when a choice has a consequence to state. The recommendation is said three times on one
  screen (`j3-chromium-item2`).
- **A tick on "Not yet".** Both choice buttons carry the same check icon. Drop it.
- **Dialog items are titled by the job, not the question.** "Dialog: Rebuild and publish the
  handbook site" and "job-5 is stopped at a terminal dialog" (`j3-chromium-item1`). The question
  is "Run `rm -rf dist && npm run build`?".
- **"Since you left" on a first visit** says "since 12 h ago · 14", and lists questions that are
  still open in Needs you above it, all with the same icon and the word "Asked" (`p-now-s1`).
  Leave out what is still in the queue; hide the section until there has been a visit to leave.
- **Small copy.** "1 open question(s)", "1 jobs", "Last sent 11/28/2026, 5:47:09 AM" beside
  relative times everywhere else, "Waits on job-24: its next turn (an undeclared stop is
  refused)".
- **The snooze control is a bare clock** on every row; nothing says it snoozes or for how long
  until you read its label. A first-timer will not tap it. Long-press or swipe for 10 min / 1 h /
  tonight would make it both findable and useful.
- **Project rows on the desktop repeat one sentence** ("Owns every job in its project: told when
  one finishes…") five times, truncated (`d-cr-now`). It belongs in the "?".
- **A Codex caveat is printed on job cards** ("Codex runs without answer receipts, usage-limit
  detection…") on the board. A chip with the sentence in its tooltip is enough.
- **Theme toasts stack** three deep when you press T repeatedly. Replace, do not queue.
- **404** says "Back to jobs" (home is Now) and has no header or tab bar (`misc-err-nope`). A
  "No such job" error offers Retry, which cannot help; offer "Back to Jobs".
- **The phone header** shows the machine as "q.." beside a "demo" chip and a CPU percentage
  (`p-cr-now`). The name is the one thing that tells quark from bedev; give it the CPU chip's
  room.
- **The job form's options open below its own submit button** and the toggle is labelled "To the
  orchestrator", which reads as a destination, not "More options" (`j2-chromium-options`).
- **The "Give job" toast has no link to the job** it just made (`j2-chromium-after`).

## Delight

Each is small and costs no density.

1. **Land is the best moment in the product and it ends in a grey toast** on a scrolled-away page
   (`j5e-chromium-merged`). Give it one beat: the chip flips to Merged, a one-line summary stays
   at the top ("Merged into main · 3 files · $3.25 · 2 h 10 min from brief to main"), and the
   next thing to land is one tap away. (hours)
2. **Queue zero.** When Needs you empties, say so once, with the lookout mark and what is still
   running. It is the state you work towards all day. (minutes)
3. **Haptics and a tick** on answer, Viewed and Land (`navigator.vibrate` where it exists; the
   heartbeat can beat once on every answer delivered). (minutes)
4. **Swipe on queue rows**: right to open, left to snooze. Rows already have the height. (hours)
5. **"Answer all routine"**: the count line already knows "2 approvals"; one tap to review them
   as a stack with Allow all. (hours)
6. **The walkthrough before the diff.** j_18 has a per-file walkthrough and a reviewer verdict,
   shown after the diff (`j5e-chromium-merged`). Put them first and link each file name to its
   place in the diff: read the guide, then the code. (minutes)
7. **Recording feedback.** Hold-to-talk shows "Listening… release to finish" and a good hint
   ("Say the project first") but no timer and no level (`misc-voice-holding`). A level bar proves
   the microphone hears you before you speak for a minute. (minutes)
8. **The footer one-liners** ("Man the pumps, the token budget is leaking") are the product's
   voice, and the 404 has it too. Use it in the empty states and on the first-run hint, which is
   the flattest sentence on the page. (minutes)
9. **`.` to repeat the last command in the palette**, and recent commands when it opens empty,
   above the queue. (minutes)
10. **A project's row pulses once when its job finishes** while you are looking at Now; the
    heartbeat already has the vocabulary. (minutes)

## The journeys, measured

| Journey | Phone result |
|---|---|
| 1. First five minutes | The count sentence and Needs you are right and above the fold. Projects start at the fold; Give a job is out of sight. Nothing on the first screen says what crowsnest is; the hint sends you to a manual |
| 2. Give a job | Typed: 2 swipes + 2 taps, toast in about 1.5 s. Palette: 2 taps, no project choice. Voice: hold works and records; transcription not tested against the real service |
| 3. Answer | Question: 2 taps (3 for a gate-worded one). Routine approval: 2. Dangerous: 3. Auto-advance works. Two card designs (F5) |
| 4. Stuck agent | Found in 1 tap from the queue. Why: visible. What the agent said and how to reply: 1.5 screens down (F2) |
| 5. Review and land | Comments, preview, send, Viewed and merge all work. First code line 1.3 screens down (F1); line comments dead in the embedded view (B2); merge is 4 taps and a scroll, 3.6 s |
| 6. Five projects | Now's project rows answer "which is on fire" well: waiting counts, running, failed, spend. "What did each do today" has no answer beyond counts |
| 7. New project | Form opens in 1 tap; the picker is clear and warns about the home folder. Worktrees offered as projects (F14) |
| 8. Settings | 2 taps to reach if you know it is under the account icon. Theme: easy. Limits, voice, tailnet: found, each breaks the header (B1) |
| 9. "Accepted" | The "?" beside the check opens the right manual section over the job in 1 tap. The palette finds it too. This is done well |
| 10. Palette | Finds jobs, diffs, projects, themes, modes, manual sections. Misses Settings sections; cannot route a job; a miss offers a job (F4) |

## Before the tag, in order

1. B1: Settings links must not scroll the app shell. (minutes)
2. B2: the line-comment bar in the embedded diff on a phone. (minutes)
3. F1: the diff above the fold: one-line Land card while running, folded toolbar. (hours)
4. F2: a stuck job shows the agent's words and a reply box first, one recommended action. (hours)
5. B3: "Sending…", a late-answer message, and a heartbeat that notices. (hours)
6. F7 and F5: danger label first on every row; one approval card. (hours)
7. F3 and F4: a New job button on the phone; a project in the palette; no Give job on a miss. (hours)
8. F6: a smaller answer bar, toasts out of the way, "3 of 29" and Skip. (hours)
9. The demo: B4, one copy of each question, trusted git folders. It is the first impression. (hours)
10. F8 and F9: Settings in More and the rail; the merge confirm in reach, Discard quiet. (hours)

## How it feels right now

It feels like a serious instrument built by someone who has thought hard about what an agent
should be allowed to do, and has then written all of that thinking onto the screen. Every card is
correct and most are careful; the trouble is that the care is in front of the work. I was never
lost and never surprised by a wrong result, and the moments that matter (answer, send back, merge)
each worked first time on a phone. But I scrolled past caveats to reach buttons, past buttons to
reach the diff, and past eight rescue options to reach the one sentence the agent had said. The
phone is close: the queue, the auto-advance and the full-screen diff already feel fast. Move the
explanations behind the "?" that is already beside them, fix the four broken things, and it stops
feeling like auditing and starts feeling like steering.

## Not checked

- No real phone, no real herdr, Claude or ElevenLabs: demo mode with voice calls intercepted.
- Chat, Agents, System, the terminal, Schedule, Journal and Spend were opened and their text read,
  not walked as journeys. Firefox, tablets and landscape were not run.
- Deep sea, Ember and High contrast were cycled with `T` (it works, with a toast) but I did not
  examine their screenshots; Phosphor only on the phone in light mode.
- "40 items in a list": the demo reached 29 queue items and 27 jobs; nothing larger was tried.
- The stream's reconnect time-box (B3): I measured that 35 s was not enough, not what the limit is.
- I started the demo with `TMPDIR=/tmp/claude-501/r10` (a short path outside my scratchpad,
  because of B4) and stopped it when done. The repository tree is otherwise untouched.
