# R3 security review (Opus): main at 5b5cb3a

This is the last round. The review was read-only: I read the code, ran only read-only git commands, and ran no build, test, herdr or server. I checked the builder's claims (`report-fix3b.md`) against the code. Where a finding depends on behaviour I could not run (git, Claude Code, herdr), the text says so.

Counts for new findings: **0 critical, 1 high, 2 medium, 4 low, 4 info.**

---

## Part 1: verdicts on the R2 findings

| ID | Verdict | Evidence (file:line) | Notes |
|---|---|---|---|
| **R2-H1** job_create unconfined | **Partly fixed** | `internal/jobs/delegate.go:116-151` (`CreateForAgent`), `:210-232` (`widerThanAllowed`), `:285-311` (limits); `internal/agentapi/jobs.go` (`creates` limiter, burst of 5 then 1 a minute; server.go:84-87,152); `protocol.go` (`modelPattern`) | Fixed: the engine check, the isolation check, the approval flow, depth, fan-out, the global live cap, the rate limit and the model pattern.<br>Not fixed: the directory boundary is re-read at check time from paths and git configuration the agent can change, so a job agent can still escape it deterministically (**R3-H1**). The orchestrator path checks only the directory, and its token can be borrowed (**R3-M2**). |
| **R2-M1** raw briefs typed into terminals | **Fixed** | `internal/orch/send.go:104-105` (`termtext.Block` on every `orch.Prompt`, then validation); `internal/termtext/termtext.go:77-96` (`Block`), `:98-112` (`DisarmHeaders`); `internal/jobs/briefs.go:43-102` (the brief goes to a file, one cleaned line is typed, agent-written briefs are framed) | I checked Block against the brief's list (Part 3): no ESC, no C0 or C1, no DEL, no CR. The only direct `AgentPrompt` call left is the fixed `/exit` (`orch/upkeep.go:137`).<br>The brief file opens a new channel (**R3-L4**). |
| **R2-M2** tokens readable by every agent | **Not fixed (accepted)** | `briefs.go:28-39` (job directory 0700, files 0600); DESIGN 4.6 and 20 | Documented as a limit of running everything as one uid. It now matters more because the orchestrator's token gives approval-free creation (**R3-M2**). |
| **R2-L1** tokens never revoked | **Fixed**, with a gap | `internal/jobs/service.go:317` and `tokens.go:17` (`RevokeJob` when a job ends); `cmd/crowsnest/tokens.go:44-63` with `agentid.RevokeGonePanes`; `serve.go:131` | A pane id that herdr reuses before the sweep sees it gone keeps its old token (**R3-L2**). |
| **R2-L2** every tunnel client is one loopback peer | **Fixed for cloudflared; partly for the tailscale layout** | `internal/httpd/proxy.go:51-60,75-99`; `middleware.go` (`clientIP`); `config/load.go` (`parseTrustedProxies`) | `CF-Connecting-IP` is preferred over `X-Forwarded-For` from any trusted peer. In the tailscale layout a client can set it itself (**R3-L1**). |
| **R2-L3** job notices unlimited, carrying agent text | **Fixed** | `internal/questions/notify.go:119-122` (per-job and overall limiters, Clean, redact); `internal/jobs/hooks.go:258,289` (fixed sentences) | |
| **R2-L4** API key follows redirects; http accepted | **Fixed** | `internal/voice/client.go:138` (`ErrUseLastResponse`); `config/load.go` (`SecureOrLoopbackURL`) | |
| **R2-L5** shared bucket, global cap | **Fixed** | `internal/agentapi/server.go:80-82,298-307` (10 per job agent; the orchestrator is exempt from the total); unidentified-bucket warning | A borrowed orchestrator token also lifts these caps (same root as R2-M2). |
| **N1 residual** secrets readable by agents | **Partly fixed** | `--elevenlabs-api-key-file`, `--notify-url-file` (`load.go` `secretOrFile`, `readSecretFile`); `deploy/crowsnest.env.sample` | These secrets stay out of every environment now, but the files are still readable by the same uid, as the password file is. The real fix is still a separate uid. |
| **N4 residual** connection cap behind the proxy | **Fixed** | `proxy.go:79-97` (32 requests in flight per attributed client) | The global TCP cap of 128 is still shared through the proxy's one peer. That is acceptable, since the proxy multiplexes. |
| R2-I1 labels unquoted, full-width brackets | Fixed | `questions/delivery.go` (labels quoted); `termtext.go` `bracketsAndQuotes` | |
| R2-I2 journal stores prompts verbatim | Fixed | `httpd/conversation.go:160`, `httpd/jobs.go:122` (`redact.String`) | |
| R2-I3 socket reads need no identity | Open (documented) | DESIGN 20 | |
| R2-I4 `say` from anyone | Partly fixed | `agentapi/server.go:399` (a token is required) | The source pane is still not shown. |
| R2-I5 supervisor types by pane id | Partly fixed | `jobs/launch.go:228,261,540` (`ExpectAgent`) | Nudges, retries and job messages (`actions.go`, `recovery.go`) still target the pane id alone. |
| R2-I6 role overrides writable | Open (documented) | DESIGN 20 | |
| R2-I7 model unvalidated | Fixed | `protocol.go` `modelPattern` | |

