# R11 usability review, by using it (Fable)

Round 2 of 3. Reviewed the built binary at `135dac1` (`v0.1.0-508-g135dac1`) on 2026-11-28, in
demo mode, driven with Playwright. This file is the only thing written to the repository.

**How I looked.** Phone 390x844 with touch in Chromium and WebKit; desktop 1440x900 and 1920x1080
in Chromium; light and dark; Crowsnest, Phosphor, Deep sea and Ember looked at on a phone in
WebKit. Taps are counted from my scripts; times are automation timings, so lower bounds.
Screenshots are named in backticks and live in
`/private/tmp/claude-501/-Users-jsc-Work-crowsnest/bea4235f-5411-4320-9162-5ffbb438b06a/scratchpad/shots/`
(157 PNGs; the scripts are in `../scripts/`).

**Sizes:** minutes, hours, a day.

## Verdict

Round 1's fixes are real. Of the 31 items in the table below, 16 are fixed, 10 are fixed in part
or with something left over, 1 is fixed badly, and 4 I did not re-test. Nothing regressed. The phone is now quick where
it was slow: the first line of a diff moved from y=1098 to y=562, a stuck agent's words are on the
first screen, Land is four taps with no scroll, and a reflex double tap answers one item, not two.

What is left is smaller and mostly new, because the big things used to hide it. Three things are
wrong (two counts and leaked protocol text). The largest friction is that the new "+" on the phone
cannot send a job to a project.

## 1. Round 1, checked by using it

F = R10 Fable, O = R10 Opus. "Before the tag" numbers are each review's own list.

