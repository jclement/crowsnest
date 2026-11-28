# R9 correctness review — Codex

Reviewed `2a13f09fdda64695ba3dd0458d534e35e56d2c85`, including the changes since R7 (`3456e19`), the builder reports, and the migration history at the local `v0.1.0` tag. The checkout was clean at the start. Static review only: no tests, builds, servers, herdr commands, tailscale commands, commits, pushes, or state-changing git. Only this report was written. Scenarios below are code traces, not runtime reproductions. Builder test results are claims about their tested revisions, not an independently verified gate here.

**Not fit to tag. 14 findings: 3 high, 9 medium, 2 low. R7 verification: 8 fixed, 4 partly fixed, 0 wholly unfixed.** “Fixed” below addresses the original failure, not certification of the whole subsystem.

At the final status check, concurrent work had modified four terminal/conversation screenshots and created `internal/jobs/e2e-stop-d30950b651/`. Those changes were not made, reverted or used as validation by this reviewer.

## R7 verification

| ID | Verdict | Evidence and limits |
|---|---|---|
| R7-C01 | partly | `internal/landing/isolated.go:38–70` now creates a unique detached worktree and verifies its initial HEAD; `internal/jobs/accept.go:488–503` fails on copy/prepare errors without falling back. Default mode is explicitly the live worktree, with endpoint fingerprints (`accept.go:373–415`). However the isolated tree is never revalidated after prepare/check, and crash cleanup is absent: R9-C6/C7. This is an isolated starting copy, not yet reliable evidence that the pinned source was tested. |
| R7-C02 | fixed | `internal/jobs/recovery.go:82`, `budgets.go:486–526` replay propagation from durable ancestor holds; `holds.go:67–74` checks ancestors at admission even before propagation reaches a child. `budgets.go:472–475` walks below already-held children. This closes the specified crash between parent save and child propagation. The five-level bound remains (`budgets.go:44–45,511–512`); do not describe it as an unlimited ancestry check. |
| R7-C03 | fixed | `internal/jobs/budgets.go:681–688` closes grace in every nonterminal state; `:699–715` also bounds it in time. Declaring blocked before Stop no longer keeps grace open across timed wakes. The fallback timestamp is a separate KV write, so expiry is a backstop, not an atomic turn identity. |
| R7-C04 | partly | Timed/dependency wakes check before mutation and before send (`internal/jobs/waits.go:298,326`); review/check feedback uses `deliverFeedback`; retries and automatic nudges check `continuationRefusal` (`recovery.go:465`, `actions.go:255`). The gate checks pause, effective holds, ancestry and fleet (`holdset.go:362–395`). It releases the lock before typing. Plain human messages, keys, budget continuation messages, and standing-agent instruction restart still have exceptions described below. New comment deferral has R9-C3/C4/C14. |
| R7-C05 | partly | `internal/jobs/accept.go:1125–1149` claims one delivery owner, eliminating the original simultaneous callback/tick delivery. Stale claims no longer blindly replay (`lifecycle.go:243–248`). But the claim does not identify a durable immutable message batch: appending new comments during the send replays the prefix, and stale recovery clears even newly appended text. R9-C4. |
| R7-C06 | fixed | `internal/jobs/waits.go:500–525` uses a visited set and returns uncertainty on traversal exhaustion/read failure. The original 33-job cycle is no longer allowed by a 32-hop fail-open traversal. |
| R7-C07 | fixed | `internal/jobs/land.go:335–349` invokes the common viewed gate and pins an omitted head. `internal/changes/changes.go:1023–1039` uses a fresh reading and refuses read/mark errors, wrong HEAD, truncation and unviewed files. Approved agent land requests use this same LandWith path. The explicit gone/not-found exemption at `:1018–1019` relies on landing's independent checks; it is not a general read-error exemption. |
| R7-C08 | fixed | `internal/changes/changes.go:220–227` publishes the reading/error/time through one atomic snapshot, replacing the differently locked mutable fields. No equivalent remaining access to those old fields was found. This is static inspection, not a race-detector run. |
| R7-C09 | partly | `internal/landing/changes.go:120–143,410–435` caps subprocess output before parsing, `:375–379` caps skipped rows; `internal/changes/changes.go:146–157,200–216` bounds concurrent readings and evicts idle entries. These address the reported unbounded output/cache paths. `landing/changes.go:289` still snapshots all tracked content with `git add --update`; `:298–314` stats the entire bounded listing. Output limits do not bound Git's input/object-store work or total snapshot bytes. Aggregate disk/work limits can follow; do not call the entire operation byte-bounded. |
| R7-C10 | fixed | `internal/httpd/browse.go:125–130` uses one Places result; `internal/browse/browse.go:343–345` puts roots and favourites in the same bounded operation. Standalone Roots is also bounded (`:202–203`). |
| R7-C11 | fixed | `internal/orch/freshness.go:208–230` retains a versioned notice; `cmd/crowsnest/jobs.go:404–412` acknowledges only after stdout was written. A lost HTTP response no longer consumes the notice. Stdout acknowledgement remains weaker than transcript acknowledgement; it proves the hook wrote the text, not that the model processed it. |
| R7-C12 | fixed | `web/src/lib/changes-queries.ts:42–48,81–88` sends the expected hash and rejects a mismatching response; the since-viewed baseline participates in the key (`:11–12`). The original B-under-A infinite cache entry is closed. |