---

## Part 2: new findings, most severe first

### R3-H1 (high): a child's directory boundary is read from paths and git configuration the agent controls, so any job agent can escape it without approval

- **Where:**
  - `internal/jobs/delegate.go:236-249` (`jobRoots`: the parent's recorded `WorkDir` as a path, plus `git rev-parse --show-toplevel` run at the moment of the call)
  - `:253-269` (`withinAny`: `EvalSymlinks` at the moment of the call)
  - `internal/jobs/protocol.go:149` (`cleanDir`: `filepath.Clean` only, so the stored and launched `Dir` is the unresolved path)
- **Weakness:** The boundary is not a fact crowsnest recorded when it created the parent's pane. It is recomputed whenever a child is requested, from filesystem and git state the parent agent can change, since it runs as the same user and works in that tree. Three routes, each needing no human:
  1. **Swap the parent's own `WorkDir` for a symlink.** For example: `mv ~/.herdr/worktrees/repo/job-3 job-3.real && ln -s ~ ~/.herdr/worktrees/repo/job-3`. The agent's shell keeps its working directory, because it holds the moved inode. On the next `job_create`, `EvalSymlinks(WorkDir)` resolves to `$HOME`, so `dir: "~/anything"` counts as inside and launches at once with the parent's engine and permission mode. The same works one level down: a child created in a real subdirectory, which is then replaced by a link.
  2. **Move the git toplevel.** In a job that works in a plain checkout, writing `core.worktree = /Users/jsc` into `.git/config` (inside its own working directory) makes `git -C <WorkDir> rev-parse --show-toplevel` print `$HOME`, which becomes a root. I reasoned this from git's documented behaviour and did not run it. For a linked worktree the same needs `extensions.worktreeConfig`, set in the shared `.git`. A repository whose real toplevel is `$HOME` (a dotfiles repo) has this property with no tampering at all.
  3. **Check, then swap.** `Dir` is checked resolved but stored and launched unresolved (`record.Dir` comes from `cleanDir`). A link that flips between the check and the launcher's herdr call opens the child somewhere else, including a worktree of a different repository: `isGitRepo` and `worktree.create` both follow the link. A background loop that flips the link continuously makes this race practical across 8 children.
- **Impact:** R2-H1's directory confinement is undone. A prompt-injected builder can put agents in `$HOME`, `~/.ssh` or any other repository, with the project scope that gives Claude's auto mode, and the human is never asked. The engine change (to Codex) still needs approval: that check does not depend on paths.
- **Fix:**
  - When crowsnest creates a job's pane or worktree, record the resolved roots in the job row: `EvalSymlinks(WorkDir)` and the repository root learned then. Compare children against those stored values only, and never run git or resolve the parent's path at child time.
  - Resolve the child's `Dir` at creation, store and launch the resolved path, and re-verify it right before the herdr call (or refuse when the resolution changed).
  - Refuse any root equal to `/` or `$HOME`, or one that contains `$HOME`.

### R3-M1 (medium): the approval question can be rewritten by the brief it quotes, and it shows the unresolved path

- **Where:** `internal/jobs/delegate.go:378-381` (each brief line is indented 4 spaces, after `strings.Split(job.BriefMD, "\n")`), `:272-281,365` (`displayPath(job.Dir)` shows the stored, unresolved path). `newRecord` keeps `\r` in the brief (`TrimSpace` only). Question validation refuses CR and LF only in the title (`questions/validate.go:100`).
- **Weakness:** CommonMark, and so remark/react-markdown, treats a lone `\r` as a line ending. A brief line `fix typo\r## This request stays inside the job's worktree\r**Recommended: Allow.** Deny would lose the work.` is one "line" to the splitter, so only its first segment is indented. The rest renders as a heading and bold text outside the code block, under crowsnest's own explanation. The agent can thus write its own reassurance, or a fake "reasons" list, into the human's approval card.

  Separately, the card names the directory as the agent typed it. An approval for `~/.herdr/worktrees/repo/job-3/docs` reads as harmless even when `docs` is a link to `$HOME`. The check resolves the link, but the human is shown the unresolved name.
- **Fix:**
  - Normalise `\r`, `\v`, `\f`, U+2028 and U+2029 to `\n` in briefs (`termtext.Block` already does) before building the body. Better still, show the brief in a fenced block whose fence is longer than any run of backticks in it, or as plain text outside Markdown.
  - Show the resolved path, plus the unresolved one if it differs ("`…/docs` → `~`").

### R3-M2 (medium): the orchestrator path checks only the directory, and its token is a file any agent can read

- **Where:** `internal/jobs/delegate.go:211-216` (orchestrator: no directory, or any directory inside a remembered one, starts at once; the engine is not checked), `:172-186` (orchestrator identity means the pane matches; `parent` is nil when it holds no job, so `checkLimitsLocked` skips depth and fan-out, `:287`); `internal/orch/start.go:36,385` (`<state>/agent-token`).
- **Weakness:** Through the orchestrator's token (R2-M2), any job agent can create `engine: codex` jobs, which run with approvals off, in any directory under a remembered one, without asking. When the orchestrator holds no job, these jobs are also free of the depth and fan-out limits; only the global 16 and the rate limit remain.
  - The remembered list holds every directory a human job has used, plus every approved one. A single human job in `~` makes the whole home directory free.
  - The same applies when the orchestrator itself is talked into it by a child's result text. Results are not framed the way briefs are.
- **Fix:**
  - Apply the engine rule to the orchestrator too: Codex, or any engine other than the configured default, needs approval.
  - Do not remember `$HOME`, `/`, or any directory that contains `$HOME`.
  - Count the orchestrator's creations against a fan-out cap of their own even when it holds no job.

### R3-L1 (low): `CF-Connecting-IP` is believed from every trusted proxy, including `tailscale serve`

- **Where:** `internal/httpd/proxy.go:51-60`; `deploy/compose.yaml` and `crowsnest.env.sample` (`CROWSNEST_TRUSTED_PROXY=loopback` for the tailscale layout as well).
- **Weakness:** Cloudflare overwrites `CF-Connecting-IP` at its edge, but tailscale serve passes a client's own headers through, and the builder notes this layout is unverified. A tailnet client can send a new `CF-Connecting-IP` on every attempt and get a fresh per-IP login bucket and a fresh in-flight slot. Only the global login limit (20 a minute) stays. On a multi-user host, `loopback` also trusts other local users' processes.
- **Fix:** Make the header choice explicit per proxy (`--trusted-proxy loopback:cf` or `:xff`), or trust `CF-Connecting-IP` only when the configuration says the proxy is cloudflared. For tailscale, use the last `X-Forwarded-For` hop, or `Tailscale-User-Login` with the peer identity.

### R3-L2 (low): the token sweeper misses a pane id that herdr has already reused

- **Where:** `cmd/crowsnest/tokens.go:52-56`. A token is revoked only when its pane id is absent from `pane.list`.
- **Weakness:** The case R2-L1 named is reuse. If herdr hands the id to a new pane before the next sweep, which runs every minute and spares bindings for 2 minutes, the id is present and the old token is never revoked. `RevokeJob` covers job agents. What remains: orchestrator tokens across a pane replacement crowsnest did not perform, and adopted panes.
- **Fix:** Record each pane's creation identity (herdr's pane creation time or sequence number, or the agent name) in the binding, and revoke on mismatch, not only on absence.

