# R10 (Opus): usability and awesomeness, by using it

Round 1 of 3 before `v0.2.0`. Reviewed `main` at `dcc6fdd` (binary `v0.1.0-467-gdcc6fdd`) on
2026-11-28 by **using** a demo instance (`serve --no-auth --demo`, port 28120) through Playwright:
Chromium and WebKit at 390x844 with touch, and Chromium and WebKit at 1440x900, in light, dark,
Phosphor and High contrast. 141 screenshots are in
`/private/tmp/claude-501/-Users-jsc-Work-crowsnest/35b60c8c-be76-4206-a45b-54a21fca4624/scratchpad/shots/`.
Below, a shot is named by its file name in that folder (without `.png`). Scripts are in `../scripts/`.

Read-only. Apart from this file, I created ~30 throwaway jobs and one project inside the demo's own
state, and stopped and restarted my own demo server once to test a dropped connection. The server
is stopped now.

**Two deviations from the brief.**
- **TMPDIR.** A TMPDIR inside the scratchpad makes the demo refuse to start: the agent socket path is
  over 103 bytes. Its hint, "set XDG_RUNTIME_DIR", **does not help on macOS**: I set it and the error
  came back unchanged. So the demo ran with `TMPDIR=/tmp/cnr10ux`.
- **Voice.** I unset `ELEVENLABS_API_KEY` for the demo. With the key in the environment, the demo
  starts with talk mode **on** and would spend real ElevenLabs credit; headless browsers have no
  microphone anyway. Voice is therefore **not verified** here, beyond what the app shows when voice
  is off.

Sizes: **min** (minutes), **hrs** (hours), **day**.

## Timings (phone unless noted)

| Journey | Taps / keys | Time | Notes |
|---|---|---|---|
| 1. Land on Now cold | 0 | 1.5 s to content (skeletons at 1.2 s on a 3 s API) | Above the fold: the count sentence, the "New here?" tip (5 lines), and 3 queue rows. Projects start at 1.3 screens; the job box is at **2.3 screens** |
| 2. Give a job by typing (Now) | scroll + pick For + type + 1 | ~2.2 s to the "Job j_27 given" toast | The toast has no link to the job, and it covers the Give job button |
| 2. Give a job from the palette (desktop) | ⌘K, type, ↵ | instant | Can't pick a project; "give …" stays in the brief (B9) |
| 3. Dangerous approval from Now | 3 (row, Allow…, Yes, allow it) | ~1.5 s | The row expands in place; good |
| 3. Question from the Inbox | 2, then **a confirm** (F4) | ~1 s | Then it auto-advances (B1) |
| 3. Approval from the Inbox | 2 | <1 s | Auto-advances into the next item (B1) |
| 4. Stuck agent: find → why → act | 2 (Inbox row → job) | ~1 s | The real "why" sits under 7 rescue buttons (F3) |
| 5. Review a 3-file change, comment, send back (desktop) | ~8 | 3.4 s scripted | Clicking a line doesn't open a comment; `c` does (F14) |
| 5. Land (phone) | 5 (job, Land, Mark all viewed, Merge, Yes) | 6.4 s to "Merge: done" | Works. In the demo, the other ready job **cannot** land (B8) |
| 7. Create a project with the picker | 4 | 3.7 s | Accepted the home folder it had flagged red (B2) |
| 8. Reach Settings on a phone | 2, through the account icon | 2 s | Not under More (F8) |
| 9. "Accepted": what does it mean? | ⌘K + "what does accepted mean" + ↵ | instant | Lands on the right page. On a phone the chip has only a `title` tooltip, which never shows |
| Dropped server (killed under the page) | — | Banner at once; back 17 s after restart | Good: banner, retry, the typed job kept, an honest error |

---

## Broken

**B1. The decision screen jumps to the next item under your thumb.** (phone, all themes; hrs)
*Saw:* `j3f-chromium-doubletap`, `j3e-chromium-approval-allow-menu`.
- After **Allow once**, `/decide` swaps in the next queue item at once: same layout, same button
  positions, no "next item" notice. The only sign is the toast naming the item just answered,
  which no longer matches the screen.
- In my double tap (250 ms apart), the second tap landed on the next item: a terminal dialog
  whose red **"Yes, and don't ask again for this command"** sits about 50 px above where Allow
  once was.
- This time, the toast caught the tap, because it covers the bottom of the next item's buttons
  for several seconds.
- One approval followed by another approval would put **Allow once** exactly under the second tap.

*Why it matters:* a reflex double tap, or a tap one beat late, approves something you never read.
It is the worst outcome the product can have from a phone.