## Findings

### R9-C1 — high — Sending back an accepted job leaves it landable before the requested changes

**Files:** `internal/jobs/comments.go:149–175`; `internal/jobs/accept.go:941–958,981–1007`; `internal/jobs/land.go:174–209,320–359`; `web/src/lib/jobs.ts:279–296`.

**Scenario:** A done job has passing evidence at H and all files viewed. Send comments while automation is paused, so its new fix job stays queued. AskChanges creates the child but changes no acceptance/review disposition on the parent. The parent remains done/accepted at H, its chip says Ready to land, and LandWith never checks live fix jobs. Merge/PR can therefore land H without an evidence override although the requested changes have not begun. Even a failed/cancelled fix leaves the parent's old acceptance state intact (`accept.go:946–948`). Teardown can also remove the worktree the queued fix expects.

**Fix:** Reserve a durable requested-changes/fix-pending disposition on the parent before publishing the child, invalidate the old land decision, and enforce it in every landing path. Keep it through failed/cancelled fixes until a deliberate human resolution. The card should show the fix and its actual owner. A successful check alone cannot establish that prose comments were addressed; preserve an explicit review decision for that.

### R9-C2 — medium — Fix admission is neither exclusive nor exclusive with checking

**Files:** `internal/jobs/comments.go:158–169,180–197`; `internal/jobs/protocol.go:77–79,127–140`; `internal/jobs/accept.go:839–874,903–927`.

**Scenario:** Two sends both read an empty fix list, then each independently enters Create's insertion lock. Both fix jobs launch in the same directory/branch. There is no parent reservation or uniqueness constraint between the list and inserts. Separately, Recheck on a done job sets `accept_state=checking` while keeping `state=done`; AskChanges accepts that state and does not inspect acceptance. A comment fix can therefore start during the parent's check. Conversely Recheck does not refuse an existing live fix. `fixInFlight` also explicitly permits an unreadable fix list.

**Fix:** One durable parent-scoped operation reservation shared by AskChanges, AskRebase, Recheck and landing; acquire it and create the child atomically, fail closed on unreadable state, and clear it through recovery. A sequential duplicate-request test does not cover the two-read/two-insert schedule.

### R9-C3 — medium — Paused comments on a blocked job never deliver after resume

**Files:** `internal/jobs/comments.go:92–94,109–138`; `internal/jobs/accept.go:1125–1127`; `internal/jobs/recovery.go:139–143`.

**Scenario:** Job is blocked with a human wake. Pause automation, send comments, then resume. holdComments stores pending feedback without changing the blocked state. After resume the continuation gate permits the job, but deliverFeedback requires StateRunning, so every tick skips the message. No timed/dependency wake exists. The UI already marked the comments sent and told the human they wait only until the job may continue. It needs an unrelated explicit wake to make progress.

**Fix:** Give queued human review feedback a defined transition from blocked to runnable once holds clear, preserving or explicitly resolving the wait metadata. Do not mark delivery complete before that transition/delivery succeeds; expose waiting feedback independently of sent comments.

### R9-C4 — medium — Appending comments to in-flight feedback replays old text or loses new text on recovery

**Files:** `internal/jobs/comments.go:118–121`; `internal/jobs/accept.go:1129,1156–1159`; `internal/jobs/lifecycle.go:243–248`.