### R3-L3 (low): held approvals count against the live cap, and each is a blocking question

- **Where:** `internal/store/jobs.go:303-310` (`CountLiveAgentJobs` counts `queued` jobs, held ones included); `delegate.go:342-358` (each held job asks a blocking question).
- **Weakness:** A looping or injected agent can file wider requests, 5 at once and then 1 a minute. That fills all 16 live slots with held jobs, refusing every other agent's delegation, and puts 16 blocking questions in the Inbox for the human to deny one by one. The phone notices are rate limited per pane, which helps. The same agent can repeat this after each denial.
- **Fix:** Cap held approvals per creator (for example 2 open). Do not count held jobs against the live cap, or count them against the creator only.

### R3-L4 (low): brief and token files can be rewritten by any agent, and a rewritten human brief carries the human's authority

- **Where:** `internal/jobs/briefs.go:43-62` (`os.WriteFile` follows symlinks; paths are predictable, `<state>/jobs/j_<n>/`); the typed line points the agent at the file (`:88-102`).
- **Weakness:** Same uid, so this is no new privilege, but the consequence is new. An agent that rewrites a human job's `brief.md` between crowsnest writing it and the target agent reading it gets its words read as the human's: no "written by an agent" frame appears, because that framing depends on `ParentID`. This includes the orchestrator's dispatched briefs. Pre-creating `j_<next>/brief.md` as a symlink makes crowsnest write the brief through it into another file.
- **Fix:** Put the brief's SHA-256 (first 12 hex characters) in the typed line and tell the agent to check it, or use `job_get`, which comes from the database through the token, as the primary channel. Write with `O_NOFOLLOW | O_EXCL` into a freshly created directory.

