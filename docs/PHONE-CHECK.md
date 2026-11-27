# The real-phone check (ten minutes)

Everything on `wip/phone` was tested in Playwright's WebKit at 390x844 with touch and in Chromium's
phone emulation, never on an iPhone. This is the pass that turns those emulator results into facts.
Write down every failure as a failure (Codex top-ten 7): a line per item, with what you saw.

## Before you start (once, two minutes)

1. On the Mac, run crowsnest normally (not `--demo`), with talk mode if you want the voice items:
   `mise run dev` in its own herdr tab, or the installed binary.
2. Expose it over HTTPS on the tailnet (the phone's microphone and Web Push need a secure context):
   `tailscale serve --bg 2769`, then set `CROWSNEST_TRUSTED_PROXY=loopback` and restart crowsnest.
   `crowsnest doctor` says whether serve is set up.
3. On the iPhone, open `https://<mac>.<tailnet>.ts.net` in Safari, sign in, then Share → **Add to
   Home Screen**, and open it from the Home Screen icon (Web Push only works installed).
4. In the app: account menu → Notifications → **Turn on**, allow; then **Send a test notification**.
   ✔ It arrives on the lock screen.
5. Have something waiting: give a project job from the Mac that will ask a question and need an
   approval, e.g. on a scratch project: "Ask me (ask tool, blocking) whether to use tabs or spaces,
   recommend spaces, then run `go test ./...` and report."

## The checks

| # | Do this | Expect |
|---|---|---|
| 1 | Lock the phone. Wait for the question to be asked. | A notification titled `<mac>: <Project>: blocking question`, body `j_N <job title>: <question> · Recommended: <label>`. No buttons (iOS shows none; that is expected). |
| 2 | Tap the notification. | Face ID, then the app opens on the **decision packet** for that question (not the Inbox, not Now). Project, job and "waiting …" at the top; "If you choose" lists both options; the answer bar is at the bottom, fully visible above the bottom nav. |
| 3 | Type a note in the answer bar ("only if the linter agrees"). The keyboard comes up. | The bar stays above the keyboard; the text you type is visible while typing. |
| 4 | Rotate to landscape, then back. | The note is still there. |
| 5 | Switch to another app for a minute (Messages), come back. | The note is still there. |
| 6 | Press the recommended option. | A toast "Answered q_N"; the packet moves to the next decision or to Now. |
| 7 | Open the same notification again (Notification Centre) or press Back. | The packet says "Already answered." and shows no answer bar. |
| 8 | Wait for the `go test` approval (lock the phone first). | Notification `<Project>: approval`, body names the job and the command. Tap it: the packet shows the command, "Low risk" or the reason it is not, Allow once / Deny in the bar. Allow once. |
| 9 | On the project's page, add an approval rule "Bash `go test *`". | Before saving it lists the recent calls it would have matched (the one you just allowed). |
| 10 | Make the job run `go test ./...` again (message it from the job's page). | No notification and no card: the timeline says "Auto-allowed by rule r_N". |
| 11 | Find a stuck or needs-you job (or interrupt one: Rescue → Interrupt). | Rescue panel open on the job; Interrupt shows "Sent Esc"; Continue/Nudge work; Compact now is enabled only when the agent is idle. |
| 12 | Open the job's terminal (Rescue → Open the terminal). | The key bar is two rows; every key is on screen and 44 pt tall; A+/A− change size; Copy copies the screen (paste it into Notes); ^C interrupts. Landscape: one row. The on-screen keyboard does not hide the cursor line. |
| 13 | (Talk mode) On Now, hold **Hold to give a job by voice**, say "Scratch: add a README line saying hello", release. | "Transcribing…", then the text, the project chip selected, and the readback "Scratch: Add a README line saying hello. Create the job?" (spoken too if speak mode is on). Nothing is created yet. |
| 14 | Press Create, then Undo within 8 s. | "Created j_N for Scratch", then "Undone: j_N cancelled"; the board shows it cancelled. |
| 15 | Say something naming two projects. | "That named more than one project. Pick one:" and Create is disabled until you pick. |
| 16 | On the project page (or Chat's header), press **Open in Claude**. | The Claude app (or claude.ai) opens on that lead's live Remote Control session. If the button is missing, the line under it says why. |
| 17 | In the Claude app, ask the lead to do a small thing. | The lead says it recorded a job (`job_adopt`) with an id; that job is on crowsnest's board. |
| 18 | Restart crowsnest on the Mac while an approval is waiting on the phone. | The card is still there after the restart and can still be allowed from the phone (within 150 s the hook reconnects). |
| 19 | Turn on airplane mode for 30 s, then off. | The connection banner counts down and reconnects; the packet you were on still works. |

## Report

Paste the table back with ✔ or ✘ and a few words per row into the next brief. Known limits that are
not failures: iOS shows no notification buttons (Allow/Deny/Snooze are Chrome on Android and
desktop only); the microphone does not work with the screen locked.