| Item | Verdict | Evidence |
|---|---|---|
| F-B1 / O-B5: a Settings section link scrolls the app shell | **Fixed** | `#tailnet`, `#limits`, `#voice`, `#notifications` on a phone: `scrollY` 0, header top 0, the section's heading at y=126 to 138. `j8-chromium-phone-tailnet` |
| F-B2: line-comment bar hidden in the job sheet on a phone | **Fixed** | WebKit: one tap on line 21 shows "Comment / Clear" at y=542; a second tap opens the composer. `j5b-webkit-line-selected`, `j5b-webkit-composer-typed` |
| F-B3: no "Sending", silent on a frozen server | **Fixed** | Server frozen with SIGSTOP: "Sending…" at 300 ms; at 6 s "Still sending “Friday”: the server has not answered yet" and the heartbeat reads STALLED; went through on resume. `stall-chromium-6000` |
| F-B4 / O-B8: demo will not start with a long `TMPDIR` | **Fixed** | Started with a 106-byte `TMPDIR` inside my scratchpad; the socket went to `/tmp/crowsnest-501/` |
| O-B1: auto-advance puts the next item under a double tap | **Fixed** | WebKit, two taps 250 ms apart on Allow once: only `ap_1` left the queue. "Allowed … Next up:" shows above the next item. `j3a-webkit-after-2nd-tap` |
| O-B2: picker warns "too wide", then lets you | **Fixed (client)** | At `~`, Select this folder is disabled. `j7-chromium-picker`. I did not try to create a project, so the server refusal is not re-tested |
| O-B3: a sent-back done job disagrees in three places | **Not re-tested** | I sent comments on a running job (j_24): the tab stayed "Review". The done-job case was not walked |
| O-B4: Now counts queued as running | **Fixed on Now; alive on Jobs** | Now: "4 running · 39 queued". The Jobs filter chips say "51 running" for the same fleet (see B1 below) |
| O-B6: a decision that fails to load is a skeleton | **Fixed** | Offline: "Cannot load q_3: this browser is offline…" with Retry within 1.5 s; loaded by itself when back. `offline-chromium-open-1.5s`. It names the id, not the question |
| O-B7: refused Land lives in a toast; demo Land always fails | **Not re-tested** | I landed j_18 (worked); I did not land j_25 or force a refusal |
| O-B9: palette keeps "give" in the brief | **Fixed** | "give tidy the changelog" offers "Give job: tidy the changelog" |
| O-B10: Go error in a toast; `<!-- Generated` in a manual snippet | **Not re-tested / not seen** | No project created; no HTML comment showed in any palette result I read |
| Fable: the demo never settles | **Fixed** | 35 minutes: the queue moved between 13 and 17, and at the end no title appeared twice |
| Fable: "7 of 7 not viewed" beside "0 of 10 viewed" | **Fixed** | "3 of 3 committed files not viewed". It is now said twice inside the merge confirm (Polish) |
| Fable / O-F15: chip and detail disagree | **Fixed** | j_25 reads "Ready to land (not checked)" |
| Fable: demo projects untrusted, not repositories | **Fixed** | The Website page opens with no warning. `j6-chromium-website` |
| Fable / O-F18: protocol text in the agent's last message and Chat | **Fixed badly** | See B3 below. `stall-chromium-6000`, `phone-chat` |
| Fable: malformed transcription hangs | **Not re-tested** | Voice was off in my demo (no key, on purpose) |
| F-F1 (tag 3): the diff below the fold | **Fixed on a phone; partly on a desktop** | Phone: first code line y=562 (was 1098). Desktop: y=546 (was about 850), with the toolbar still in four rows. `j5-chromium-phone-j_24-review`, `j5-chromium-desk-j_24-review` |
| F-F2 / O-F3 (tag 4, 5): the stuck job | **Fixed, one leftover** | Banner, Reply, the agent's words and the message box are all above y=450; rescue is folded. The banner still says "without saying why" above the agent saying why. `j4-chromium-stuck` |
| F-F7 / O-F1 / O-F2 (tag 6, 4): danger word truncated, labels twice | **Fixed on rows; ids partly** | "Dangerous · Crowsnest · …" leads every row. Board cards still carry both `job-16` and `j_16` (see F9 below) |
| F-F5 (tag 6): two approval cards | **Partly** | Words and colour match. The Now card is still its own layout (smaller left-aligned buttons, an always-open note box, "job-16 wants to edit a file"). `j3d-chromium-now-row-open` |
| F-F3 (tag 7): Give a job is far down | **Fixed, with a hole** | "+" in the phone header opens the form from anywhere in one tap. Its form has no project picker (F1 below) |
| F-F4 (tag 7): palette cannot route; a miss is a job | **Fixed, one leftover** | `@website fix the footer link` gives "Give job to Website: … To Website's lead"; "tailnet" leads with "Settings: Tailnet". Three or more words still lead with Give job even when real matches exist (F6 below) |
| F-F6 / O-F5 / O-F6 (tag 8): the answer bar and toasts | **Fixed for two or three choices** | Footer 107 px for two choices and 179 px for three (was about 480); toast at the top; "1 of 10" and Skip. A four-option multi-select still takes 600 of 750 px (F3 below) |
| F-F8 / O-F8 (tag 10, 9): Settings hard to find | **Fixed** | More → Settings (2 taps), the rail, `g s`; a sticky Section picker. The page is still 11,845 px (15.8 screens) |
| F-F9 / O-F13 (tag 10): Land confirm off-screen | **Fixed** | "Yes, merge" at y=477; three bullets; Discard is quiet. `j5c-chromium-j18-confirm` |
| O-F4 (tag 6): a plain question gets a scary confirm | **Fixed** | "Apply it" answered in one tap. `j3c-webkit-q-apply-tapped` |
| O-F17 (tag 8): raw tool names and temp paths in headlines | **Fixed in titles, not in bodies** | "GitHub: create issue", "Edit: src/screens/InboxScreen.tsx". Inside the approval the diff header is "/private/tmp/claude-501/-Use…" (F8 below) |
| O-F9, O-F11 (tag 9): Voice invisible when off; "default" for values | **Fixed** | `settings-voice-off`; "Off (default)" on the tailnet switch |
| O-F10 (tag 10): palette verbs, Do not disturb, ranking | **Partly** | The Do not disturb commands exist but rank 6th and 7th under five manual pages. "nudge" and "merge" find no verb rows. I did not open an approval's own actions |

## 2. Broken