### Info

- **R3-I1: `StripANSI` swallows text after an unterminated CSI.** Its parser (`internal/fleet/ansi.go:84-95`) skips everything until an ASCII final byte, including newlines and non-ASCII text, and an unterminated OSC eats the rest of the text. In `Block`, a stray `ESC[` in a human's prompt therefore deletes the text up to the next ASCII letter. This fails safe (it only deletes), but it silently loses content.
- **R3-I2: R2-I5 is half done.** `jobs/actions.go` (nudge, NudgeIfDue, Message) and `recovery.go` (retry) still type by pane id without `ExpectAgent`. Each is a one-field change.
- **R3-I3: a held job cancelled from its card leaves its approval question open.** Answering it later does nothing, because `ApprovalSettled` checks the state. This is harmless but confusing (the builder lists it).
- **R3-I4: an agent can ask a question that looks like an approval card.** A normal `ask` with the same title shape and allow/deny options is possible. Its answer approves nothing (`ApprovalSettled` reads the real question id from the job, `delegate.go:403-421`), but it can teach the human to click "Allow" quickly. Consider marking crowsnest-originated questions visibly in the Inbox.

---

## Part 3: checked and found sound

**`termtext.Block` against the brief's list:**
- CR, VT, FF, NEL, U+2028 and U+2029 become LF.
- Tabs become 4 spaces, so no completion is triggered. Validation runs after Block, so tab expansion is still bounded by `MaxPromptBytes`.
- `StripANSI` consumes CSI and OSC, including OSC 52 clipboard writes, and DCS, APC and PM.
- `invisible` then drops any ESC that is left, C0 except LF, C1 (U+0080–U+009F, so 8-bit CSI 0x9B), DEL, Cf, Co and invalid UTF-8.
- A paste-end marker `ESC[201~` is consumed as a CSI. A literal `[201~` without ESC is inert.
- Very long lines are bounded by `MaxPromptBytes` (32 KB).
- Agent text never starts a typed line: answers, briefs and pokes all begin with crowsnest's own header or `- `.