**Scenario:** Delivery owns message A and has begun typing. Pause is enabled, then another send queues B by replacing PendingFeedback with A+B. When A finishes, the equality check refuses to clear A+B. After resume, A is typed again with B. If crowsnest crashes instead, stale-feedback recovery clears the entire field, including B, although B was never part of the claimed send. B's comment rows can already carry sent_at. The recovery journal's claim that the findings remain in job_get is also inaccurate for these comment batches: they are not a check output or reviewer result.

**Fix:** Store immutable delivery batches with IDs and pending/in-flight/uncertain disposition. Appends create a subsequent batch. Completion/recovery may settle only the batch actually claimed, never the whole current string. Keep uncertain comments visible with a resend decision.

### R9-C5 — medium — Comment sends have no snapshot/version claim

**Files:** `internal/httpd/comments.go:176–201`; `internal/changes/comments.go:378–397`; `internal/store/diffcomments.go:87–107`.

**Scenario:** Two requests both obtain the same pending IDs from Outgoing before either marks them sent. Both deliver the batch (or attempt concurrent fix creation). A single request also loses an edit: Outgoing reads body A, another browser edits that row to B, delivery sends A, then SetDiffCommentsSent marks the row containing B sent because it matches only ID/subject. B was never delivered. A crash or database failure after the external send similarly leaves drafts that a retry sends again.

**Fix:** Claim a versioned outgoing batch transactionally before external delivery; mark only the captured versions delivered. Keep edits made after the snapshot pending. Recover uncertain external outcomes explicitly and make request retries idempotent.

### R9-C6 — high — Isolated acceptance can pass a different tracked tree than its recorded commit

**Files:** `internal/landing/isolated.go:61–70`; `internal/jobs/accept.go:488–503,396–417`.

**Scenario:** Commit H fails its tests. The configured prepare task generates/repairs a tracked source file (or checks out another commit) and exits successfully. The check passes in that modified copy. Nothing validates the copy after prepare or after the check; runCheck's final Snap is of the original job directory. H is recorded as passing in isolated mode. A check that itself leaves tracked repairs behind has the same result. This does not require a concurrent agent or a malicious human: dependency/code-generation preparation can do it.

**Fix:** Verify the isolated HEAD and tracked source against H after preparation and after checking, fail closed on inspection errors, and define which generated/dependency outputs are permitted. If changing tracked inputs is intentionally allowed, record the actual tested tree and stop attributing its pass to H. Isolation from the builder is useful but is not immutable-source enforcement.

### R9-C7 — medium — A crash leaks isolated check worktrees and possibly running checks

**Files:** `internal/landing/isolated.go:49–55,75–83`; `internal/jobs/accept.go:493,553–567,1027–1037`; `internal/jobs/recovery.go:91–95`.

**Scenario:** Kill/crash crowsnest after the isolated directory is created or during a long check. Cleanup exists only as a defer in that process, and the random path is not durably recorded. The check runs in its own process group without a parent-death/lifeline mechanism. Startup restarts checking but cannot identify/remove the abandoned copy or stop its old command. Repeated crashes accumulate dependency trees and worktree registrations, and old/new checks may run together.

**Fix:** Record an owned per-check workspace and child lifecycle, reconcile them at startup, and give the check process a crash-safe termination mechanism. Ordinary timeout/prepare failure does reach Remove, which ignores cancellation for cleanup; that does not cover process death. Unique MkdirTemp paths prevent two ordinary checks from removing each other's copy; shared-repository prune is not an orphan-directory collector.

### R9-C8 — high — Atomic trust-file replacement can silently lose concurrent agent configuration

**Files:** `internal/agenttrust/agenttrust.go:94–119,195–223,268–302`; `cmd/crowsnest/trust_wiring.go:43–45`.

**Scenario:** crowsnest reads Claude config A. Claude writes A plus a new setting/session/project record. crowsnest renames its trust-modified copy of old A over that file. The later read finds its own trust bit and returns success, so the intervening agent update is silently lost. Two concurrent project-create/trust requests have the same problem. Codex uses the same stale-read/whole-file replacement pattern. Atomic rename prevents partial files, not lost updates; the retry loop tests only whether trust survived.

**Fix:** Serialize crowsnest writers and use a coordination mechanism compatible with the agent's own writer, or refuse/defer updates when safe coordination is unavailable. At minimum retain recoverable originals and detect intervening changes; a re-read without coordination still leaves a last-read/rename race and must not be presented as full protection.

### R9-C9 — medium — A null Claude config panics instead of reporting a failed trust write

**Files:** `internal/agenttrust/agenttrust.go:124–130,159–164`; `internal/httpd/projects.go:207–218`.

