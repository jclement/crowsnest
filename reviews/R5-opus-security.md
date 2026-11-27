# R5 security review (Opus): main at 0e68362

Scope: everything added since R3 (8d93449, near v0.1.0), against the nine areas of the brief. The review was read-only: I read code and ran only read-only git. No build, test, herdr or server was run. I read setup, owner auth, the opener handoff, approvals, action tokens, the risk classifier, push notices, delegation approval questions and the land-request flow myself. Four read-only explorer agents read attachments and push, backup, update and diagnostics, projects, schedules and landing, and Tailscale sign-in, tokens and release. I re-read the code behind every finding of theirs rated medium or higher before keeping it. Lower findings from them are marked "(agent-read)" where I did not re-read the code.

Counts: **0 critical, 3 high, 4 medium, 7 low, 5 info.**

The pattern this round: the network side is still sound. The new weaknesses are all ways an agent, or one tap on a phone, gets past a human gate that the design says only a deliberate human decision can pass: landing, delegation, and job creation.

---

## Findings, most severe first

### R5-S1 (high): an agent can approve a land with its own question, because `land_for` is an unreserved context key and the settle handler does not check the question id

- **Where:**
  - `cmd/crowsnest/questions.go:46-48`: any settled question whose `Context["land_for"]` names a job calls `LandRequestSettled`.
  - `internal/jobs/land.go:633-655` (`LandRequestSettled`): acts on the `question` passed in. It never compares `question.ID` with the job's stored `LandQuestionID` (`land.go:613`).
  - `internal/agentapi/mcp_ask.go:66` passes the agent's `Context` through. `internal/questions/validate.go` `checkContext` checks only lengths, so `land_for`, `land_action` and `approval_for` are all settable by an agent.
  - `internal/jobs/land.go:581-616` (`RequestLand`) and `internal/agentapi/landing.go:65-85`: any caller with a pane token can file a land request for **any** finished job id. Nothing ties the job to the caller.