| # | Where | What I saw | Why it matters | Fix | Size |
|---|---|---|---|---|---|
| B1 | Jobs, filter chips, every size | "All 14 · 51": the second number's label is "51 running". The API has 11 running and 39 queued; the line under it says "14 of 32 running". Unfiled reads "43 running". `load-desk-jobs`, `wide-jobs-j24` | It is O-B4 again, one view over. Three different "running" numbers on one screen | Count `running` only; show queued as its own number, as Now does | minutes |
| B2 | Settings → Limits, desktop | Saved "Running jobs at once" as 5: the field says "set here", the toast says saved, the API says 5, and the card above still reads "14 of 32 running … Limit 32 at once". After Reset it read "14 of 5" until a reload. `desk-settings-limit-saved` | The one place that states the effective limit is wrong right after you change it | Refetch the throttle query on save and on reset | minutes |
| B3 | Decision screen and Chat, phone | (a) "The agent's last message": "On **You hold job j_10 (role lead). Its b…**: here is where things stand." (b) Chat shows your answer as a bubble reading `[crowsnest answer q_8] "Friday"; the question was "Ship v0.2 on Friday?"`, and the orchestrator replies "On **"Friday"; the question was "Ship v…**: here is where things stand." `stall-chromium-6000`, `phone-chat` | The bracket is stripped in one place and not the other, and what remains reads as a broken sentence. Chat is a tab on the phone's bar: a new user sees it in the first minute | Give `[crowsnest answer …]` a `systemLine` case ("You answered: Friday"). Change the demo orchestrator's script so it does not quote its own prompt (if a real orchestrator does this, I did not check) | minutes to hours |

## 3. Friction

Ranked by what it costs each day.

**F1. The phone's "+" cannot choose a project. (minutes to hours)**
"+" → New job: a text box, then "Options · To the orchestrator". Options holds Where (a
directory path), Role, Done when and Engine; there is no "For" list (`j2-webkit-plus-options`).
Now's own job box has it. So from the fastest door on a phone, every job goes to the orchestrator
unless you type a path. With five projects, routing is the one choice you make every time.
Fix: put Now's "For" picker at the top of the sheet and remember the last choice.

**F2. A "Ready to land" row opens on Overview, and Overview leads with caveats. (minutes)**
Queue row → job sheet on Overview: a "Review and land" button, then crowsnest's check with two
grey paragraphs ("Accepted means…", "It ran in the job's working tree…"). What the agent did
(Result) starts at y≈770 (`j5c-chromium-j18-open`). Round 1 moved these caveats behind the "?" on
the Land card; Overview still prints them, and the "?" is right beside them.
Fix: open "Ready to land" rows on Review. On Overview put Result first and the caveats behind the
existing "?".

**F3. A multi-select question gives 600 of 750 px to its footer. (hours)**
"Which findings should the builder fix before merge?": the four options are listed in "If you
choose" and again as four checkboxes in the fixed footer; "What the agent wrote" gets no room at
all (`j3-chromium-q-findings`). Two of the four "If you choose" lines still say "The agent is told
you chose this."
Fix: for a multi-select, make the "If you choose" list the checkboxes and leave only Submit (with
the count chosen) in the footer. Drop the filler line when an option has no description.

**F4. Snooze is silent and has no undo. (minutes)**
One tap on a row's clock hides a blocking approval for ten minutes. No toast; the only sign is
grey "1 snoozed." at the end of the count line (`now-snoozed`). The clock is a 44 px target at the
right edge of every row, where a thumb scrolls. I did not test whether "1 snoozed" is tappable; it
does not look it.
Fix: a toast "Snoozed 10 min · Undo", and make "1 snoozed" a link that lists them.

**F5. A failed job is a dead end. (hours)**
"Port the TUI to Bubble Tea v2" sits in Needs you as "Can wait · Failed job". Its sheet has the
reason, the brief and a timeline, and no action at all (`j4-chromium-failed`). You cannot retry
it, give it again with a changed brief, or clear it from the queue from there.
Fix: "Give it again" (the form, pre-filled with the brief) and "Dismiss".

**F6. The palette still ranks its catch-alls and its manual above what you meant. (hours)**
- "npm run test" (to find an approval): row 1 is "Give job: npm run test". Enter starts a paid job.
  Real matches are rows 3 and 4. Three words is past the search threshold.
- "do not disturb": five manual pages, then the two commands.
- "nudge" leads with "Settings: Budgets, upkeep and notices"; "merge" offers no Merge.
Fix: commands first, then live items, then the manual. Lead with Give job only when nothing else
matches well, or when the text starts with `>`, `give` or `@project`.

**F7. The folder picker needs two taps per level and focuses its filter on a phone. (minutes)**
Tap "Developer": the row highlights and the footer says "~/Developer, not a git repo", but the
list does not move; a second tap or the chevron goes in (`j7-chromium-picker-in`). Nothing says
so. In Chromium with touch the filter box takes focus on open (`j7-chromium-picker`), which would
raise the keyboard; ux1b reports this fixed, so check it on a real phone.
Fix: one tap goes in; the folder you are in is what Select chooses.