*Fix:*
- After an answer, show a one-line interstitial ("Answered. Next: <title>"), or keep the next
  item's action buttons inert for about 700 ms after it appears.
- Never put the next item's buttons under the previous tap point.
- Show toasts above the sticky footer, never over it.
- Announce the change to screen readers.

**B2. The folder picker warns "too wide for a project", then lets you do it.** (phone, WebKit; hrs)
*Saw:* `j7b-picker-pasted`, `j7b-created`.
- The picker says in red: "This is your home folder: too wide for a project (it would claim every
  job)."
- **Select this folder** stayed enabled, and **Create project** made project `demo-home` on the
  home folder, with its lead started.
- A pasted path outside the browse roots also leaves the selection on `~`: pressing Enter then
  selects home.

*Fix:* disable Select while a refusal is showing, and refuse it on the server too. When a pasted
path is refused, leave the selection empty rather than on home.

**B3. Sending a done job back leaves three places disagreeing.** (desktop; hrs)
*Saw:* `j5c-sent-5s`, `j5d-j18-after-sendback`.
- After **Send 1 comment to the agent** on accepted job j_18:
  - the card reads **Changes requested** (right);
  - the detail header still shows **Done · Accepted**;
  - the board's **"2 to land"** still counts j_18.
- The tab renames itself from "Land" to "Review · changes requested".

*Fix:* drive the header chips and the "to land" count from the same `nextStep`; keep the tab name
stable.

**B4. Now counts queued jobs as running.** (desktop and phone; min–hrs)
*Saw:* `load-now-desk-phosphor`, `load-jobs-phone-s3`.
- With 32 Website jobs, Now's Website row says **"32 running"**.
- The API has 19 running and 13 queued.
- The board says "31 of 32 running · 14 waiting", and the cards say "Waiting for a slot".

*Fix:* count `queued` separately ("19 running · 13 queued").

**B5. Jumping to a Settings section scrolls the whole page.** (1440, all themes; min)
*Saw:* `j8-tailnet`.
- `/settings#tailnet` (and every section link) scrolls the document root rather than `<main>`.
- The app header (name, search, Live, account) scrolls out of view, and a black band about 40 px
  tall appears under the rail and the content.

*Fix:* scroll the `main` element, or clamp the root's overflow.

**B6. A decision that fails to load stays a skeleton for good.** (phone; hrs)
*Saw:* `net-offline-open-item`.
- With the browser offline, opening a question from the Inbox showed a skeleton for 21 s (the
  whole test) and the header still read **Live**: Chromium's emulated offline left the event
  stream connected.
- No error and no retry. A real drop (`drop-12s`) is handled well, so this is the half-broken case
  only: one request failing while the stream lives.

*Fix:* show an error state with Retry after the first failed fetch.

**B7. The demo's Website Land always fails, and the refusal only lives in a toast.** (desktop; hrs)
*Saw:* `j5d-merged`.
- Merging j_25 is refused: "the main checkout … has uncommitted changes (README.md); commit or
  stash them there first."
- In the demo this means one of the two ready jobs can never land.
- The reason is a toast that disappears. The Land card then looks as if nothing happened, and it
  offers no way to act: no terminal on the main checkout, no git status.

*Fix:*
- Persist the refusal in the Land card with "Open a terminal in the main checkout" and "Show what
  is uncommitted".
- Fix the demo fixture so both Lands work.

**B8. The demo's own start error gives the wrong hint on macOS** (see deviations; min). Name
`TMPDIR` in the message, or fall back to a short socket directory by itself.

**B9. The palette keeps "give" in the brief.** (desktop; min)
*Saw:* `j2-desk-palette-give`.
- Typing `give fix the typo in the README` offers "Give job: **give** fix the typo…".
- Either strip a leading verb, or don't invite one.
- Nor can the palette aim a job at a project. Something like `@website fix …` would be the
  natural way.

**B10. Raw internals in user-facing text.**
*Saw:* `j7b-created`, `j10` output.
- Creating a project showed a Go error in a toast: "taking the lock …/.claude.json.lock: mkdir …:
  no such file or directory". This is demo-only, but it is the first-run path.
- The palette's manual snippet for "keyboard" shows `<!-- Generated from web/src/lib/shortcuts.`.
- *Fix:* min each.

---

## Friction

**F1. On a phone, the queue truncates away the word that matters.** (390, every theme; hrs)
*Saw:* `sweep-phone-chromium-light-now`, `j3c-chromium-tray-open-vp`.
- The meta line is project · job title · label · kind · pane · age, and the label comes third. On
  a phone it is cut: the force push row loses **Dangerous** entirely, and others read "Bl…",
  "Blocki…", "Ca…".