- **Weakness:** the gate is "only the human's Allow on the land question runs it". In fact any question with the right context key, answered with an option keyed `allow`, runs it.
- **Exploit:** a prompt-injected agent with a token (a lead, the orchestrator, another job's agent, or any same-uid process that borrowed one, 4.6) does this:
  1. It picks a finished job `j_5` that has a worktree and calls `job_land` with `merge`. (`pr` pushes to the remote and opens a PR. `discard` deletes the worktree and branch.) The honest land question is filed and sits in the Inbox.
  2. It calls `ask` with title "Tests pass. Continue with the docs?", options `allow: "Yes, continue"` and `deny: "No"`, `recommend: allow`, `context: {"land_for": "j_5"}`.
  3. The human taps "Yes". Crowsnest merges j_5 into its base, or pushes it, or discards it. It records this as "you, as an agent asked", and the honest question's link is cleared.

  This needs one tap of a deceptive question, and that tap can be a notification button (R5-S2). The merge goes into any unprotected base branch. A push publishes agent-chosen commits to the remote.
- **Fix:**
  - In `LandRequestSettled`, act only when `question.ID == q_<LandQuestionID>`, as `ApprovalSettled` already does.
  - Reserve crowsnest's context keys (`land_for`, `land_action`, `approval_for`, `requested_by`, and any future key) in `checkContext` for agent-originated asks. A simple rule: refuse keys a caller with a token sets that match a reserved list.
  - Allow `RequestLand` only for the caller's own job, its children, or a lead's project jobs.
  - The same forged key also feeds `relinkApproval` (`delegate.go:640-660`) in its crash window: it adopts the first question on the job carrying `approval_for`. Reserving the key closes that too.

### R5-S2 (high): delegation approvals and land requests can be allowed from a lock-screen notification button

- **Where:**
  - `internal/push/watcher.go:159-171`: every pushed question of kind `confirm` or `choice` gets its first two options as buttons plus an action token. No exception is made for crowsnest's own approval questions.
  - `internal/push/notices.go:178-186` (`QuestionActions`).
  - The approval questions themselves:
    - `internal/jobs/delegate.go:560-566`: kind `choice`, urgency `blocking`, options "Allow: start it as asked" and "Deny". This is the R2-H1/R3 gate for a wider job: another directory, Codex with approvals off, no isolation.
    - `internal/jobs/land.go:602-606`: kind `confirm`, urgency `normal`, options "Allow: merge it" and "Deny".
  - `/push/action` (`internal/httpd/approvals.go:181-194`) answers the question with the button's key, so `ApprovalSettled` or `LandRequestSettled` then acts.
- **Weakness:** DESIGN 4.16 and 8.7 keep notification actions to low-risk approvals, because anyone who sees the payload can use the token, and a lock-screen button shows no detail. Elevated and dangerous tool calls are refused from a notification (`approvals.go:354`). Yet a job wider than allowed, a merge into the base, a push to the remote, and a worktree discard are each one tap from the lock screen. The human sees only a title, never the brief, the diff or the directory reasoning that the Inbox card was built to show (R3-M1). Combined with R5-S1, the button can even sit on a question the agent wrote.
- **Fix:** give no action token to any question crowsnest raises as a gate. Mark gate questions with a field crowsnest sets and agents cannot (see R5-S1), and skip `QuestionActions` for them. Consider also refusing notification answers for agent questions whose option keys are `allow`/`approve`, since nothing stops an agent from shaping one (R3-I4).

### R5-S3 (high): scheduled-task files start jobs with no approval, and the orchestrator can write them with its ordinary file tools

- **Where:**
  - `cmd/crowsnest/upkeep.go:56-75`: task sources are `<--dir>/tasks` (the orchestrator's working directory) and `~/.config/crowsnest/tasks`. `scheduleJobs.CreateJob` calls `jobs.Service.Create(..., "")`, which is the human origin.
  - `internal/schedule/service.go:450-466` and the tick: an `as: job` task runs its `dir:`, `role:` and body as a job. Files are re-read every tick.
- **Weakness:** the empty origin skips `widerThanAllowed` and the delegation question entirely. R3-H1 and R3-M2 confined what an agent can start *through `job_create`*. A task file is a second door to the same place, with no confinement and no question, and it repeats on a schedule and survives restarts.
- **Exploit:** an injected orchestrator, whose working directory contains `tasks/`, uses its normal Write tool (no Bash needed, so no permission prompt in its directory) to create `tasks/sync.md` with `every: 15m`, `as: job`, `dir: ~` and a hostile brief. Every 15 minutes a full job agent starts in `$HOME`, with the default engine (Codex when the human chose it as default). Any job agent can do the same through the orchestrator's directory, or `~/.config/crowsnest/tasks`, with one file write. Pause stops it, but nobody is asked.
- **Fix:**
  - A file-origin `as: job` task must be accepted by the human once per content hash: a gate question, without notification buttons (R5-S2). Or it is poke-only, and jobs on a schedule must be app schedules made in the browser.
  - At minimum, run file-task jobs through the same `widerThanAllowed` check as the orchestrator (`safeRoot`, remembered roots, Claude only).
  - Better still, move the orchestrator's `tasks/` out of its own working directory.

### R5-S4 (medium): Land merges the branch as it is at click time, not the commit the human reviewed

- **Where:**
  - `internal/landing/land.go:56-62`: `git merge --no-ff|--ff-only <branch>`.
  - `internal/httpd/landing.go:112-125`: the body is `{action, confirm: true}` with no expected head.
  - `internal/jobs/land.go:183-203`: the confirmation names branches and directories, not a SHA.
  - `land.go:650`: an agent-requested land runs `Land` whenever the human answers, possibly hours later.
- **Weakness:** the job's agent is still alive until teardown, which happens after landing. It can commit to `job/<id>-…` after the human opened the Review tab, or after it filed its land request. What is merged or pushed is then not what was shown.
- **Fix:**
  - Carry `produced.Head` in the Review payload and the land question. Send `expected_head` with the confirmation, and refuse on mismatch.
  - Merge or push the SHA, not the ref.
  - Stop the agent (or freeze its branch) before enabling Merge.

### R5-S5 (medium): crowsnest's own git calls run repository-controlled config and hooks, with no protective flags, and this is not acknowledged anywhere

- **Where:** `internal/landing/git.go:56-82` (`gitTimed` and `gitEnv` set only prompt, pager, locale and editor variables). The calls that trigger it:
  - `git status` in `readStatus` (`git.go:344`), run on every Review view and every sweep (`jobs/land.go:561`);
  - `git merge`, `branch -d` and `push` in `landing/land.go`;
  - `git diff` with `--no-ext-diff` but not `--no-textconv` (`git.go:375`);
  - `gh` with `cmd.Dir` in the worktree (`land.go:280`).

  A grep finds no `core.hooksPath` or `core.fsmonitor` in code or DESIGN.
- **Weakness:** `core.fsmonitor`, `diff.<x>.textconv` with a committed `.gitattributes`, merge drivers, `post-merge`, `pre-merge-commit`, `commit-msg` and `reference-transaction` hooks, and an `ext::` remote all run commands chosen by whoever can write the repository's git config or hooks. Those commands run as the user, from crowsnest, when the human opens Review or clicks Merge. They run outside Claude's permission model and outside any approval.
- **Scope, honestly:** for a linked worktree, the config and hooks live in the main checkout's `.git`, which is outside the agent's own directory. Writing there is a Claude permission prompt for an edit-only agent ("writes outside the job's directory" is elevated). An agent with Bash in auto mode can already run code, so for it this adds no privilege. It turns "one approved write outside the worktree" into "code execution on the human's next click", and it makes the human's routine click the trigger.
- **Fix:**
  - Run every git call as `git -c core.hooksPath=/dev/null -c core.fsmonitor=false -c core.untrackedCache=false -c diff.external= -c protocol.ext.allow=never …`.
  - Pass `--no-textconv --no-ext-diff` to `diff`, `log` and `show`, and put `--` before remotes and paths.
  - Document that hooks are disabled on Land, or, if wanted, run them deliberately and say so on the confirmation.

### R5-S6 (medium): Sign in with Tailscale trusts any loopback peer, so a second local proxy (cloudflared) differs from `tailscale serve` only by the Host header

- **Where:** `internal/httpd/ops.go:371-381`:
  - `peerAddr(r).IsLoopback()`: the raw TCP peer, which `--trusted-proxy` does not rewrite. That is good.
  - the `Tailscale-User-Login` header;
  - `gate.ServesHost(r.Host)`.

  DESIGN 4.20 discusses local processes but not other local proxies.
- **Weakness:** in the devbox and tunnel layouts, cloudflared also connects from 127.0.0.1, and it passes client headers through. An internet client's forged `Tailscale-User-Login: <owner login>` is then stopped only because cloudflared normally sends the public hostname as `Host`. If anything on the path forwards a client-chosen Host, the client is signed in as the owner, which is a shell. That could be a cloudflared `httpHostHeader` or wildcard ingress, a Caddy or nginx with Host passthrough, or an edge that routes a mismatched Host. Using the feature also needs `--tailscale-auth` and a non-empty allowlist. In the compose layout it fails closed, because there is no `tailscale` CLI (agent-read). The risk is a host install that also runs a tunnel.
- **Fix:**
  - Accept the header only when the last `X-Forwarded-For` hop that serve adds is a tailnet address (100.64.0.0/10 or fd7a:115c:a1e0::/48).
  - Refuse it when `CF-Connecting-IP`, `Cf-Ray`, `Cdn-Loop` or `Forwarded` is present.
  - Document that `--tailscale-auth` must not be combined with another loopback proxy unless those checks are in.

### R5-S7 (medium): "low risk" means "matched no denylist rule", and low-risk calls are allowed from the lock screen on a 160-character summary

- **Where:**
  - `internal/risk/risk.go:252` (`Result{Level: Low}` unless a rule hits) and the builtins at `:72-91`;
  - `internal/push/notices.go:154-167` (low gives Allow and Deny buttons);
  - `internal/approvals/approvals.go:590-607` (`Summary`: first matching field, whitespace collapsed, cut to 160 runes, bidi controls kept).
- **Weakness:** none of these hit a rule, so all are low and one tap from a notification:
  - `python3 -c "import shutil;shutil.rmtree('/Users/x/proj')"`
  - `find . -delete`
  - `base64 -d <<<… | sh`
  - `perl -e 'unlink glob "~/*"'`
  - `git -c alias.p=push p`
  - a long benign prefix with the payload past character 160

  U+202E in a command can also reorder what the notification and the card show: questions refuse bidi controls in options (`validate.go:151`), approvals do not. The browser card shows the full input in a scrollable block, but the notification is the one surface designed for allowing without opening anything.
- **Fix:**
  - Invert the default for one-tap answers: give notification buttons only to an allowlist of read-only shapes (Read, Glob, Grep, `git status|diff|log`, `go test`, …). Everything else opens the card.
  - Strip or flag bidi and other format characters in `Summary` and on the card (`termtext.HasBidiControl` exists).
  - Show "…(N more characters)" instead of a bare ellipsis.

### R5-S8 (low-medium): pre-migration database copies are not scrubbed

- **Where:** `internal/backup/upgrade.go:74-79`: `VacuumFileInto` then `Chmod 0600`. There is no `ScrubSnapshot` call (it is called only from `backup.go:140`). Three copies are kept.
- **Weakness:** these copies carry everything 4.17 removes from backups:
  - `machines.token` in clear;
  - the VAPID private key;
  - `settings.notify-url` (an ntfy topic means posting to the owner's phone);
  - session, agent and API token hashes.

  They sit in `<data>/backups/` beside the scrubbed backups that users are told are safe to copy elsewhere. `MkdirAll(…, 0700)` does not tighten an existing directory, and the file exists at the umask mode until the chmod (agent-read, consistent with the code above).
- **Fix:** scrub the copy, which loses nothing a downgrade can use. Create it in a 0700 staging directory and rename it in. Or name it so nobody mistakes it for a backup.

### R5-S9 (low): a restore installs whatever a crafted archive says, with no total size cap

- **Where:** `internal/backup/restore.go:117-150,288,326` (agent-read; the manifest's digests are self-attested). `maxEntryBytes = 8<<30` per entry, and nothing caps the total.
- **Weakness:** a backup from a shared drive or chat can carry its own `auth.owner` hash, `risk.yaml` (disable rules, mark everything low; this feeds R5-S7) and role or task files (R5-S3). `--force` or a fresh data directory takes them all. A gzip bomb fills the disk during staging.
- **Fix:**
  - On restore, print the owner username, the `risk.yaml` diff, and the role and task files that would change, and ask.
  - Cap total bytes and entry count.
  - Open the restored database with SQLite defensive mode and check its schema against the known one.

### R5-S10 (low): the release chain has three gaps

These are agent-read; I checked the SAN pattern in `internal/update/sigstore.go:18-35`.

- The ghcr image is pushed without cosign or provenance (`.github/workflows/release.yml`, the image job), so `docker compose` users verify nothing.
- `crowsnest update` accepts any certificate for `release.yml@refs/tags/v*`. Anyone who can push a tag (with a modified `release.yml`) mints a valid signature, and nothing binds the certificate's tag to the release being installed.
- The `check` job runs `mise run setup` with npm install scripts on, and the signed binaries embed that `web/dist`. The Dockerfile uses `--ignore-scripts`; this job does not.
- **Fix:**
  - Sign the image by digest, with provenance.
  - Protect `v*` tags and put the release job behind a GitHub Environment with a required reviewer.
  - Compare the certificate's ref with the tag being installed.
  - Build `web/dist` with `--ignore-scripts`.

### R5-S11 (low): the setup handoff id is on the opener's argv, and loopback is the only other check

- **Where:** `cmd/crowsnest/setup.go:181-183`; `internal/httpd/setup.go:48-57`; `internal/opener/handoff.go`.
- **Weakness:** the id is single-use, 128-bit and lasts 2 minutes. That is good. But `open` or `xdg-open` (and a cold-started browser, whose argv keeps the URL) shows it in `ps` to every local user. Redeeming it returns `/setup#token=<setup token>`. On a shared Linux machine, another user who polls `ps` and races the browser gets the setup token, completes setup, and owns a shell as this user. DESIGN 3.3 says argv is visible; the race is not discussed. This is a first run only, with `--open`, on a multi-user host.
- **Fix:** have the server set a one-time cookie on the redemption that is bound to a nonce only the opener's browser can present. Or refuse redemption once the page has loaded once. Or simply document "multi-user hosts: `--open never`".

### R5-S12 (low): thumbnailing and attachment quota

These are agent-read. I checked `thumb.go:44-53` for the order of `DecodeConfig` before `Decode`.

- The 40-megapixel cap allows about 320 MB for a 16-bit PNG.
- Thumbnailing runs under the service mutex (`attach.go:243-262`), so one image stalls every upload.
- An agent can attach 10 files of 25 MB per `ask` or `done` with no rate limit. Those files are held by a live owner, cannot be evicted, and fill the 1 GB quota (507 for the human).
- **Fix:** a cap of about 16 MP; thumbnail outside the lock with a time limit; a per-token attachment byte rate.

### R5-S13 (low): small guard gaps in push and machines

These are agent-read; each is defence in depth.

- `web/public/sw.js` `safePath` accepts `"/\t/evil.example"`, which the URL parser turns into an off-origin URL. The server filter (`notices.go:104`) also lets `\t` through. It is not exploitable today, because every notice URL is a fixed template. Fix: parse with `new URL(u, location.origin)` and require the same origin.
- `push/endpoint.go` `publicAddress` misses 198.18/15, 192.0.0/24, 240/4, 6to4 and Teredo.
- `machines` accepts `https://` to private IPs and sends the `cnk_` token there.

### R5-S14 (low): role files on disk silently outrank the app's text

- **Where:** `internal/jobs/roles.go:54-80`; `internal/instructions/instructions.go:8-12` (agent-read).
- **Weakness:** this is R2-I6 again, now with an app editor beside it. An agent that writes `~/.config/crowsnest/roles/builder.md` (one approved out-of-tree write) injects text into every later builder. The UI shows it as "from file", with nothing that says it changed.
- **Fix:** show a "changed on disk since you last looked" badge (content hash), or apply the hash acceptance of R5-S3 to role files.

### Info

- **R5-I1:** a login from flags, the environment or the config file silently replaces the stored owner (`cmd/crowsnest/setup.go:40-56`). Anything that can write the config file or the service environment can swap the login at the next restart. That is the same uid, so it is no new privilege. Only a log line says so. Consider showing it on the System page.
- **R5-I2:** `parsePHC` allows stored hashes up to 1 GiB and 16 passes (`internal/auth/owner.go:167`). Only someone who can write the database can set that; 256 MiB would be a tighter bound.
- **R5-I3:** "Copy diagnostics" keeps the MagicDNS name (`connected as <node>.<tailnet>.ts.net`) and paths outside `$HOME` (agent-read).
- **R5-I4:** a session holder can subscribe a push endpoint they control and keep receiving notices, with their action tokens, after the session is gone. Journal "new push device" (agent-read).
- **R5-I5:** the risk classifier's custom rules are checked first and win outright (`risk.go:210-213`), so a `risk.yaml` rule can mark `rm -rf` low. It is a file under the user's config, so this is the same uid, but it matters for R5-S9.

---

## Verdicts on R3

| ID | Verdict | Evidence |
|---|---|---|
| R3-H1 child boundary from agent-controlled paths | **Fixed** | Roots recorded resolved at creation (`confinementRoot`, `delegate.go:~300`). `safeRoot` refuses `/`, `$HOME` and its ancestors, as written and resolved (`:313-327`). Leads are confined to the project's recorded `RootDir` (`:260-268`). Agent-read, spot-checked. |
| R3-M1 approval card rewritable by the brief, unresolved path | **Fixed** | `approvalQuestion` (`delegate.go:530-567`): `termtext.Block`, a fence longer than any backtick run, the resolved path plus "the agent asked for …". Read myself. New gap: the same question gets a notification button (R5-S2). |
| R3-M2 orchestrator path: engine and broad remembered dirs | **Fixed**, residual | Claude only (`:271-275`); `safeRoots(RememberedDirs)`. Residual: a remembered `~/Work` is still a wide free zone. Bypassed entirely by task files (R5-S3). |
| R3-L1 `CF-Connecting-IP` from any trusted proxy | **Fixed per DESIGN 4.2** (`--trusted-proxy-header`, XFF by default) | proxy.go not re-read this round |
| R3-L2 pane-id reuse in the sweeper | Not re-checked | |
| R3-L3 held approvals fill the live cap | Not re-checked | |
| R3-L4 brief and token files rewritable | **Mitigated per DESIGN 4.6** (exclusive, no-follow, 0400 creation) | Not re-read. Same-uid residual, as stated. |
| R3-I3, I4 | I4 still open, and it now has teeth through R5-S1 and R5-S2 | |

## Checked and found sound

- **First-run setup:**
  - token: 32 bytes, SHA-256 only in memory, constant-time compare (`internal/setup/setup.go:140-149`);
  - fields are validated before an attempt is counted;
  - one global limiter with rotation after 10 wrong tokens;
  - mutex plus `InsertKVIfAbsent`, so a second setup or a racing loser gets 410 (`setup.go:132-158`, `auth.go` `CompleteSetup`);
  - locked server: health, setup, handoff and the SPA only; every other `/api/` path is 403 before any upgrade (`httpd/setup.go:34-43`);
  - the token never reaches a log line except the announcement, and it travels only in the fragment and the POST body;
  - argon2id 64 MiB/t=3/p=1, serialised by `hashSlot`, with a dummy hash when there is no owner;
  - password change counted by the limiter; `reset-setup` leaves no valid session (`ResetSetup`, and `currentDigest` returns `ErrNoSession` with no owner).
- **Approvals:**
  - the hook output never carries `updatedPermissions` or `updatedInput` (`approvals.go:569-587`);
  - expiry and supersession print nothing;
  - dangerous allows need `confirm` server-side (`:357`);
  - notification answers are refused for elevated and dangerous calls (`:354`);
  - action tokens: 256-bit, hashed, single-use, spent on a wrong action, item- and action-scoped, 15 minutes (`internal/actions/actions.go:54-88`);
  - `/push/action` keeps the host, Origin and `X-Crowsnest` checks;
  - the approved input is the one Claude runs, because the hook relays Claude's own call and the decision carries no input. A forged request through a borrowed token wakes only the forger's own poll.
- **Tailscale and tokens** (agent-read):
  - allow-list edits are refused to Tailscale-signed sessions;
  - sessions are re-checked per request and on SSE and terminal heartbeats;
  - `requireAuthOrToken` guards only `GET /summary`, which leaks counts and the version;
  - tokens are hashed, revocation is checked per request, and the machines fetcher uses no redirects, no proxy and 64 KiB.
- **Attachments** (agent-read):
  - session-only fetch;
  - type sniffed from bytes, an inline allowlist, `nosniff`, the sandbox CSP;
  - `mime.FormatMediaType` for the filename;
  - `ServeContent` ranges with no second sniff;
  - agent reads through `os.Root` with token-scoped roots;
  - inbox copies with `O_EXCL|O_NOFOLLOW`;
  - the Read allow rule is scoped to `<state>/jobs/j_N/attachments/**`.
- **Push and service worker** (agent-read):
  - the SSRF guard sits in `Dialer.Control` on the resolved IP, after `Unmap`;
  - no redirects or proxy;
  - payloads are RFC 8291 encrypted;
  - the service worker has no fetch handler and no cache;
  - the CSP is unchanged since v0.1.0.
- **Backup** (agent-read):
  - regular tar entries only, names from the manifest with anchored patterns, digests checked before anything is placed;
  - no restore over HTTP;
  - `GET /backup` is session-only, files are 0600, and the scrub list is as in 4.17;
  - retention deletes only `^[0-9a-f]{64}$` blobs and `^j_[0-9]+$` real directories.
- **Update:** signature, then checksum, then atomic rename; the admin endpoint is session, Origin and `X-Crowsnest`, and refused for Docker, dev and Homebrew builds; there is no version or URL parameter.
- **Onboarding shell:** only a command the current doctor report offers (`httpd/system.go:195-235`).
- **Landing arguments:** branch names are crowsnest-generated (`job/<id>-<slug>`), titles and messages are values after their flags, the PR body goes through a file, push refspecs are explicit, and protected branches are checked for merge and push.
- **Instruction layers, projects, settings and schedules in the app:** web-only behind `requireAuth`. The agent API has no verb that writes them. Auth, bind, hosts, proxy, Tailscale auth and delegation limits are not in the editable settings table. Markdown renders with `skipHtml`, and mermaid is strict.
- **Release workflow** (agent-read): actions pinned by SHA, least-privilege permissions, no `pull_request_target`, no untrusted `${{ }}` in `run:`, keyless cosign on checksums.

## Not reviewed

- No live checks: Claude Code's handling of a hook allow while its input changes; cloudflared's and Cloudflare's behaviour on a mismatched Host (R5-S6); git hook and fsmonitor behaviour in linked worktrees (R5-S5, reasoned from git's documentation).
- R3-L2 and L3; proxy.go for R3-L1.
- `internal/attention`, `digest`, `presence`, `spend`, `throttle`, `retention`'s planner logic, and the herdr lazy client, beyond what the areas above touch.
- The web UI beyond the approval card, the Markdown renderer and the service worker. In particular, how a remote machine's summary JSON is rendered (agent noted, not checked).
- The guard beyond its stated scope (a seatbelt, bypassable by any interpreter, as it says).

## Overall judgement

On a tailnet with a password, main is safe to run today against network attackers. Setup, login, CSRF and host checks, the attachment serving and the push SSRF guard all held up. What is broken is the meaning of the human gates against a prompt-injected agent: R5-S1, R5-S2 and R5-S3 each let an agent land work, widen a job or start unconfined jobs with no deliberate human decision.

In the devbox behind a tunnel, it is acceptable with R3's conditions (Cloudflare Access in front; accept that agents read every secret in the container). Leave `--tailscale-auth` off on any host that also runs a tunnel until R5-S6 is fixed.

Before the next release tag, fix R5-S1 (check the question id, reserve context keys), R5-S2 (no notification buttons on gate questions) and R5-S3 (task-file jobs gated or confined). R5-S4 (pin the reviewed SHA) and R5-S5 (protective git flags) should follow in the same release, because they are a few lines each.