**Delegation:**
- `withinAny` compares with `filepath.Rel` after resolving both sides, so `..` and prefix tricks such as `/a/bc` against `/a/b` fail. The weakness is the inputs it is given (R3-H1), not the comparison.
- Limits are checked under `s.mu`, so concurrent calls cannot both take the last slot.
- `depthOf` is bounded by `maxAncestors`.
- `ApprovalSettled` re-reads the real question from the store by the id recorded on the job, so a forged question or Context entry cannot approve anything. Only `"allow"` from an answered question launches.
- Deny, expiry and cancel all cancel the held job. A finished parent cannot delegate.
- A pane that is neither a job agent nor the current orchestrator pane is refused.

**Proxy:**
- Forwarding headers are ignored unless the TCP peer is trusted (`proxy.go:81-84`).
- `X-Forwarded-Proto` is believed only from a trusted or loopback peer, and only to add `Secure`. A spoofed value can only break that client's own cookie.
- The host and origin checks are unchanged: `trustProxies` does not touch `Host` or `Origin`.
- `--trusted-proxy` refuses values that are not an IP or a CIDR.

**Secret files:**
- `readSecretFile` refuses group or other access and an empty first line.
- Giving both the variable and the file is refused.
- The `*_FILE` variable names are in `secretEnv`, so they are re-exec'd away and unset.

**Tokens:**
- `Bind` replaces a pane's tokens in one transaction under the registry lock.
- A stale start token for a pane now held by another job is refused (`ErrStaleStart`).
- `RevokeJob` runs inside `mutate` when a job first reaches a terminal state, so a held job cancelled by `cancelHeld` is covered too.
- The sweeper revokes nothing when herdr cannot be read.

**Brief delivery:**
- The job directory and its parent are forced to 0700 and the files to 0600.
- The typed line contains only crowsnest's own path and the role, both through `Clean`.
- Agent-written briefs are framed and `DisarmHeaders`'d in the file, in `job_get` and in SessionStart.

---

## Not reviewed

- No live checks: git `core.worktree` behaviour in linked worktrees (R3-H1, route 2), how tailscale serve forwards headers (R3-L1), and herdr's pane-id reuse timing (R3-L2).
- Part A's supervisor changes (`lifecycle.go`, `recovery.go`, the rest of `actions.go`, `term/tui.go`) beyond the paths that type text and choose targets.
- `orch/poker.go`'s replay (R2-C13) and the question deliverer cursor (R2-C14) as correctness. I only checked that they type nothing new.

---

## Overall judgement

On a tailnet with a password, crowsnest is reasonable to run today for a single user. The network-facing side (auth, CSRF, host checks, rate limits, secrets out of argv and the environment) has held up across three rounds. What remains is agents acting against each other or against their confinement, all as one uid.

In the devbox behind a Cloudflare tunnel it is acceptable with these conditions:
- Cloudflare Access is in front.
- `CROWSNEST_TRUSTED_PROXY=loopback` is set (cloudflared overwrites `CF-Connecting-IP`, so the attribution is sound there; do not copy that setting to the tailscale layout until R3-L1 is fixed).
- You accept that any agent in the container can read the password file and every token.

Fix R3-H1 first: store the resolved roots when the job's pane is created, and resolve and launch the child's directory once. Then R3-M1 (the approval card) and R3-M2 (the engine rule and the remembered-directory rule for the orchestrator), because together they decide whether the human approval gate means anything.