**Scenario:** `.claude.json` contains the valid JSON value `null`. Unmarshal into `top` succeeds and sets the map to nil. The routine builds the project entry and panics at `top["projects"] = raw`. The HTTP recovery boundary can drop the request after project creation, rather than return the promised per-agent failure while leaving the malformed config alone.

**Fix:** Require a non-null object at the top level and validate each object node before mutation. Return a normal failure without writing. Also give nonregular config files a bounded/refusal path: readOptional currently uses unrestricted ReadFile.

### R9-C10 — medium — The main job chip hides a blocked job's usage-limit hold

**Files:** `internal/jobs/holdset.go:257–260`; `web/src/lib/jobs.ts:269–273,328–343`.

**Scenario:** A blocked job waiting on a time/dependency gets a usage-limit hold. applyHoldsLocked deliberately preserves StateBlocked and sets stuck/cause=limit. nextStep branches on blocked before inspecting stuck or cause and displays Waiting/Waiting on a job. When the timer/dependency is satisfied, continuationRefusal still prevents its wake because of the limit. The primary board signal therefore names an event that cannot actually resume the job. The exhaustive label test only ensures a label exists, not that the priority is correct.

**Fix:** Give effective holds/recovery causes precedence across all live states, then derive the remaining wait label. Add cases drawn from actual API transitions, especially blocked+limit and blocked+approval-tool.

### R9-C11 — low — “Outdated” ignores the old-side anchor

**Files:** `internal/changes/comments.go:124–142`; `web/src/lib/comments.ts:128–145`.

**Scenario:** Leave an old-side comment, then the base changes while the final file content stays the same. The stored old blob is no longer the displayed old side, but the server compares only NewBlob and still calls it draft/sent rather than outdated. The client correctly refuses to place it inline when OldBlob differs, so this is misleading status/context rather than attaching the quote to a wrong line. Deletion comments also share empty NewBlob regardless of which old content was deleted.

**Fix:** Compare the blob of the commented side (and the relevant comparison identity for whole-file notes), keeping the intended rule that committing unchanged work does not outdate new-side comments. This can follow the tag if documented.

### R9-C12 — low — Push and the queue still disagree about job-stopping questions

**Files:** `internal/push/watcher.go:177–179`; `internal/attention/model.go:109–114,323,333–343`.

**Scenario:** An ordinary non-wait question belongs to a job already needs_you/questions. The queue uses the job state and calls it blocking; push unconditionally calls QuestionLabel(question, false), so it lacks the blocking classification. Sharing a helper does not make the inputs equivalent. Header/Now consume the shared queue counts, but push is not that same snapshot.

**Fix:** Supply the same stopped-job fact at notification time, or explicitly describe push as ask-time urgency rather than the queue's label. This can follow the tag; no evidence found that header and Now independently recompute different counts.

### R9-C13 — medium — Publishing stays green forever after a live child loses its serve session

**Files:** `internal/tsserve/run.go:151–169,176–188`.

**Scenario:** After the first successful confirmation, tailscaled loses/removes the foreground session while the CLI/watchdog stays alive. The poll only calls confirm while `!confirmed`. The card keeps publishing, with a dead URL, and the supervisor never replans/restarts. This is acknowledged in the builder report and confirmed in code; it is still a product-state defect, not verified health.

**Fix:** Periodically verify the owned foreground handler while publishing; on loss, report degraded state and stop/replan the owned child with backoff. Can follow the tag if the card/documentation explicitly avoids claiming continuing health until then.

### R9-C14 — medium — Deferred comments discard the user's draft protection

**Files:** `internal/httpd/comments.go:145–146,228`; `internal/jobs/comments.go:79–95,109–130`; `internal/jobs/accept.go:1142–1143`.

**Scenario:** Send comments with force=false while paused/held. The immediate path would respect a terminal input draft, but holdComments does not retain ForceOverDraft. Later delivery always passes ForceOverDraft:true. A human draft present at resume can be overwritten without the “Send anyway” confirmation the comment UI otherwise requires.

**Fix:** Persist the delivery policy with the batch. Deferred human comments must keep draft protection and wait/report a draft conflict unless that batch was explicitly authorized to overwrite it.

## Additional requested audits and limits

### Send gate, restart and budget recovery