- *Fix:* lead with the label as a chip ("DANGEROUS", "Blocking", "Routine"), then the project.
  Drop the rest on a phone; the decision screen has it.

**F2. Labels and ids say everything twice.** (all sizes; hrs)
*Saw:* `now-desk-light-s1`, `j4c-rescue-default`.
- Rows read "Dangerous · Dangerous approval", "Blocking · Blocking question" and "Approval ·
  Approval".
- Every job carries two ids: the pane `job-13` and the job `j_13`. The detail shows the id three
  times (`Job j_6`, chip `j_6`, `tester j_6`) plus `job-6`.
- *Fix:* one label per row, and one id (`j_13`). Show the pane name only in Agents and the Terminal
  tab.

**F3. "Stopped three times without saying why", above a message that says exactly why.** (phone, dark; hrs)
*Saw:* `j4c-rescue-default`.
- Job j_6's last message is a real question: "I'm not sure how to proceed without changing
  herdr's event shape".
- The screen leads with "Stopped … without saying why" and a **Nudge** button. Under them sit seven
  rescue buttons (Nudge again among them), and only then the message and the reply box.
- It already has "3 nudges". The useful action, answering it, is the last thing on the screen.
- *Fix:* when the last assistant message ends in a question, say "Asked in its message" and put the
  reply box first, with the rescue list under "Other ways to get it moving".

**F4. A plain question gets a scary confirm.** (phone; hrs)
*Saw:* `j3e-chromium-answered-2s`.
- Answering "Apply the fleet cache refactor?" with the recommended **Apply it** asks for a second
  tap: "This widens what agents may do. Read the evidence above, then confirm".
- Applying a tested refactor widens nothing. False alarms teach the second tap to be automatic,
  which costs the confirm its value on the day it matters.
