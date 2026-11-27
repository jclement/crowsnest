# R8 product review and brainstorm (Fable)

Reviewed `main` at `44883af` on 2026-11-27. Read-only: this file is the only thing written. Nothing was built, run or served; findings come from the documents, the code and the committed screenshots. Sizes are my estimates: **hours**, **a day**, **days**.

## Verdict

The middle of the product is now strong. Between "a job exists" and "press Land", crowsnest does more than any tool of its kind I know of: its own check, budgets, one queue, a decision screen, a real diff viewer, gates that three review rounds failed to walk around. Most of what R4 asked for was built in a day and a half.

The weak parts are the two ends and the fact that nobody has used it.

- **Nobody has used it.** No real phone, no real `tailscale serve`, no Linux box, push seen once on desktop Chrome. Every phone feature since R4 is verified against fakes. This was R4's first item and it is still the first item.
- **The front end of a job is Jeff typing.** Work never arrives by itself: no issue, PR comment or red CI run becomes a job.
- **The back end of a job stops at the repository's edge.** Land is a local merge or a GitHub pull request. After that crowsnest knows nothing: not whether CI passed, not what the reviewer said. For Barreleye (Azure DevOps, protected `develop`) the loop ends at a branch nobody pushed.
- **You cannot see what was built.** A finished job is text and a diff. For a web app that is the wrong evidence, and from a phone it is the only evidence.

So: stop adding to the middle. Use it for a day, then build the two ends.

---

## 1. Half done

Ranked by how much each will hurt in real use.

| # | What | Evidence | Why it matters to you | Size |
|---|---|---|---|---|
| H1 | **The phone, for real** | STATUS and DESIGN 20: publish-to-tailnet "verified only against a fake `tailscale`"; iOS push, Safari microphone, the key bar and Home Screen install never run; `docs/PHONE-CHECK.md` is written and unexecuted | It is the reason the product exists. Nineteen checks, ten minutes of your time, and I expect five to fail | hours (yours), then a day of fixes |
| H2 | **Land ends at "pushed"** | `internal/landing/land.go`: a pull request is opened only on GitHub with `gh`; any other host gets "pushed; open the pull request there". The PR URL is stored and never looked at again. STATUS item 5 tells you not to use Land's push on Barreleye at all | Your paid work is on Azure DevOps. There the product stops one step before the thing you actually need | a day for an ADO pull request; days for watching it (see M2) |
| H3 | **Codex looks equal and is not** | `NewJobForm.tsx` offers "Claude" and "Codex" with no qualification. DESIGN 20: Codex has no stop hook, no structured approvals, no spend, no answer receipt, no usage-limit detection, no spinning detector | You will pick Codex for a builder one night and get none of the guarantees. R4 asked for a label; it was not added | hours to label and to default Codex to reviewer only; days for parity, and only if the current Codex CLI has hooks to build on (I did not check) |
| H4 | **The folder-trust prompt** | The manual (`troubleshooting/common-problems.md`) says "press the option on the dialog card". DESIGN 20 says that prompt is an unnumbered list, so the card "offers no buttons, only the terminal", and "a new worktree is a new folder" | If both are true, a first job in a new project from the phone stops at a 54-column terminal. One of the two documents is wrong; I could not tell which without running it | hours to find out; a day if the card must drive the list |
| H5 | **Reviewing a big change** | The diff viewer has Viewed marks, split view and fullscreen. Line comments were "designed, not built" (STATUS). Feedback is a free-text message to the job | On a 40-file change you will want to say "this line, not that". Today you describe the location in prose, which is what a diff viewer exists to avoid | a day or two |
| H6 | **History and search** | DESIGN 20: "The archived-job search has an API and no view". The Jobs board has "Show N older". The palette searches only what is live (`lib/palette.ts`: "never asks the server to search") | Monday morning, "what did the Website lead do on Thursday?" has no answer in the app | a day for a History view over the existing API |
| H7 | **Settings with no control** | Per-project running limits: `PUT /throttle` only (DESIGN 20; `LimitsSheet.tsx` has no project field). A job's model: the API takes `model`, the form has dir, role, done-when and engine. Delegation limits are constants | Five projects sharing one limit means Barreleye's builders queue behind a docs job. No model field means every phone job runs on the default model | hours each |
| H8 | **Scheduled work is "run" when delivered** | Manual, `concepts/schedules-and-pokes.md`: "A task is marked run when the poke carrying it is delivered". R4 2.12 asked for job-type to be the default | A week unattended reads as seven green runs whether or not anything happened | hours |
| H9 | **Other machines** | A read-only Machines card on the System page (`OpsCards.tsx`). Not on Now, not in the queue, not in push | quark and bedev are two apps, two badges, two sets of notifications. See M3 | days |
| H10 | **Standing agents over a long life** | DESIGN 20: a lead's compaction, hand-over and daily new conversation ran against the fake herdr only | Leads are the longest-lived agents and the least proven. A week of scheduled work is exactly when this runs for the first time | hours to run once against a real Claude |
| H11 | **Off the tailnet the app is blank** | No `caches.open` anywhere in `web/`. R4 3.8 | On mobile data without Tailscale up, the Home Screen icon opens nothing, with no hint why | hours for a cached shell that says "not on your tailnet" |
| H12 | **Push volume** | DESIGN 23.4: "Not done: default-to-blocking-only push volume and per-project collapsing". Push settings are one set per install, not per device | A finished-job push for every job is how notifications get switched off in week two | hours |
| H13 | **The demo, your first impression** | In the committed screenshots: an approval titled `Edit: /tmp/cne-…/tmp/crowsnest-demo-…/state/demo-src/mobile/src/screens/InboxScreen.tsx` (an absolute path where a path inside the job would do; real jobs will look the same); the project page's Changes card says "is not in a git repository" twice; 9 of 16 items are "Blocking" | The tour film and `--demo` are how anyone meets this | hours |
| H14 | **STATUS has drifted** | STATUS lists the R7 fixes as in flight on `wip/fix-r7`; `git log` shows those commits on `main` (through `aae3d65`). It still says CI is red; I did not check GitHub | You read STATUS to decide whether to tag | minutes |