There is no universal “nothing can type into a held/paused pane” invariant. `jobs.Message` directly calls Prompt (`actions.go:44–47`); `httpd.handleKeys` checks a screen hash but not job holds/pause (`agents.go:191–215`). Trust-card keys therefore still reach a held or paused pane. Those are direct human actions, and pause is described as pausing automation, so their existence alone is not counted as a new unauthorized-automation bug. The fresh-launch brief path still needs its normal launch admission after trust; accepting trust is not itself proof that a brief was delivered.

Budget raise/grace continuations call sendText directly (`budgets.go:558–559,642,667–669`), and sendText only checks agent identity (`launch.go:313–315`). They do not share the automatic continuation gate. A budget change is not an explicit lifting of an independent pause/fleet hold; this exception should be narrowed when consolidating delivery admission. Standing-agent instruction refresh checks pause and HeldJobBusy (`orch/freshness.go:154–180`), but HeldJobBusy only checks LifecycleOp (`cmd/crowsnest/upkeep.go:239–254`), not budget/question holds. It can type `/exit` into a pane holding a budget-held job (`orch/upkeep.go:157–164`). That is a lifecycle restart, not evidence of a newly admitted job turn, and is not counted as such here.

The fixed timed wake and feedback gates remain sampled checks, not a reservation against a hold imposed during the external Prompt call. The brief's strongest “any path” formulation is therefore false even after the R7 changes. Direct human override policy, lifecycle restart policy, and automatic job-turn admission need to be distinguished in the claim.

For descendant-budget crashes, the parent is now enough to reconstruct missing holds and admission independently checks it. The inverse operation is still side effects: lifting below the parent (`budgets.go:531–562`) is not a durable all-descendant transaction. The reviewed fix is specifically for enforcement after a lost propagation, not a proof that every release/raise crash is reconciled. No unlimited-depth assurance is made.

### Isolated checks and concurrency

Ordinary checks of the same job are single-flight in-process (`accept.go:311–329`); different jobs get unique temporary directories. Timeout kills the command process group and normal return runs cleanup (`accept.go:550–563`, `isolated.go:79–82`). Neither uniqueness nor that mutex spans a server crash. Preparation and check each receive their own timeout, so the displayed check timeout is not a bound on checkout+prepare+check+cleanup together.

Filters are disabled for initial checkout, LFS smudge is skipped, and submodules/dependencies are deliberately not materialized automatically. This is documented policy, not a missing implicit materialization step. The detached worktree shares repository metadata/objects and the check inherits a selectively scrubbed environment (`accept.go:583` onward), so it is neither a hermetic build nor an isolation boundary against same-user code. Worktree mode fingerprints also explicitly ignore a failed initial fingerprint (`accept.go:381,415`); those runs should not be marketed as fingerprint-verified.

### Trust and Codex wiring

The project trust caller supplies only the selected directory and its resolved spelling (`httpd/projects.go:214–218`); no ancestor/global wildcard is inserted. Worktree inheritance deliberately broadens the practical effect to worktrees of that repository. Codex's explicit non-trusted value is respected (`agenttrust.go:237–242`), and invalid TOML/syntactically invalid JSON is refused. A symlinked config is read through but replaced at the link pathname (`:269,277–302`): the referent is not overwritten, but the user's symlink arrangement is destroyed. Prefer a clear refusal for such configuration unless preserving the link is explicitly implemented.

The Codex script correctly forwards `"$@"`; executable/socket/job/token paths use POSIX single-quote escaping (`agentcfg.go:90–98,118–119,177–179`). The five hook events and shell/apply_patch matcher are wired through CLI arguments (`:185–218`), and hooked launches select on-request plus network access (`jobs/launch.go:595–608`). Hook failures are explicitly fail-open (`cmd/crowsnest/jobs.go:359–406`); PermissionRequest failure returns to the CLI's own approval UI, not an automatic allow. A no-token launch has no crowsnest hooks and uses approval=never (`launch.go:536–538,593–604`), whereas the engine catalog unconditionally advertises Stop/approvals/guard (`engines.go:143–152`). That catalog describes a normally configured hooked job, not actual per-job health. No external Codex runtime behavior was re-probed during this review.

### Settings, attention and CI fixes

The consolidated Settings sections map to the existing server setting groups and mutation endpoints (`SettingsView.tsx:123–175`, `SettingsSheet.tsx:69–84`); the old sheet names map to anchors in `settingsSections.ts:78–90`. No concrete setting that stopped saving was found. Voice anchors intentionally disappear when voice is disabled; generic voice links should explain that prerequisite. Multiple controls can display stale local drafts after an external setting change because SettingRow initializes state once (`SettingsSheet.tsx:65–66`); this is not evidence that saving itself fails.