**F8. Absolute paths survive inside the cards. (minutes)**
Titles are relative now, but the diff header inside an Edit approval reads
"/private/tmp/claude-501/-Use…": the head is kept and the file name is cut
(`j3b-webkit-danger-after`, `j3d-chromium-now-row-open`). "The call" prints "in /private/tmp/…"
over four lines (`j3-chromium-danger`), and the project page's header is three lines of path
(`j6-chromium-website`). A real `~/.herdr/worktrees/<repo>/<slug>/…` path will do the same.
Fix: the same relative-to-the-job path the title uses; `~` for home; cut in the middle.

**F9. Pane names are still on the surface. (minutes)**
Board cards carry `job-16` and `j_16` side by side (`j3e-chromium-jobs-with-tray`); the decision
screen's headline for a dialog is "job-5 is stopped at a terminal dialog" although the queue row
says "Run rm -rf dist && npm run build?" (`j3-chromium-dialog`); "worker job-11" is a chip on
every question; "Message job-6…"; the Now card says "job-16 wants to edit a file"; one demo job is
"job-demo".
Fix: one id (`j_16`) outside Agents and Terminal. Use the queue's title as the decision headline.

**F10. Line numbers are 40x20 px targets on a phone. (minutes)**
The comment flow works, but the thing you tap to start it is 20 px tall
(`j5b-webkit-line-selected`).
Fix: make the whole gutter cell the target at the row's full height, or let a tap on the code
select the line.

**F11. The desktop diff toolbar is still four rows. (hours)**
Scopes; "2 to send · 0 of 10 viewed"; Hide viewed, Hide whitespace, Wrap, expand, collapse, Mark
all viewed, Clear marks; then "vs where it left main" and four icons
(`j5-chromium-desk-j_24-review`). The phone got the fold; 1440 did not.
Fix: one row (scopes, counts, View options) at every width.

**F12. "Since you left" reports your own answers back to you. (minutes)**
After I answered three questions, the first five entries were three "Answered" and two "Asked"
for the same questions (`now-since`). The one fact that matters is cut off: "Answered · Crowsnest
· 3 min ago · held: the agent could n…".
Fix: leave out what you did in this browser; fold Asked into Answered; put an undelivered answer's
reason first, or in Needs you.

**F13. A usage-limited job says three different things. (minutes)**
Banner: "resets at 14:00" and "Retry in 15 s (attempt 2)". Board chip: "Retry in 30 min". Rescue
is open with eight buttons, two disabled (`j4-chromium-limit`).
Fix: one line ("Held until 14:00; crowsnest retries then") and Retry now; fold the rest as on the
stuck job.

**F14. The project page does not say what the project did today. (hours)**
Order: path, Lead (eight buttons), Changes, Jobs, Schedules, Approval rules, Acceptance,
Instructions; 3,366 px on a phone (`j6-chromium-website`). Now's row has the counts; nothing has
the story ("landed 1, 2 running, 1 waiting on you: …").
Fix: a "Today" line under the name, built from the same events as Since you left, filtered to the
project. Fold the lead's six lesser buttons.

**F15. Keyboard leftovers. (minutes each)**
- `g n` is "focus the tray", not Now (`g h` is), and does nothing on Now or the Inbox.
- `g j` leaves focus on `<body>`; the other jumps move it into the view.
- Number keys answer a decision, but the desktop buttons do not show their numbers
  (`desk-decide-q3`).
- After answering with a key and auto-advancing, Esc went back to the item just answered. I did
  not look at what that screen then showed.

**F16. "1 of 10" beside a badge of 16. (minutes)**
The decision screen counts only what it can show; jobs are left out. Say "1 of 10 decisions".

## 4. Polish

- **"Next up:" ends in a colon with nothing after it**, and its banner pushes the page down 76 px
  (`j3b-webkit-danger-after`). Say "Next: 5 blocking left".
- **The phone file header has an empty row** holding one comment icon, about 100 px
  (`j5-chromium-phone-j_24-review`). Put it beside the copy icon.
- **"Walkthrough" looks like an empty heading**: no chevron, no count (`j5c-chromium-j18-confirm`).
- **Stutters.** "Merged: Merged into main"; "The check failed: crowsnest did not accept it: The
  check still fails after 3 rounds"; "Review round 0:" with nothing after it; "3 of 3 committed
  files not viewed" twice in the merge confirm.