### The six journeys, and where each one stops

1. **New machine to first job.** Install, setup link, checklist, doctor: good, and the folder picker helps. Stops at: the trust prompt (H4); on bedev, Linux is unverified for doctor and for the load hold; `brew install` itself is untested (STATUS).
2. **A job from the phone to landed.** Voice job with readback: good. Lead takes it, builder in a worktree, check, Accepted, Land tab: good. Stops at: Merge "needs the main checkout to be on the base branch; it never switches branches for you" (DESIGN 20), so a lead that left the checkout elsewhere means a terminal; and at "is it right?", because there is nothing to look at but the diff (M1).
3. **A stuck agent at 11 pm.** Push, decision packet, rescue panel: the best-designed path in the product. Stops at two causes it cannot show. A Claude sign-in that expired (STATUS item 6 says yours does within days): the fix is a login flow in a phone terminal. And an agent on bedev waiting for a signature or secret that devtun wants approved on a laptop that is shut: it looks like a hung command (section 3).
4. **Five projects on a Monday morning.** Now is right for this on a desktop. On a phone, with 16 items the project rows, "Since you left" and "Give a job" are several screens down (`now-390.png`). Leads show "lead off" with no sign of who starts them. The other machine is somewhere else (H9). Last week is not findable (H6).
5. **Reviewing a 40-file change.** Viewed marks and "since viewed" are good. No line comments (H5), no way to send a hunk back with one tap, and the review verdict beside it is a wall of agent prose.
6. **A week of unattended scheduled work.** Budgets and the usage-limit hold will stop runaway cost, correctly. Then each stop waits for you, so a week away is a week of nothing after the first hold. Finished scheduled jobs pile up as "N to land". "Run" means delivered (H8). There is a daily digest and no weekly one.

---

## 2. Missing

Ranked. The first three are the product; the rest are good ideas.

**M1. See what it built.** (a day, then days)
The cheapest version needs almost no new code: `crowsnest job done --attach` already exists. Tell the builder role that a job touching a UI must attach a screenshot (it has Playwright in most of your projects), show it at the top of the result card and the Land tab, and put it in the push. That is a day and it changes what "done" feels like. The full version is a live preview of the job's dev server: section 3.

**M2. The loop after Land, and work intake. They are one feature.** (days)
A per-project watcher that reads, never writes: pull requests opened from crowsnest jobs, their CI runs and their review threads, on GitHub (`gh`) and Azure DevOps. Each event becomes a *proposed* job in the queue: "CI failed on PR 412: fix it?", "Review comment on line 88: address it?", "Issue 97 labelled `agent`: start?". One tap starts it with the failing log or the comment as the brief, in the same worktree branch. A per-project rule can make a class start by itself (red CI on a crowsnest PR is the obvious one). A job's state then runs past "landed" to "merged" or "CI red". For Azure DevOps keep your rule: no push unless you ask, so on those projects the watcher proposes and the push is the tap. This is what makes the product keep working when you are not typing.