- *Fix:* only confirm when the option grants a permission (the agent marks it, or it matches
  approval-like wording *and* the job's gate). Word the confirm as what will happen, not as a
  warning about agents. Also, "Yes, Apply it" should read "Yes, apply it".

**F5. On a phone, the decision screen gives more than half the height to its footer.** (390; hrs)
*Saw:* `j3d-chromium-question-s1`, `j3e-chromium-approval-s1`.
- The sticky footer (note field, two 48 px buttons, "Open the full job") takes about 470 of
  750 px. The evidence scrolls in the rest.
- "Recommended" appears three times. "Not yet" carries a ✓. The low-risk callout uses the star
  that also means "recommended".
- *Fix:* collapse the note to an "Add a note" link that expands, and show Recommended once. Give
  "Not yet" no tick and the risk callout its own shield icon.

**F6. Toasts cover the buttons you need next.** (phone; hrs)
*Saw:* `j2-chromium-given-400ms`, `j3f-chromium-doubletap`, `j3c-chromium-after-danger-allow-400ms`.
- The toast sits over the job box's Give job button, over the next decision's options, and over
  project rows.
- "Job j_27 given" has no link to the job.
- *Fix:* place toasts above sticky actions, give "given" and "merged" toasts an **Open** action,
  and move focus sensibly. After an answer on Now, focus fell to `<body>`.

**F7. "Blocked" in the Inbox means notifications, not jobs.** (phone; min)
*Saw:* `j3d-chromium-inbox-s1`.
- Next to "16 need you: 6 blocking", a crossed bell says **Blocked**. It means this browser blocks
  notifications.
- *Fix:* "Notifications off" plus a link to the how-to.

**F8. Settings isn't under More on a phone.** (390; min)
*Saw:* `j8-more-sheet`, `j8-account-menu`. More holds Jobs, Agents, Help and System. Settings is
only behind the account icon. Add it to More.

**F9. Voice can't be found when it is off.** (all; min–hrs)
- With no ElevenLabs key there is no Voice section, no mic, and nothing that says voice exists.
- `/settings#voice` silently lands on Limits.
- *Fix:* always show a Voice section. When off: "Talk mode is off: start crowsnest with
  `ELEVENLABS_API_KEY` …", with a link to the how-to.

**F10. The palette finds things but can't act on most of them.** (desktop; a day)
*Saw:* `j10-palette-empty`, `j10b-actions-*`.
- **Actions per item:** an approval offers only "Answer in Inbox" (no Allow once or Deny). A
  stuck job offers only Nudge (no Rescue…, Cancel or Reply). A ready job offers "Review and land",
  not Merge.
- **Missing commands:** no toggle for Do not disturb (only help pages), and no Settings target for
  Tailnet or Voice.
- **Ranking:** for "approve", "nudge", "deny", "snooze" and "cancel job", the catch-all **Give
  job: …** and **Send to orchestrator: …** rows rank above real matches.
- *Fix:* add item actions (Allow once, Deny, Snooze, Nudge, Reply, Cancel, Merge when ready) and a
  DND switch. Add Tailnet and Voice targets, and rank the catch-alls last.

**F11. Settings shows "default" instead of the value.** (all; hrs)
*Saw:* `j8-tailnet`, `j8-settings-phone-s1`.
- "Publish to your tailnet: default" doesn't say whether it is on. Several fields are the same.
- The phone page is 19 screens with the index at the top only.
- *Fix:* show the effective value ("Off (default)"), and add a sticky "Sections" jump on phones.

**F12. Waits name a limit without saying which one.** (desktop; min)
*Saw:* `j5-desk-job`.
- j_22 says "Waiting for a slot: 4 of 4 running" while the header says "14 of 32 running".
- *Fix:* name the limit ("Unfiled's limit: 4 of 4").

**F13. The Land tab leads with caveats, not evidence.** (phone; hrs)
*Saw:* `land2-phone-land-s1`.
- The tab opens with two paragraphs on what Accepted does not prove, then Merge and Discard.
- The reviewer's **Approved** verdict and the walkthrough are on Overview (`land2-after`), not
  beside the buttons.
- *Fix:* lead with verdict, walkthrough and files; put the caveat behind the "?".
- On a phone, Discard (solid red) sits right under Merge. Move it away, or make it a quieter
  style.

**F14. Commenting: a line tap selects but doesn't open a comment.** (desktop; hrs)
*Saw:* `j5b-line-click`, `j5b-comments-panel`.
- Clicking a line number highlights it. A comment opens only with `c` (the selection bar the
  manual describes was not in view), or with one of the **two** "Ask for change here" links per
  hunk.
- The Comments dialog repeats the Comments panel under the diff, so there are two Send buttons.
- *Fix:* open the composer on a second tap of the selected line (or show a floating "Comment"
  next to it). Keep one Send.

**F15. "Ready to land" on an unchecked job.** (desktop; min)
*Saw:* `j5d-merged`. j_25's detail says **Unverified**, while its card says **Ready to land**.
Say "Ready to land (not checked)".

**F16. "Since you left" repeats Needs you.** (phone; min)
*Saw:* `now-phone-wk-dark-s3`. Its first five entries are "Asked" questions still open in Needs
you, two screens above. Leave out items still in the queue.

**F17. Raw tool names and absolute paths as headlines.** (all; hrs)
*Saw:* `load-jobs-phone-s3` (tray bar), `sweep-phone-chromium-light-inbox`.
- The phone's tray bar headline was **`mcp__github__create_issue`**.
- Edit approvals are titled `Edit: /tmp/cnr10ux/crowsnest-demo-…/state/demo-src/mobile/src/…`;
  R8 H13 is still open.
- *Fix:* humanise them ("GitHub: create issue"; a path relative to the job's folder). This is the
  first impression in the demo and in every push.

**F18. Chat shows crowsnest's protocol text as your message.** (phone; hrs)
*Saw:* `sweep-phone-chromium-light-chat`.
- The "[crowsnest job j_10] You hold job j_10 (role lead). Its brief is in /tmp/…" injection is
  drawn as a user bubble.
- The orchestrator's summary then quotes it: "On **[crowsnest job j_10] You hold job j_10…**: here
  is where things stand."
- *Fix:* render injections as a system line ("Job j_10 handed to the orchestrator").

**F19. Agents and the queue disagree on dialogs.** (phone; hrs to find out)
*Saw:* `sweep-phone-chromium-light-agents`. Agents says "4 at a dialog" (job-13's force push among
them); the queue had one terminal dialog and the rest as approvals. Pick one vocabulary, or say
"waiting on an approval" in Agents.

**F20. New project suggests job worktrees.** (phone; hrs)
*Saw:* `j7-new-project`, `j7-picker`.
- The quick picks under Directory are `job-120-checkout-redesign`, `job-121-…` and so on: the
  demo's `.worktrees`.
- The picker also autofocuses its filter, so a phone keyboard covers half the list.
- *Fix:* never suggest worktrees or existing projects as new projects; don't autofocus on touch.

**F21. Phone job detail: hidden tabs, wandering names.** (390; min)
*Saw:* `j4c-rescue-default`. "Termin…" is cut off with no scroll hint. The second tab is
"Changes", "Land" or "Review · changes requested" depending on state. Fit four tabs (icons and
short words), and keep one name.

**F22. Keyboard.**
- No skip link: ten tab stops before Now's content.
- Focus is lost after answering an item.
- The `?` sheet, `g` shortcuts and `j` in the queue all work well.

---

## Polish

- "1 open question(s)", "Show 1 more lines", "Show all 1 hidden lines", "waiting 6 min ago"
  (should be "waiting 6 min"), "Yes, Apply it". (min)
- The Comments dialog's file name is flush against the dialog's left edge, outside its padding
  (`j5b-comments-panel`). (min)
- The merge confirm is a six-line paragraph of absolute paths (`j5d-merged`). Make it three
  bullets: merges into main; removes the worktree and branch; refused if the checkout is dirty.
  (min)
- The "New here?" tip says "every project is beside it"; on a phone the projects are below. The
  tip also costs five lines above the fold until dismissed. (min)
- While offline, the header's machine stats stay frozen at the last value and look live
  (`drop-12s`). Dim them. (min)
- Manual drift: `how-to/review-and-land.md` and `review-changes.md` say "Review tab", but the tab
  reads **Land** (or "Review · …"). They also promise "a bar at the bottom" for a selection, which
  I could not see at 1440x900. (min)
- The Phosphor and High contrast themes held up at 1440 with 40 jobs; long briefs wrap to four
  lines with an ellipsis, which is right (`load-jobs-desk-phosphor`, `load-inbox-desk-hc`,
  `load-jobs-phone-s3`).

## Delight (only where it costs no density)

- **The empty moment.** When the last blocking item is answered, put one line where the queue
  was: "Nothing needs you. 3 agents moving again." It is the payoff of the whole product, and
  today it passes silently. (hrs)
- **A rule from a routine approval.** On a low-risk approval, add a third button: "Always allow
  `npm run test:links` in Website", with Undo in the toast. I did not find a way to make a rule
  from the decision screen. (hrs)
- **Undo in toasts** for Deny, Snooze, Discard and Mark all seen, for 5 s. (hrs)
- **Number keys on the decision screen**: 1, 2, 3 for the options and ↵ for the recommended one;
  the dialog card already shows the numbers. (hrs)
- **The tab title carries the count**: "(3) crowsnest", blocking only, so a pinned tab tells you
  without looking. (min)
- **A landed line in Since you left** with the diffstat and a link to the merge commit. (hrs)

---

## The ten changes I would make before the tag, in order

1. **B1**: guard the auto-advance on the decision screen, and move toasts off the action
   footers.
2. **B2**: enforce the picker's own refusal; never create a project on the home folder.
3. **B3, B4, F15**: one source of truth for state. The header, "to land" and Now's counts all
   from `nextStep`; queued is not running.
4. **F1, F2**: on a phone, the queue row leads with its label; one label and one id per row.
5. **F3**: a stuck agent that asked in prose gets "Reply" first, not seven buttons.
6. **F4**: drop the "widens what agents may do" confirm from questions that grant nothing.
7. **B7, B6**: a refused Land says why in the card and offers the way out; the demo's Land works.
   A decision that fails to load says so and offers Retry.
8. **F17, B10, F18**: no raw tool names, temp paths, Go errors or protocol text in headlines,
   toasts or chat. This is the demo's first impression and every push's text.
9. **F8, F9, F11, B5**: Settings in More; a Voice section that explains how to turn it on;
   effective values, not "default"; section links that don't scroll the header away.
10. **F10**: palette verbs for approvals and jobs (Allow once, Deny, Nudge, Reply, Merge), a DND
    switch, and the catch-all Give and Send rows ranked last.

## How it feels right now

It feels like a serious instrument made by someone who has been bitten. The dropped-connection
path is better than most commercial apps: banner, countdown, the typed job kept, an honest
"not created". Land is five calm taps. The palette finds the right manual page from a plain
question, and the themes are genuinely good. The decision screen and the rescue panel are both
thoughtful. What lets it down is the last centimetre:

- states that disagree depending on where you look (Accepted here, Changes requested there; 32
  running that are mostly queued);
- the most important word on a phone row cut off the end;
- an approval screen that slides the next decision under your thumb;
- system internals (`mcp__github__create_issue`, `/tmp/…/state/…`, protocol preambles, a Go lock
  error) where a person's words should be.

None of these are design problems; they are consistency and copy problems, most of them hours each.
Fix the ten above and the phone experience goes from "impressive, careful, slightly
nerve-wracking" to one you would trust at 11 pm with one thumb.
