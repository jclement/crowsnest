# R4 product review — Codex

Crowsnest has the right foundation for Jeff's problem: a durable job, explicit reasons for waiting, non-blocking questions, and a way back into the actual agent. It is already considerably more useful than a collection of remote terminals. But it currently supervises **agent activity and declarations more reliably than delivery of an accepted result**. Ten jobs across five projects would still leave Jeff reconstructing ownership, deciding which interruptions matter, trusting self-reported completion, and arranging integration himself. I would keep the architecture and stop expanding the feature surface until three loops are complete: every unfinished job has an owner and a next wake-up condition; every completed coding job has reviewable evidence tied to its revision; every phone interruption leads to one clear decision with enough context. The strongest next release is one Jeff can leave overnight and understand in thirty seconds the next morning.

Review baseline: `main` at `26013085d5f83b17b59462e54d4b71dccc509ae3`, November 26, 2026. This is a product review, not a correctness or security audit. Findings below distinguish source/design evidence from existing screenshots. I attempted desktop Chromium at 1440×900 and Chromium/WebKit at 390×844, but the sandbox denied the demo's Unix socket bind and the browsers' launch. **No interactive journey completed and no new screenshot was captured.** Existing screenshots include earlier builds and 375×812 layouts; I checked their observations against current components rather than treating them as fresh captures. Details and logs are at the end.

Tags: **P0** blocks dependable unattended delivery; **P1** removes substantial recurring pain; **P2** improves usability. **S/M/L** are relative implementation sizes, including validation, not calendar estimates. The brief names two in-flight branches: doctor/first-run/settings/system-stats and landing/throttling/pause/presence/digest. I did not inspect either branch; overlaps below are acceptance criteria for those changes, not assertions that their builders missed them.

## 1. Coordinating many projects from one place

### C1 — Make home answer “what changed and what needs me?” [P1 / M]

**Saw:** Jobs is `/`. Its project switcher, new-job composer, spend, Schedule and Journal precede the attention list. In [the existing phone board](../web/screenshots/tray-jobs-375.png), the first job starts well down the screen. The [desktop Projects capture](../web/screenshots/projects-list-1440x900.png) devotes most space to “Pick a project”; rows contain paths, lead states and counts, not project outcomes. Current [JobsView](../web/src/routes/JobsView.tsx) and [ProjectsView](../web/src/routes/ProjectsView.tsx) retain those structures.

**Why:** Returning to five projects, Jeff needs to distinguish three decisions, one stuck worker, and otherwise healthy progress before assigning another job. A remembered project filter can make a locally quiet board look like a globally quiet day. Chat is not a suitable replacement home: asking for status would cost a turn and depend on a model's memory.

**Recommend:** Evolve Jobs home into an all-projects operational overview: “3 decisions · 1 needs recovery · 2 ready for review,” changes since last visit, then five compact project rows with current objective, next milestone, accountable lead and freshness. Keep job creation one tap away and expand the composer on intent. Use the empty desktop detail panel for this overview. Summaries should link to underlying job records and show their last observation time. The in-flight digest can supply narrative, but the counters and links should work without it.

### C2 — Give Inbox, tray and badges one definition of attention [P1 / M]

**Saw:** [AppFrame](../web/src/components/AppFrame.tsx) counts open questions, approvals and unmatched dialogs for its badge; it does not count stuck jobs. [needsYou.ts](../web/src/lib/needsYou.ts) includes stuck/auth/config/launch jobs, excludes notices and applies browser-local snoozes. [InboxView](../web/src/routes/InboxView.tsx) has its own ordering and source selection. The existing phone board visibly has **12** in the tray and **11** on Inbox. The tray puts stuck jobs below even low-urgency questions, whereas Inbox places them before ordinary questions.

**Why:** These are competing answers to “how many things need me?” A stalled high-value job can fall behind low-impact editorial questions. Jeff should not have to learn three counting algorithms or discover that snoozing on his Mac did not snooze his phone.

**Recommend:** Define one attention-item model with project, job, reason, impact, age, next action and deduplication identity. Make Inbox the full view and the tray its compact view; derive counts and ordering from the same model. Distinguish actionable decisions from informational notices. Prioritize blocked delivery and age as well as command risk. Sync snoozes per owner, with an explicit “only on this device” option if needed. A zero badge must mean no actionable item, including when the only remaining item is a stuck job.

### C3 — Make the front door and project leads' responsibilities visible [P1 / M]