**M3. One cockpit for quark and bedev.** (days)
Not federation. Now already has the queue and the project rows; the Machines card already fetches a summary. Widen the read token's summary to carry the other instance's queue items and project rows, show them on Now with a machine chip, and make each a link to the same decision packet on that instance (Sign in with Tailscale means no second login). Push stays per instance but names the machine. You answer on the instance that owns the item; you look in one place.

**M4. Credentials an unattended agent can use.** (a day)
A doctor group, "can agents work here without you": can this box push to each project's remote, sign a commit, and read what its check command needs, with no human approving? Plus a stuck cause for "waiting on a credential" in place of a silent hang. The answer for bedev overnight is the box's own narrow credentials (a deploy key, a scoped token), not your forwarded ones. Crowsnest should say so; section 3 has the reasoning.

**M5. The limit before the wall.** (hours to a day, unverified)
Today the fleet learns about a usage limit when an agent hits it. On a subscription the number you want on Now is "how much of this window and this week is left", per engine, before you hand out six jobs. Whether Claude Code or Codex exposes that to a script I did not check; if they do, it replaces most of the Spend sheet.

**M6. Playbooks.** (a day)
Saved briefs with their done-when, role, engine, reviewer and budget: "bump dependencies", "fix the red CI run", "review this PR", "write the release notes". Schedules already carry a body; this is the same record without a time. From the palette and from voice ("run bump dependencies on Website").

**M7. Learning between jobs, without a memory system.** (a day)
R4 was right that a second store of agent guidance is a mistake. Do the small thing: when a reviewer finds the same class of problem twice in a project (your own landed screenshot shows a reviewer flagging an unsigned commit against a house rule), the lead proposes a one-line change to that project's `CLAUDE.md` or to the role text, as a diff you land like any other. The repository stays the memory.

**M8. Search across everything agents said and did.** (a day for records, days with transcripts)
SQLite full-text over briefs, results, questions, answers, the journal and review verdicts; transcripts later. One box in the palette. Pairs with H6.

**M9. Notifications worth the interruption.** (hours)
Blocking only by default; everything else collapsed per project; finished jobs in the morning digest, with the screenshot from M1.

**M10. Two attempts, compared.** (days; later)
The same brief to Claude and to Codex in two worktrees, the two diffs side by side, land one and discard the other. Every part exists. It is fun and it is not this week.

**M11. Remote Control handoff.** (hours of testing, not building)
"Open in Claude" and `job_adopt` are built. What is missing is anyone trying the two cases R4 named: a permission answered in the Claude app while its card is open here, and a poke typed while you are mid-message there.

**M12. A second person. Do not.**
One login is a shell as you. The honest way to share work with a colleague is the pull request. If you ever want more, it is a different product.

---

## 3. The devtun question

Not silly. But only one of devtun's five services belongs in crowsnest, one must stay out for a security reason, and none of devtun's code should move.

**What belongs: ports, as "Preview".** devtun brings the box's ports to the laptop. The phone gets nothing, and the phone is where you approve work. Crowsnest already runs on the box where the ports are, already knows each job's process tree (it attributes CPU by pane), and already supervises a `tailscale serve`. So:

1. **Show the ports** a job's processes are listening on, on its card: "Listening: 5173 (vite)". A day. Useful by itself, and at the desk with devtun running the same port is already on your `localhost`, so the card can link there too.
2. **Open preview**: a button, pressed by you, never by an agent, that publishes that one port to the tailnet for an hour and then takes it down, with every live preview listed on the System page. Days, plus a security review round.

How, and the cost:

- **Never under crowsnest's own address** (`/preview/job-12/…`). That puts JavaScript an agent wrote on the origin that owns a shell as you. It also breaks most single-page apps and hot reload.
- **A separate port on the same name is not free either.** Your own safety page says it: browsers send cookies to every port on a host, so a dev server on another port of the same name receives crowsnest's session cookie. The preview must therefore go through a small listener of crowsnest's own that checks the session, **strips the cookie**, and proxies HTTP and websockets to `127.0.0.1:<port>`. That gives you "behind crowsnest's auth" as a side effect. It also has to rewrite the Host header, because Vite and others refuse hosts they do not know.
- What remains: one more surface reachable from the tailnet, carrying content an agent built, opened in the browser that also holds your crowsnest session. Crowsnest's Origin and header checks already assume hostile pages exist; this makes one a tap away. Acceptable on your own tailnet, with the time limit. Not acceptable with Funnel, ever.