- **"Too long to show whole on a lock screen"** is printed as the reason an Edit is Elevated
  (`j3d-chromium-now-row-open`). It is a fact about notifications, not about risk.
- **Wrap breaks words anywhere** ("StatusNotImpleme / nted") and the continuation loses its indent
  (`j5b-webkit-line-selected`).
- **Ligatures in the diff**: `!=` is drawn as `≠`. In a review you are checking characters; I
  would turn ligatures off in code. Your call.
- **Mono titles wrap mid-word** in the Inbox ("…/time/ra / te", `j2-webkit-plus-given`).
- **The palette's Needs you rows use a third vocabulary** ("Elevated", "Check failed" chips, a
  dialog icon for approvals) and cut titles early to fit the chip (`j10-webkit-palette-empty`).
- **A toast on every Settings visit** when notifications are denied: "…then reopen this sheet".
  There is no sheet now, and it is not news the second time.
- **"T cycles the theme from anywhere"** is shown on a phone (`j8-chromium-phone-settings-top`).
- **The merged toast covers the Merged chip** (`j5c-chromium-j18-merged-1500`).
- **Desktop decision screen**: 500 px of nothing between a short question and its answers
  (`desk-decide-q3`). Put the choices under the question when the evidence is short.
- **The first-run hint is 185 px** for two lines of text, because its buttons take their own row
  (`j1-chromium-phone-light-now`).
- **"Open in the Claude app"** is a boxed icon, the loudest control on each project row, with no
  label.
- **The stuck banner's "?"** drops to its own line (`j4-chromium-stuck`).
- **After a reply to a stuck agent** the banner vanishes and nothing says it was sent; the proof is
  a timeline row four cards down (`j4-chromium-reply-sent`).
- **Chat's phone header is two rows** (agent picker, then "Open in Claude" alone), 180 px.
- **Demo data**: j_24's timeline is fifteen "Edited web/src/cart.ts (change N)" lines above the
  events that matter (`wide-jobs-j24`).
- **WebKit only**: the Role and Engine selects in the job form draw 22 px tall
  (`j2-webkit-plus-options`). This is the desktop WebKit engine at a phone size; check an iPhone.
- **Inbox dialog rows** repeat the job title on their third line.

## 5. Delight

Small, and none costs density.

1. **Say "demo" on a phone.** The demo chip is hidden below 400 px, so on the phone nothing tells a
   new user this fleet is made up. One clause in the first-run hint: "This is a demo: five made-up
   projects; nothing here touches your machine." It is the first thing a new user runs. (minutes)
2. **The Land beat** (R10 D1, still open). Replace the toast with a line that stays on the card:
   "Merged into main · 3 files +29 −3 · $2.98 · Next to land: Round prices to the cent →". (hours)
3. **"+" remembers the last project**, and the palette reads a trailing "… on Website" as
   `@website`. (minutes)
4. **Key caps on desktop choices**: a small `1`, `2`, `3` on each button and `↵` on the recommended
   one. The keys already work; nobody will find them. (minutes)
5. **A reply that lands**: where the stuck banner was, "Sent · the agent is running again" for a
   few seconds. (minutes)
6. **Viewed, to the end**: when the last file is ticked, say "All 10 viewed" and scroll to the
   Land card. (minutes)
7. **Tap the heartbeat on a phone** for "Live · last event 2 s ago". Its meaning is in a `title`
   today, which touch never shows. (minutes)
8. **Snooze with a choice**: long-press the clock for 1 h or tonight. (hours)

I could not see queue zero (ux1a built it): the demo's queue never emptied.

## 6. The journeys, measured (phone unless noted)