**Saw:** [DESIGN §21](../DESIGN.md#21-projects-leads-instruction-layers-and-app-schedules) makes the orchestrator a cross-project front door and each lead a project planner. Yet both can hold execution jobs, each holds only one at a time, and the orchestrator is still poked about project jobs that the lead also watches. [ProjectDetail](../web/src/components/projects/ProjectDetail.tsx) foregrounds lead lifecycle controls and instruction editing. Child jobs expose a `from j_N` chip in [JobDetail](../web/src/components/jobs/JobDetail.tsx), without a visible delivery tree there.

**Why:** Jeff needs one accountable owner, not two agents reacting to the same event and assuming the other will integrate the result. Long jobs held by a standing agent also make later assignments queue behind it, even though the agent is intended to coordinate.

**Recommend:** Show “Jeff → front door → project lead → workers” as ownership on the job, with clickable parent/children and an integration owner. Route worker events to their lead; escalate to the front door only for cross-project dependencies, owner failure or decisions beyond project authority. Give every project an explicit current outcome and lead-written next step backed by job IDs. Default substantial execution to workers, leaving the lead able to plan and triage. The front door should summarize and arbitrate priorities, not independently re-plan every project event. Presence/digest work may cover part of this; test that one worker completion produces one owning-lead action, not duplicate management.

### C4 — Keep navigation, but demote infrastructure on phones [P1 / S]

**Saw:** [nav.ts](../web/src/components/nav.ts) gives Jobs, Projects, Chat, Inbox, Fleet and Terminal equal mobile rank. A persistent needs-you bar sits above them. Projects → project → Chat with lead is a separate route through substantial configuration content. Fleet's agent terminal and the global Terminal are different capabilities, despite similar names.

**Why:** Six destinations plus a tray consume scarce space and give the recovery tools the same prominence as the core daily work. The conceptual duplication is more costly than the raw number of taps.

**Recommend:** Use four phone destinations: Overview, Projects, Inbox, Chat; put Fleet and the full herdr Terminal under a More/Tools entry and retain direct “Open this agent's terminal” actions everywhere they are needed. Keep the palette as the desktop accelerator. Project rows should have a direct lead-chat action, while project detail defaults to outcome and live work, with settings in a secondary section. Do not remove the terminal escape hatch.

## 2. The agentic-coding loop itself

### L1 — Separate “agent finished” from “accepted and landed” [P0 / L]

**Saw:** [DESIGN §6.7](../DESIGN.md#67-budgets-and-review-gates-after-the-core) explicitly leaves review gates for later. The current [JobDetail Result](../web/src/components/jobs/JobDetail.tsx) presents agent-provided `result_md`, `verified` and `not_verified`; its actions do not provide a review or landing workflow. The [existing done screenshot](../web/screenshots/jobs-detail-done-1440x900-dark.png) shows a green Done and “mise run check: green” as text. `done_when` is optional in [NewJobForm](../web/src/components/jobs/NewJobForm.tsx). A branch mentioned in prose is not an acceptance record.

**Why:** The supervisor can prevent an undeclared stop, but calling `done` satisfies it without proving Jeff's requested outcome. Ten “done” workers can become ten branches Jeff must manually reconstruct and validate. Nudging harder can increase premature declarations instead of fixing this.

**Recommend:** Keep execution state separate from delivery state: agent finished → review required → changes requested / ready to land → landed. Capture acceptance criteria, changed-file summary, base/head revisions, check commands and results, artifacts, unresolved risks, and reviewer disposition. Label existing verification text “reported by agent”; reserve “verified” for recorded evidence at a known revision. A second-engine reviewer should inspect the same immutable revision and report to the builder, with bounded repair rounds and escalation. No self-approved completion for coding jobs that require review. Non-code jobs can use simpler acceptance templates. This is directly in the landing branch's area: a merge button without revision-bound evidence does not close this finding.

### L2 — Give every wait a wake-up condition and an owner [P0 / M]

**Saw:** [Decide](../internal/jobs/decide.go) accepts `blocked`, `needs_you`, `in_review` and `queued` as declared dispositions. The documented blocked record has a reason, but no general dependency reference or mandatory next-check deadline. [DESIGN §20](../DESIGN.md#20-known-limitations-kept-honest-as-the-build-proceeds) says unchanged standing lists do not produce repeated timer pokes. The demo's Postgres-driver job waits for a database; this is a sensible wait, but a reason alone does not say who resumes it.

**Why:** “It stopped, but now there's a yellow label” is only a partial solution to Jeff's complaint. A declared block can persist indefinitely, and a parent awaiting children needs deterministic wake-up behavior rather than hope that its lead notices a poke.

**Recommend:** Require an accountable owner and one of: dependency job/event, timed retry, external condition with next check, or explicit human decision. Show “Waiting for j_23; lead-website will resume it” or “Database unavailable; recheck at 14:10.” Wake parents on settled dependencies, re-evaluate at startup, and turn overdue unowned waits into attention items. Human pause should be a distinct durable state that the supervisor respects, with a deliberate resume action; it must not be represented as a fabricated blocker. The pause branch should prove these distinctions survive service restart.

### L3 — Bound expenditure and repetition independently of activity [P0 / L]

**Saw:** The nudge streak resets on changed progress or non-crowsnest tool activity ([DESIGN §6.8](../DESIGN.md#68-as-built-in-m7)); [Decide](../internal/jobs/decide.go) caps consecutive no-progress nudges, not total work. An agent alternating “investigating A/B” or repeatedly running a tool can keep the streak clear without advancing the task. The design explicitly says job budgets are not built. [§21.2](../DESIGN.md#212-project-leads-one-lifecycle-for-every-standing-agent) also says leads' own conversations are not metered; Codex spend is unmeasured.

**Why:** The unattended promise needs a bounded failure mode. Activity is evidence of liveness, not useful progress. A per-job display alone also misses expensive lead chatter and delegated trees.

**Recommend:** Add project/run ceilings for elapsed execution, model calls or tokens, and concurrency, rolling child usage up to the parent. Mark coverage prominently when lead/Codex use is unavailable; use time/call/concurrency controls rather than pretending a complete dollar budget exists. Before another recovery cycle, ask the agent for a changed hypothesis and evidence; repeated command/error fingerprints should trigger a bounded diagnostic step or escalation. Never demand changed files as proof of progress: research and tests can legitimately produce none. At a cap, stop new dispatch and request a safe checkpoint; show whether the agent actually paused, and never claim an in-flight external tool was stopped. Throttling/pause may implement these mechanics; dollar estimates alone will not.

### L4 — Plan integration when delegating, not after all workers finish [P1 / L]

**Saw:** Builders default to herdr worktrees, which is a good baseline. [DESIGN §6.10](../DESIGN.md#610-as-fixed-in-r2-part-b-what-agents-may-create-and-how-briefs-travel) caps delegation depth, children and live agent-created jobs. It does not make those caps a repository integration plan. Leads run in main checkouts, non-builder jobs can share a directory, and job records' single-assignee rule does not prevent different jobs from changing the same files. A different-engine child requires human approval, including the reviewer arrangement the role text encourages.

**Why:** Five repos with two jobs each is already enough for shared-file conflicts and review queues. A count cap limits fan-out but does not prevent two legitimate agents from fighting over one checkout or every review becoming another phone permission request.

**Recommend:** Give mutating jobs exclusive checkout ownership and declare a lightweight write scope/dependencies at planning time. Allow parallel isolated branches, but serialize integration per repository. A landing queue must recheck target revision and required checks, invalidate stale review after changes, and send conflicts back to an agent with a bounded repair brief. Let Jeff explicitly preauthorize a project workflow such as “Claude builder + read-only Codex review, within this budget and repository” once, instead of widening engine access ad hoc for each child. Preserve the human landing boundary; do not translate a general job brief into permission to push or merge. Landing/throttling is the likely implementation home.

### L5 — Track the answer through receipt and application [P1 / M]

**Saw:** Non-blocking questions are an excellent existing choice. [DESIGN §8.4](../DESIGN.md#8-needs-you-questions-and-dialogs) details delivery acknowledgements for polling, held typed delivery, uncertain sends that are not retried, and long answers fetched by ID. [AnswerForm](../web/src/components/inbox/AnswerForm.tsx) reports “Answered” after settlement. That proves the decision was stored, not that the agent consumed it and adjusted its plan.

**Why:** Jeff can answer from his phone and reasonably believe the job is moving, while an answer is held behind a dialog or its delivery is uncertain. A non-blocking question also needs to identify which work the agent can safely continue; otherwise it becomes a polite way to stop everything or proceed past the dependency.

**Recommend:** Show three distinct facts: decision recorded, received by the agent, applied/resumed. Use question IDs for idempotent acknowledgement; keep uncertain delivery visible with a reconcile action instead of encouraging blind resend. The question should name the blocked step and independent work continuing meanwhile. For consequential answers, the agent records a short acknowledgement of the condition, especially the note that outranks the selected option. Cancel obsolete questions when their dependent job is abandoned, including held delegation requests.

### L6 — Turn schedules and handovers into observable commitments [P1 / M]

**Saw:** [DESIGN §7.8–7.9](../DESIGN.md#7-the-orchestrator) explicitly treats a poke schedule as run when delivered, including uncertain delivery. Job schedules already avoid overlapping their previous live job, which should be preserved. Daily freshness asks the orchestrator to write `handoff.md` before replacing the conversation; leads have no equivalent daily handover. SessionStart does restore jobs and questions, but a generated summary is still responsible for tacit decisions.

**Why:** “Daily check ran” can mean “a message was sent,” and “new conversation started” can conceal stale or missing handover content. Both failures recreate the feeling that work just stopped, only with plausible status labels.

**Recommend:** Default scheduled work that promises an outcome to a tracked job. Call poke schedules “reminders,” and distinguish delivered, acknowledged and completed. Give runs stable IDs, lateness and failure policies, with no catch-up stampede after downtime. Before conversation replacement, require a fresh handover acknowledgement tied to the outgoing session; preserve decisions, rejected approaches, remaining steps, dependency IDs and acceptance evidence in durable records. Apply the same standing-agent contract to leads. Digest schedules should report unresolved delivery obligations, not merely summarize transcript activity.

## 3. Phone use

### P1 — Design the decision screen around context, then action [P1 / M]

**Saw:** The [existing question capture](../web/screenshots/inbox-question-375x812-light.png) has useful context chips, prose, a large sequence diagram and the start of a unified diff; the answer is below the initial viewport. [QuestionDetail](../web/src/components/inbox/QuestionDetail.tsx) and [AnswerForm](../web/src/components/inbox/AnswerForm.tsx) support notes and evidence. But [the palette capture](../web/screenshots/palette-actions-375x812-light.png) offers “Apply it (recommended)” with only a title and metadata, and the tray offers quick options before opening the full question.

**Why:** The detailed path can require too much scrolling; the fast path can remove precisely the evidence Jeff needs. A “recommended” badge does not establish that the decision is routine. Reviewing a diff on a phone needs deliberate navigation, not merely a narrower diff renderer.

**Recommend:** Put a concise decision brief first: project/job, consequence, recommendation, strongest downside, deadline and what stays blocked. Follow with changed-file counts, findings and checks, then drill into individual files/hunks with wrap control and next-change navigation. Offer a stable Review decision action that opens the answer/note area without losing evidence position. For consequential questions, tray and palette should open this screen rather than submit from a title. Add “Needs desktop review” with a retained place and snooze. Read-result and landing should share this evidence packet; do not make Jeff reconstruct it from Chat.

### P2 — Use notifications for routing; classify questions as carefully as tool calls [P1 / M]

**Saw:** [DESIGN §8.7](../DESIGN.md#8-needs-you-questions-and-dialogs) restricts notification approval buttons to low-risk tools, with single-use short-lived tokens. Good. It also gives confirm/single-choice questions their first two option buttons, without an equivalent semantic consequence classification. A question can ask about applying a change or an irreversible action. The same section documents plain notifications without action buttons on iOS/Safari; the phone workflow must not depend on Android/desktop behavior.

**Why:** Wrapping a consequential action in an agent-written question should not give it a shorter approval path than a structured tool call. Two options from a larger choice can also be misleading. Push acceptance by a service is not proof Jeff saw it.

**Recommend:** On the lock screen show instance/project, decision title, blocked-work impact and age; tapping should open the exact still-live item after authentication. Default consequential questions to “Review” rather than direct execution. Permit inline answers only for explicitly classified routine questions whose complete choices and consequences fit, and retain the server's expiry/conflict checks. Add server-side quiet hours, per-device preferences and a daily/return digest while preserving an urgent channel. Record “sent to push service” separately from “opened/answered.” Presence/digest should reduce duplicate interruptions without silently suppressing the last working notification path.

### P3 — Make voice-created jobs durable before they become chat history [P1 / M]

**Saw:** [NewJobForm](../web/src/components/jobs/NewJobForm.tsx) has attachments and text, but no voice composer. Voice is in [ChatView](../web/src/routes/ChatView.tsx), targeting the orchestrator or selected lead. A spoken job therefore enters as a chat request and relies on the agent creating a tracked job. The ordinary job form offers “To the lead” versus “As a job,” which is useful but asks Jeff to understand execution mechanics at assignment time.

**Why:** The most natural phone interaction—say what needs doing—can bypass the durable object that makes the never-stop promise meaningful. A busy lead's conversation is a poor backlog.

**Recommend:** Add “Dictate job” to the same durable job-creation form, retaining target project and showing a short transcript preview. Default to the project's lead planning execution, with “Run directly” as an advanced choice. If Chat/Remote receives a work request, require the standing agent to echo a job ID once accepted and show that linked job inline. Distinguish “message received” from “job accepted.” Capture a minimal proposed definition of done, allowing Jeff to amend it without a lengthy intake wizard.

### P4 — Make stuck-agent rescue a focused recovery flow [P1 / M]

**Saw:** The [stuck-job capture](../web/screenshots/jobs-detail-stuck-375x812-light.png) usefully explains the rate limit, countdown and Retry now. [JobDetail](../web/src/components/jobs/JobDetail.tsx) similarly offers terminal/nudge actions by cause. The [phone terminal capture](../web/screenshots/tray-terminal-375-webkit.png) has a key bar, attention bar and six-way nav. Its long demo line wraps heavily even before a real software keyboard appears. [DESIGN §20](../DESIGN.md#20-known-limitations-kept-honest-as-the-build-proceeds) acknowledges unnumbered folder-trust prompts that still require terminal interaction.

**Why:** A readable terminal is valuable, but “Open terminal” transfers diagnosis and keyboard mechanics to Jeff at the worst moment. Retry before a known reset time and another nudge after repeated identical failure are often useless actions.

**Recommend:** Open recovery with cause, last useful activity, automatic attempts, next automatic attempt and one recommended action. Offer the affected pane directly, with project/job identity pinned. Add an explicit terminal focus mode that collapses navigation while preserving an exit and urgent indicator. Keep trust decisions manual, but explain the exact prompt and safe navigation needed. Validate on a real iPhone with the keyboard open, reconnect after app suspension, long-press paste, Bluetooth keyboard and a second browser taking control. The existing viewport/key-bar code is promising; screenshots and emulator tests do not establish these behaviors.

### P5 — Give crowsnest and Claude Remote Control separate jobs [P1 / M]

**Saw:** [DESIGN §7.10 and §21.2](../DESIGN.md#7-the-orchestrator) describes named Remote Control sessions for orchestrator and leads. [LeadControls](../web/src/components/projects/LeadControls.tsx) displays a status note; it does not render an “Open in Claude” session link. Crowsnest reads transcripts and owns jobs, questions, approvals, schedules and recovery bookkeeping. The docs demonstrate that Remote Control starts, not that every cross-client workflow is integrated.

**Why:** Jeff should not have to remember whether he issued a job, answered an ask, or merely chatted in a different UI. Remote's conversational convenience and crowsnest's cross-project accountability are complementary; two competing sources of job truth are not.

**Recommend:** Use crowsnest for overview, durable assignment, decisions, evidence and landing; use Remote for sustained conversation with one standing agent. Add an “Open in Claude” action only when an actual current session URL is available, with a named-session fallback and availability reason otherwise. Do not invent a deep link from a display name. Keep job/question IDs visible in both the standing-agent prompt and replies. Test a Remote-origin work request creating a crowsnest job, a decision answered in either client becoming settled once, and a stale session after restart. Define presence/input ownership without claiming Remote activity can be observed when it cannot. These are integration requirements inferred from the repository, not a verification of the current external Remote Control service.

## 4. Voice

### V1 — Replace the 1.5-second cancellation race with task-appropriate confirmation [P1 / M]

**Saw:** [ptt.ts](../web/src/lib/voice/ptt.ts) sets `REVIEW_MS = 1500`; release/stop transcribes and auto-sends after that interval. At five minutes it also stops and sends. [VoiceComposer](../web/src/components/voice/VoiceComposer.tsx) labels this “Sending what you said…” and provides Cancel. Dictating an Inbox note only fills a field, which is a safer distinction already present.

**Why:** A glance at a long transcript is not enough to catch a misheard repository, negation or branch. On a walk Jeff may not be looking at the screen at all. Auto-sending conversational text is reasonable; turning that same gesture into a consequential command is a different commitment.

**Recommend:** Preserve fast auto-send as an explicit conversational preference. For job creation, read back destination and a concise interpretation—“Website: fix checkout validation; create job?”—and wait for confirmation or correction. For structured decisions, bind speech to a visible/read-back question ID and the whole condition. Do not infer approval from silence or a generic “yes” after another agent interrupts. A recording reaching its limit should become a recoverable draft rather than an invisible commitment.

### V2 — Finish the hands-free turn-taking loop before adding wake words [P1 / M]

**Saw:** [VoiceButtons](../web/src/components/voice/VoiceButtons.tsx) reads a question's title/options and records into the note; choosing/submitting remains separate. The [recording screenshot](../web/screenshots/chat-talk-375x812.png) assumes a visible screen. SAY gives one sentence per reply. Skip/Stop controls exist, but the product contract does not provide spoken question selection, correction and acknowledgement as a complete loop.

**Why:** Push-to-talk plus spoken summaries is useful eyes-on dictation, not yet a reliable hands-free workflow. A spoken recommendation without its key caveat can be actively unhelpful. Driving is especially unsuitable for reviewing a diff or approving a shell action.

**Recommend:** Start with an explicit foreground “walking mode”: earcons for listening/transcribing/accepted/error, automatic playback ducking while recording, barge-in that does not lose the pending question, “repeat,” “skip,” “what needs me?” and “answer the Website question.” Read the consequence and recommended choice as well as the title. Require read-back before committing a conditional answer. Keep irreversible approvals and code-review decisions for an eyes-on screen. Defer background wake/continuous listening until real-device audio, suspension and battery behavior are demonstrated; do not market the current browser loop as dependable while driving.

### V3 — Preserve important speech while collapsing chatter [P2 / M]

**Saw:** [queue.ts](../web/src/lib/voice/queue.ts) correctly serializes speakers in one browser, requires per-agent opt-in, holds at most eight waiting lines, and drops lines older than two minutes. It is FIFO and has no event importance. The queue is browser-local, so the “single queue” is not a single queue across Jeff's laptop and phone.

**Why:** Five leads finishing together can obscure an urgent question behind status chatter; dropping old speech is sensible for updates but not sufficient for unresolved decisions. Distinct voices help recognition, but are not enough to establish project identity.

**Recommend:** Prefix each spoken interruption with its project. Coalesce routine progress per job, prioritize actionable blockers, and keep missed decisions in an explicit audible/visual recap rather than relying on stale speech. Provide a chosen active listening device or a clear local-only explanation to avoid two devices speaking. Preserve per-agent opt-in, master mute and manual replay. Missing SAY should not disappear from a delivery digest just because it produced no sentence to speak.

## 5. First five minutes and day thirty

### O1 — Onboard to a successful remote job, not merely a login [P1 / M]

**Saw:** [README Quick start](../README.md#quick-start) and setup code provide a strong locked first-run owner flow. After that, herdr/Claude availability, Claude login, folder trust, project creation, HTTPS, phone installation, notifications and voice remain distinct tasks. A new worktree may introduce another trust prompt. [ProjectDetail](../web/src/components/projects/ProjectDetail.tsx) offers many controls before the user has proved one complete loop.

**Why:** The first experience can be a nicely authenticated page containing an agent that cannot start. Finishing setup while seated at the Mac does not establish that the phone will receive tomorrow's blocker.

**Recommend:** End setup with a readiness checklist populated by actual probes: engine authenticated, project usable, one test job completed, question answered, phone HTTPS reachable, push test observed, optional mic working. Show safe remedies and the affected pane; never auto-accept folder trust. Offer a five-minute guided job that produces an artifact and asks one harmless question, then returns its evidence. The doctor/first-run branch should own this. Its success criterion is completing that loop from the phone, not a green process-health page.

### O2 — Retain delivery evidence and clean execution debris separately [P1 / M]

**Saw:** Jobs has an older-results toggle rather than a durable archive workflow. [DESIGN §8.6](../DESIGN.md#8-needs-you-questions-and-dialogs) releases a job's attachment copies when it settles and allows old released blobs to be evicted under quota. [§20](../DESIGN.md#20-known-limitations-kept-honest-as-the-build-proceeds) documents orphaned worktrees after interrupted launch. The spend meter reads only the newest 300 board jobs. These are different retention policies with different consequences.

**Why:** At ten jobs a day, day thirty is already around the meter's documented window and hundreds of branches, transcripts and results. A report remaining in the UI while its evidence disappeared is worse than a clearly archived artifact. Deleting a worktree too early can discard the only unlanded implementation.

**Recommend:** Add an archive and cleanup inventory: job delivery state, retained evidence, branch landed status, dirty worktree status, live agent, disk usage and cleanup eligibility. Keep review/landing evidence pinned through an explicit retention period independently of disposable input copies. Never clean unlanded or dirty worktrees automatically. Show unavailable evidence as unavailable, with reason and timestamp. Add paginated/searchable history and query live obligations independently of a newest-N display limit. The landing branch should provide the landed predicate, not merely hide old cards.

### O3 — Make recovery and upgrades preserve the work ledger [P1 / M]

**Saw:** [README Files](../README.md#files) spreads necessary state across SQLite, configuration, agent state, transcripts and repositories. The update path verifies the binary and can restart the service while herdr/agents continue, which is useful. I found no user-facing backup/restore workflow in the documented command surface. The design acknowledges hook/transcript formats as moving dependencies.

**Why:** A machine loss, bad upgrade or database rollback can lose decisions or replay old scheduling state while agents continue elsewhere. Preserving the executable is not preserving Jeff's work.

**Recommend:** Document and implement a consistent backup manifest covering database, instruction revisions, schedules, retained artifacts, project/worktree references and relevant configuration; state explicitly which secrets and transcripts are included. Restore into a paused/reconciliation mode that inventories live agents and pending decisions before dispatch, rather than resuming from stale assumptions. Add a “known working engine/herdr versions” health view and an upgrade smoke test that exercises ask/answer/stop/resume. A signed update should remain blocked from claiming workflow health until those probes succeed. Start with a tested local backup/restore procedure before building a large backup UI.

### O4 — Explain the service's authority in terms of machines and repositories [P1 / M]

**Saw:** [DESIGN §1 and §20](../DESIGN.md#1-goals-and-non-goals) intentionally limits management to one herdr session per process; remote agents cannot use its local socket. Yet the full herdr TUI exposes machines in the user's herdr configuration. Agents and crowsnest share a uid; tokens prevent accidental misattribution, not hostile same-user access. Protected-host/branch guards are command checks, not OS isolation. On the existing phone screenshots the instance name is truncated.

**Why:** “This project” can look like a narrower boundary than it is. With several machines, similarly named jobs and project leads, Jeff needs to know exactly which host and checkout a decision will affect. The shell's authority is material product information here.

**Recommend:** Pin instance/host, project and target repository on every consequential decision and recovery action. In settings, make full-session TUI access an explicit capability with its remote-machine reach described; default phone recovery to a local pane. Show configured protected hosts and branches without suggesting the guard replaces isolation. Provide session/device revocation independent of changing a password. For multi-machine use, start with named instance switching and cross-links; keep ownership local and explicit. Federated dispatch is a later project requiring remote identity, transport and recovery design, not an extension of the existing Fleet tree.

### O5 — Consolidate settings around the scope of the change [P2 / S]

**Saw:** Voice has instance defaults and per-browser/per-agent opt-ins; notifications have install-wide kind settings and individual device subscriptions; instructions have global/role/project layers, file precedence and “changed since start”; schedules can be file-backed or app-owned. Much of this provenance is documented and some is already shown, but controls are distributed among account sheets, Chat menus and project pages.

**Why:** At day thirty, “why didn't my phone speak?” or “why didn't the lead use my edit?” can cost more attention than the original setting saved. Restarting an agent just to discover which setting applies is disruptive.

**Recommend:** Group settings into This device, This instance, This project and Agent defaults. Every setting should say where it comes from, what it affects and when it takes effect. Keep file-backed items read-only with a clear source path and keep instruction-revision comparison. Show the active voice route and push readiness in one device panel. This is a scope/wording exercise first; the in-flight settings branch should avoid adding another parallel control surface.

## 6. What to cut

### X1 — Cut redundant management turns, not the durable jobs model [P1 / S]

**Saw:** [DESIGN §20](../DESIGN.md#20-known-limitations-kept-honest-as-the-build-proceeds) records the orchestrator reacting to agents/jobs it did not start, sometimes returning `noop`; leads now also receive project-job events. Timer prechecks and deduplication already prevent many unnecessary turns, so the remaining problem is semantic ownership, not simply a shorter timer.

**Why:** A “never stops” system can become a system that never stops narrating. Jeff pays in tokens, context growth, summaries and notifications without a corresponding advance in delivery.

**Recommend:** Remove routine project-worker events from the front door once their lead owns them. Aggregate settled siblings into one project update and only escalate an unresolved obligation. Keep event-driven retries and one manual poke for diagnosis. Presence/digest should reduce model work, not merely conceal its output. Measure useful follow-up per poke and management spend separately from execution spend.

### X2 — Cut misleading shortcuts and promises until their contracts are complete [P1 / S]

**Saw:** Green Done is stronger than the recorded evidence; schedule “run” is stronger than delivery; quick-answer paths can hide the decision packet; engine choices imply more symmetry than Claude hooks versus Codex backstop actually provide. These are documented limitations, but users encounter controls before they encounter DESIGN.md.

**Why:** Each overstatement asks Jeff to supply missing supervision mentally. That defeats the purpose of a coordinating product.

**Recommend:** Rename these states now: “Agent finished,” “Reminder delivered,” “Reported checks,” and concise per-engine supervision/usage coverage. Route high-consequence quick answers through review. Keep both engines, but make their capabilities visible at selection and on the job. Do not claim a full unattended coding loop until review, dependency wake-up and bounded execution are present. These are small changes that can ship before the larger mechanisms.

### X3 — Defer expansion that does not help Jeff accept the next result [P2 / S]

**Saw:** The product already has extensive terminal rendering, voice selection, instruction editing, scheduling and six primary views. The missing pieces above are mostly contracts between these capabilities. The existing key bar, structured approvals, question notes, worktree default and instruction provenance all earn their place.

**Why:** Another mode can make a demo richer while making daily operation harder. More controls are not the same as less coordination work.

**Recommend:** Defer wake words, broad multi-host federation, arbitrary skill-file editing, more terminal customization and additional dashboard panels until an overnight five-project trial succeeds. Demote system stats to diagnostics unless they explain a stalled job or throttled queue. Keep the palette, but make it a shortcut to the same decisions, not a separate approval product. Preserve the escape hatches and concentrate design effort on the job-to-reviewed-result path.

## Ranked top ten: do these next

| Rank | Work | Priority / size | Concrete acceptance check |
|---|---|---|---|
| 1 | **Finish the acceptance and landing contract** (L1, L4) | P0 / L | A coding job cannot be presented as accepted on its own `done` claim; reviewer/check evidence names the revision, changed code invalidates it, and a stale landing target returns to repair instead of silently merging. Coordinate with landing branch. |
| 2 | **Own every wait** (L2) | P0 / M | In a ten-job run, every nonterminal job has an assignee plus a wake-up condition or an explicit human pause. Restart the service; dependency completions still resume the right parent. |
| 3 | **Bound unattended execution and delegated spend** (L3) | P0 / L | A worker that repeats tools/changes progress text cannot evade a configured run ceiling. Children count toward the parent; unmeasured lead/Codex usage is visible. Coordinate with throttling/pause. |
| 4 | **Unify attention and build the return-home overview** (C1, C2) | P1 / M | With five projects, three decisions and one stuck agent, Jeff can identify all four obligations without opening Chat; Inbox/tray/badge agree, including when only the stuck job remains. |
| 5 | **Route responsibility through the owning lead** (C3, X1) | P1 / M | One worker completion triggers one owning-lead follow-up; the front door sees the project outcome and unresolved escalations, not duplicate busywork. Parent/child ownership is navigable. |
| 6 | **Ship a phone decision packet** (P1, P2) | P1 / M | From a push, Jeff sees project, consequence, evidence and recommendation before a consequential answer; notes survive; stale decisions are rejected; long diffs remain navigable. |
| 7 | **Prove the first remote loop on a real iPhone** (O1, P4) | P1 / M | Install/HTTPS → new project → job → trust/approval → background push → answer → result, including keyboard, suspension and reconnect. Publish failures as failures, not emulator passes. Coordinate with doctor/first-run. |
| 8 | **Show answer delivery and receipt** (L5) | P1 / M | Answer while the agent is blocked and while delivery is interrupted: the UI distinguishes stored, held/uncertain and received; retry/reconciliation does not duplicate the action. |
| 9 | **Make job creation by voice and Remote accountable** (P3, P5, V1) | P1 / M | A spoken or Remote-origin work request gets a durable job ID and explicit project; ambiguous interpretation is corrected before commitment; the app links to the actual current Remote session when known. |
| 10 | **Add the day-thirty retention/recovery baseline** (L6, O2, O3) | P1 / M | Restore a backup paused, reconcile agents before dispatch, preserve pinned review evidence under attachment pressure, and identify safe worktree cleanup without deleting unlanded work. |

A useful outcome metric is **accepted jobs per human interruption**, with companion measures for time spent silently idle, time from decision to resumed work, management-token overhead, and time from agent-finished to landed. Run a scripted five-project day and then an actual overnight trial. More nudges, more running agents and more green process indicators are not success measures by themselves.

## What I did and did not look at

- Read the original [BRIEF](../BRIEF.md), the requested DESIGN areas (goals, job/supervisor loop, orchestrator, questions/approvals, UI, limitations, projects/leads), README, STATUS and [testing guide](../docs/TESTING.md); traced routes and relevant job/project/chat/inbox/tray/voice components and supervisor decision logic. STATUS is demonstrably stale: it calls push and schedules unbuilt although current code/design include them. I did not base absence findings on that status table.
- Visually inspected eleven existing screenshots: phone Jobs/tray, project detail, question/diff, dangerous-approval tray, terminal, stuck job, lead Chat, recording UI and palette question actions; desktop Projects and done-job result. Linked captures above are existing artifacts from different revisions, not proof of this checkout's live rendering. In particular, older captures omit newer navigation/features; current-source checks constrain the findings.
- Discovered tasks with `mise tasks`. Ran `mise run build` with `TMPDIR` inside `.dev/r4-codex/`; it completed and reported `built bin/crowsnest v0.1.0-90-g2601308 (2601308)`. It also emitted the Vite large-chunk warning and `go: writing stat cache: ... operation not permitted` for an external Go module cache. This was a successful build with warnings, not a clean validation gate.
- Attempted a throwaway demo on port **28764**, launched and cleaned up inside one shell command, with all intended scratch state in `.dev/r4-codex/`. It failed at `listen unix .../state/agent.sock: bind: operation not permitted`. Chromium failed at `bootstrap_check_in ... MachPortRendezvousServer ... Permission denied`; WebKit also terminated during launch. Logs/script: [build.log](../.dev/r4-codex/build.log), [demo.log](../.dev/r4-codex/demo.log), [walk.json](../.dev/r4-codex/walk.json), [walk.mjs](../.dev/r4-codex/walk.mjs). These are ignored local review artifacts, not durable report dependencies.
- Consequently did **not** actually click through answering, tool approval, voice job creation, result/diff review, landing or terminal rescue in this run. Those flows were evaluated from source, existing screenshots and documented tests. The requested fresh **390×844** walk remains unverified. No fresh visual/layout defect is claimed from that failed attempt.
- Did not run `mise run check` or the full test suite for this read-only product report. No green-suite claim is made. Did not run real Claude/Codex, herdr commands, external voice or push services, Tailscale, installed PWA/Safari on hardware, Remote Control cross-client actions, migrations, production operations, or an overnight workload. Existing test results belong to their authors and scopes, not this review.
- Did not inspect the two in-flight branches, external Remote Control documentation, threat-model bypasses or every backend line. No deployment, commit, push, PR or work-item change was made. The only authored review output outside ignored scratch is this report; build products were regenerated as the brief permits.