RuleAnswerable is a shape test, not proof that an installed rule in this job's scope can currently answer: `approvals/policy.go:502–520` has no root/project/rule-set input. Low-risk approval items still explicitly represent stopped work and get the separate approval label (`attention/model.go:90–103`); they are not can_wait. Elevated calls stay blocking and dangerous calls dangerous. No additional concrete dangerous-as-quiet case was established.

The CI product fixes are in the code: job record/event updates share a transaction (`store/jobs.go:273–293`, used by the service save path), and the web invalidator subscribes to the completion of an initial fetch before invalidating it again (`web/src/lib/live.ts:115–129`). Their presence does not independently verify the full suite at this commit.

### Tailscale supervisor

The normal cancellation path waits for the watchdog, escalates to killing its group, and closes the pipe (`tsserve/run.go:268–280`); the watchdog handles parent-pipe EOF and escalates its direct child (`child.go:60–87`). Retry backoff is bounded (`run.go:74–85`). The wiring waits for publishing shutdown (`cmd/crowsnest/serve.go:401–402`). No real tailscale command was run.

Port choice is a snapshot (`service.go:318–335,362–375`), not a reservation against another publisher. The CLI/daemon must adjudicate a competing port claim; this code does not prove that behavior, and its fake cannot establish real-daemon safety. No `--bg`, reset or off mutation is used by the supervisor. Beyond R9-C13, no reproducible-from-code normal-stop leak or unbounded tight restart loop was established. Crash behavior when the watchdog itself dies, and real tailscaled session teardown, remain unverified external behaviors rather than asserted successes.

## Migrations 0019–0022 and backup/restore

The released local `v0.1.0` tag contains **0001–0009**, not 0018. The old nine migration files are unchanged against that tag. A real upgrade must execute 0010–0022, and inspection of that chain shows the tables/columns used by 0019 exist by 0016/0018. `store.Open` applies ordered numbered migrations; each SQL body and its version insertion are in the same transaction (`internal/store/store.go:136–172`). A migration error rolls back that migration and prevents startup; earlier successfully applied versions remain restartable. A newer-than-known database is refused (`:149–150`), so running the v0.1.0 binary over the upgraded file is not a downgrade path.

0019 adds holds/pending feedback/check metadata/fix-applied/land-approved state; its backfill uses existing cause/state and does not destructively rebuild jobs. It intentionally counts already-terminal historical fixes as applied (`0019_holds.sql:50–52`), so it does not repair an old stranded parent. 0020 adds an empty viewed-mark table. 0021 adds default worktree mode, empty fingerprints and disabled isolation, preserving old project behavior and not pretending old checks had fingerprints. 0022 adds an empty comment table/index. No static SQL ordering/default/nullability blocker was found for the released schema.

Backup restore checks schema compatibility before installation (`internal/backup/restore.go:238–243`) and opens/migrates the staged database before marking it paused (`:461–478`). It revokes approval rules, clears task approvals, withholds restored instruction layers and sets aside custom check/prepare commands (`:480–503`; `store/restored.go:61–98`). Whole-database backup carries the new tables and columns; comment/mark rows do not require a separate file format. The temporary isolated worktrees are not recoverable from those database rows, as R9-C7 explains.

**Limit:** No released database fixture, real customer database, backup round-trip or interruption was executed, as prohibited by the brief. Before tagging, the builder should run the normal gate and an upgrade/restore test starting from an actual v0.1.0-created database with representative live/terminal jobs, not merely a fresh current-schema database. Static compatibility is not a green upgrade test.

## Tag decision

**Not fit to tag. Fix R9-C1, R9-C2, R9-C3, R9-C4, R9-C5, R9-C6, R9-C7, R9-C8, R9-C9, R9-C10 and R9-C14 first.** These concern landing unaddressed work, concurrent writers/checks, lost or replayed instructions, false pinned-commit evidence, leaked check execution, lost agent configuration, malformed-config failure and misleading primary job state.

R9-C11 and R9-C12 can follow as status consistency fixes. R9-C13 can follow only with the current health limitation stated plainly; otherwise fix it before presenting publishing as monitored health. Aggregate diff snapshot limits, symlink-preserving trust policy, per-job capability health and stronger acknowledgement/send reservations remain the qualified follow-ups described above. Run the gate and the released-version upgrade/restore validation after the required fixes; this review supplies no green test result.