Do step 1 and M1's screenshot first. You may find a picture on the card covers most of what you wanted from a preview.

**What must stay in devtun: answering secret and signature approvals.** devtun's whole promise is that the box cannot approve its own requests: the question is answered on the laptop, where the vault is. Crowsnest runs on the box, as the same user as the agents, and its own documents say any agent can act as any other towards it. If the needs-you tray could say yes to an `op read` or a signature, so could an agent. Putting those approvals in the tray would quietly delete the one boundary devtun has.

Two smaller things are fine:

- **Show, do not answer.** If devtun's helper on the box exposed "a request is waiting on quark" read-only, crowsnest could put a notice in the queue: "job-7's `git push` is waiting for you to approve a signature on quark". That fixes the 11 pm case where a job looks hung. Hours on each side.
- **Answering from the phone is a devtun feature**, served from the laptop with devtun's own auth, if you want it at all. With the lid shut there is no vault to ask, which is why M4 says unattended agents need credentials of their own.

**One more that fits: the browser service.** A `BROWSER` shim for agents that turns "open this URL" into a needs-you notice with a link. It makes `gh auth login` and an expired Claude sign-in answerable from the phone. Hours.

**Stay separate:** the SSH connection, reconnect logic, the agents for ssh and gpg, the rules console. Two tools, each on the machine whose trust it holds.

---

## 4. What to cut or simplify

The product has about 69 settings, twelve sheets, eight account-menu entries, nine views and three places that draw the queue. For one user. Each was reasonable; together they are the thing a second reviewer notices first.