| Journey | Round 1 | Now |
|---|---|---|
| 1. Land on Now cold | Count sentence and Needs you above the fold; Give a job 2.7 screens down | Content in 100 to 175 ms. Count sentence, hint, three rows and "Review all" above the fold; Projects start at y=539. Give a job: "+" in the header, 1 tap. Nothing says "demo" (Delight 1) |
| 2. Give a job | Typed: 2 swipes + 2 taps, 1.5 s. Palette: 2 taps, no project | "+": tap, type, tap; 2.3 s scripted; the toast has "Open j_26". No project there (F1). Palette: `@website …` works. Voice: not run (no key) |
| 3. Answer | Question 2 taps (3 for a gate-worded one); approval 2; dangerous 3 | Question 2 (the gate-worded one too); approval 2; dangerous 3, answered in 0.8 s. Inbox → decision 123 ms. Double tap is safe |
| 4. Stuck agent | Reason 1 tap away; the agent's words 1.5 screens down | 1 tap; reason, the agent's words, Reply and the box all on the first screen. Reply, type, Send: 3 taps, 2.2 s |
| 5. Review and land | First code line y=1098; line comments dead in the sheet; Viewed then Next, 20 taps for 10 files; merge 4 taps and a scroll | First code line y=562. Line comment: 2 taps to the composer. Viewed advances: 10 taps for 10 files. Send-back 1.6 s. Land: row, Review, Merge, Yes: 4 taps, no scroll |
| 6. Five projects | Rows answer "which is on fire"; no "what did each do" | Same, with running and queued now apart. Still no "today" per project (F14) |
| 7. New project | Worktrees offered as projects | Worktrees gone; home refused. Two taps per folder level (F7). Not created |
| 8. Settings | 2 taps if you knew the account icon; every link broke the header | More → Settings, 2 taps; Section picker to Tailnet, 2 more; palette "tailnet", 3 in all. Theme change 0.7 s. A limit saves with Enter; its status card goes stale (B2). The tailnet switch was found and not touched |
| 9. "Accepted" | "?" beside the check, 1 tap | Same, still good |
| 10. Palette | No routing; a miss offers a job | Routing, Settings sections, themes and modes work. Ranking is the leftover (F6) |

Also tried: 37 extra jobs with a 137-character unbroken title, a 220-character title and a
CJK-and-emoji title: nothing overflowed at 390 or 1440 (`load-phone-jobs-long`,
`load-phone-long-detail`). The 404 is in the frame and says "Back to Now".

## 7. Before the tag, in order

1. **F1**: the "+" form gets the project picker and remembers it. (minutes to hours)
2. **B1 and B2**: running is not queued on the Jobs chips; the Limits card refetches. (minutes)
3. **B3 and Delight 1**: no protocol text in Chat or the decision screen; the demo says it is a
   demo on a phone. (hours)
4. **F2**: "Ready to land" opens on Review; Overview shows Result before caveats. (minutes)
5. **F4**: snooze says so and can be undone. (minutes)
6. **F3**: one list for a multi-select, not two. (hours)
7. **F6**: palette order: commands, live items, manual; Give job first only when asked. (hours)
8. **F8 and F9**: relative paths inside cards; one id; the question as the dialog's headline.
   (minutes each)
9. **F5**: a failed job offers "Give it again" and "Dismiss". (hours)
10. **F7 and Delight 2**: one tap per folder; Land ends on a line that stays. (hours)

## 8. How it feels right now

It feels like steering now, most of the time. The decision screen is the best screen in the
product: one question, the evidence, two buttons where the thumb is, and a calm "Next up" that
will not let a stray tap through. Review on a phone went from possible to pleasant: tick Viewed
and the next file arrives, tap a line twice and you are typing, and Land is four taps that say
exactly what they will do. What still breaks the feeling is small and specific. A count that
contradicts the line under it. A sentence with crowsnest's own plumbing in it. A "+" that makes
you choose between fast and aimed. And a few moments that pass without a word: a snooze, a reply
sent, a merge that ends under a toast. Round 1 moved the explanations out of the way; this round
is about making what remains agree with itself and letting the good moments be noticed.

## Not checked

- No real phone, herdr, Claude or ElevenLabs. Voice was off (I unset the key so the demo could not
  spend credit), so hold-to-talk and the transcription fix are unverified.
- Not re-tested from round 1: O-B3 on a done job, O-B7 (a refused Land, and the demo's Website
  Land), O-B10's project-create toast, the server half of O-B2, and "Run the check again" on a job
  with no check.
- High contrast: my script used the wrong theme id, so it was not looked at. Firefox, tablets and
  landscape were not run. Queue zero was not reached.
- One of my scripts tapped a row whose title contains "history" instead of the Inbox's History
  tab, so History was not looked at.
- I froze and resumed my own demo server once (SIGSTOP), changed one limit and put it back with
  Reset, created 38 throwaway jobs in the demo's state, and stopped the server at the end. Port
  28210 is free and `git status` is clean apart from this file.