| Cut or merge | Why | Do |
|---|---|---|
| **Three ways to give a job** (Now's job box, the Jobs composer, Chat), plus the palette and voice | The Jobs board opens with a composer above the list, as R4 complained about home | Keep Now, the palette and voice. Remove the composer from Jobs; make Jobs a list |
| **The queue drawn three times** (Now, Inbox, the tray) | One model now, still three renderings. On the project page the tray floats over the job cards and covers their chips (`projects-detail-1440x900.png`) | Hide the tray on Now and Inbox. On desktop dock it; never float it over content |
| **Orchestrator and leads** | Two kinds of standing Claude with a routing rule between them, a row each on Now, and an "Unfiled" bucket. With projects in use the orchestrator's job is small | Make "Unfiled" a project like the others, with a lead. Orchestrator off by default once a project exists |
| **Fleet and Terminal as two views** | Both are herdr's model and both are for debugging | One "Agents" view under More with the tree and a terminal |
| **System, Settings, Account, Notifications, Appearance, Voice, Instructions, Pause, Do not disturb** | Nine doors to configuration | One Settings view with sections: This device, This instance, Agents, Safety. Pause and Do not disturb stay as switches in the header menu |
| **The machine strip in the header** (CPU sparkline, memory, load, disk, agents) | The best spot on the screen shows numbers you act on once a month. On a phone it is a "17%" chip | Move to System. Give the spot to the project scope, or to the machine switcher from M3 |
| **"Blocking" on nearly everything** | A routine `npm run test:links` approval and a force push to `main` wear the same word | Rank by consequence. "Blocking" only when work is stopped and no rule could have answered it |
| **Two chips per job** (Done and Accepted; Unverified; in review; needs rebase; held; paused; limited) | The board needs a hint explaining its own chips (`e2e-claude-landed`) | One chip that says what happens next and who owns it: "Ready to land", "Fixing check", "Waiting on you" |
| **Spend detail** (per-call bars, by model, by day, CSV) | An estimate that is "not a bill". R4 said the same | One figure per job and day; put the effort into M5 |
| **Instruction layer editor with revisions and restore** | A second home for guidance beside `CLAUDE.md` and your claude-agent layers. R4 said freeze it; it is still a top-level sheet | Keep the per-project text and "what this agent loaded". Freeze the rest |
| **Task files and app schedules together** | Two sources, shadowing rules | App schedules only; import files once |
| **Walk mode (specified), daily fresh conversation (never run for real), `--notify-url`** | Each is designed, documented and unproven | Delete the spec and the option until someone wants them |
| **Five themes** | Fine. Done. No more | Freeze |

What I would not cut: the acceptance check, holds, the decision packet, rescue, the diff viewer, the manual. Those are the product.

---

## 5. If I had a week

1. **Use it, then tag it** (two days). Day one: you run `docs/PHONE-CHECK.md` on your phone with a real `tailscale serve`, and start an instance on bedev. A builder fixes what fails, settles H4, labels Codex (H3) and corrects STATUS. Tag `v0.2.0`. Nothing else should ship before this.
2. **Close the loop after Land** (three days). An Azure DevOps pull request beside the GitHub one, then the watcher from M2 for crowsnest's own pull requests: CI state on the job card, a red run or a review comment as a proposed job. Issues as intake can follow; the machinery is the same.
3. **Show the thing** (two days). Screenshots as evidence on `done`, on the card, the Land tab and the push (M1). Listening ports on the job card (section 3, step 1). Leave the preview proxy for the week after, behind a review.

**The one thing that would most change how it feels:** every finished job shows you the thing it made, not its claim about it. A picture on the lock screen and at the top of the Land tab turns "read a diff and trust a paragraph" into "look, then press Land". Everything else in this review makes crowsnest more complete; that makes it feel like directing work, not auditing it.

---

## 6. What of R4 is still true

Most of R4 was acted on. Done, by my reading of the code: one count and one queue model, Now, project and job on every item, four tabs, lead-first routing, the mechanical check on `done`, prose questions turned into `ask` (`internal/jobs/prose.go`), budgets, the fleet limit state, spinning, waits with an owner, approval policy, approvals across a restart, the landing queue, the decision packet with a sticky bar, rescue without a terminal, a job by voice, "Open in Claude", backup, retention, the terminal's reach, Sign in with Tailscale, server-side snooze.

Still true:

| R4 item | State now |
|---|---|
| Fable 3.1, Codex top-ten 7: **nothing has run on a real phone** | Unchanged. Still first |
| Fable 5.4: **more than one machine** | The small read-only list was built and sits on the System page. The cockpit is not there (H9, M3) |
| Fable section 6: **Codex as a general job engine**, label it | Not labelled, not limited (H3) |
| Fable 1.5: the queue drawn three times | One model, three renderings (section 4) |
| Fable 1.6: one project scope across the app | The filter is still the Jobs board's, kept in the browser (`crowsnest-jobs-project`) |
| Fable 1.7: orchestrator optional once projects exist | Routing fixed; the orchestrator is still poked for Unfiled work and is on by default |
| Fable 2.5: no model field on the job form | Still none (H7) |
| Fable 2.9: lead upkeep | Built; run against fakes only (H10) |
| Fable 2.12, Codex L6: a schedule is "run" when delivered | Unchanged (H8) |
| Fable 3.4: push volume | Decision-grade text built; blocking-only default and collapsing not (H12) |
| Fable 3.8: no offline shell | Unchanged (H11) |
| Fable 4.1: walk mode | Specified, not built. I would now drop it |
| Fable 5.2, Codex O2: a History view | Retention built; the view is not (H6) |
| Fable 5.6, Codex O5: settings in too many places | More places than before; speak mode, the project filter and tray state are still per browser |
| Fable section 6: instruction editor, task files beside schedules, spend detail, daily fresh conversation | All still there |
| Codex P5: Remote Control two-surface cases | Untested (M11) |
| Codex L4: one writer per checkout | I found no enforcement in the guard; not checked further |

## What I did not check

- I ran nothing: no demo, no build, no tests, no browser. UI findings are from the committed screenshots and the components' source.
- GitHub CI state, the installed Claude and Codex versions, and whether either CLI exposes usage or (for Codex) hooks.
- Whether the folder-trust prompt really blocks a worktree job (H4): the two documents disagree and I could not settle it by reading.
- Sections of DESIGN outside 4.20 to 4.21, 20 and 23, and the security model in any depth. This is a product review.

## Summary

1. The middle (check, budgets, queue, decide, diff, land) is strong; the two ends are missing: work does not arrive by itself, and nothing follows a job past Land, least of all on Azure DevOps.
2. Still nobody has used it on a real phone or a real tailnet. Do that before anything else, then tag `v0.2.0`.
3. devtun: fold in the ports idea as "Preview" through a cookie-stripping listener on its own port; keep secret and signature approvals in devtun, because crowsnest lives on the box those approvals protect against.
4. Cut surface, not features: one place to give a job, one drawing of the queue, one Settings view, one chip per job, and label Codex honestly.
5. The change that most alters how it feels: every finished job shows a picture of what it made, on the card, the Land tab and the lock screen.
