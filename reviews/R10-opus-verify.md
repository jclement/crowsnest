# R10 verification (Opus): the R9 fixes at dcc6fdd, before the v0.2.0 tag

**Scope:**
- R9-S1..S13 (`reviews/R9-opus-security.md`) and R9-C1..C14 (`reviews/R9-codex.md`), both reviewed at 2a13f09;
- the fix reports `report-fix9a.md` and `report-fix9b.md`, treated as claims;
- main at dcc6fdd.

**How it was done:**
- Read-only. I read code and ran read-only git, plus `strings` on the installed codex 0.167 binary. Nothing was built or run: no tests, servers, herdr or tailscale.
- **I read these myself:** the Codex launch arguments (jobs and orchestrator), the `.codex/` gate, `agenttrust` (lock, compare-before-rename, Inspect), the project trust and codex-config routes, `markRestored` and the restore set-aside list, the snapshot scrub, the `--tailscale-auth` refusal paths, `changes.go` (branch_op, changes_state, takeLand), `LandWith`, the isolated check and `VerifyCheckout`, and the typing paths through the continuation gate.
- **Two read-only explorer agents** covered R9-C2..C5, C7, C10..C14 and R9-S6, S7, S9..S13.
- I re-read the code behind each of their findings that I kept at medium or above: N1, N2 and the `envChdir` and `.git` rows. Anything else from them is marked "(agent-read)".

**Counts:**
- **R9 verdicts:** 22 fixed (S5 as scoped), 5 partly (S1, S2, S4, S9, S10), 0 not fixed.
- **New since R9:** 1 high, 3 medium, 11 low.
- **Must-fix before the tag:** 5 items.

---

## 1. Verdicts

### Security (R9-S)

| ID | Verdict | Evidence |
|---|---|---|
| S1 Codex network | **Partly** | **Jobs: fixed.** `launch.go:604-616` passes `-c sandbox_workspace_write.network_access=false` on every Codex job: with a token and without, leads included (they are jobs). There is no Codex restart path. A repository's `.codex/config.toml` cannot turn it back on: the binary says "project configuration cannot change permissions", and `-c` beats the user's file. **Not covered: the Codex orchestrator.** `orch/start.go:257` starts it with `--ask-for-approval never --sandbox workspace-write` and no network override. A user's `~/.codex/config.toml` with `network_access = true` gives it the network with no card at all: R9-S1's shape, for the agent that reads every job's result (new-M2). **MCP server outside the sandbox:** it is the same `agentapi` tool set the shell reached through the socket before (`agentapi/mcp_job.go:94-183`, ask, say, get_question, job_adopt). Result attachments are read from paths the sandbox could already read. I found nothing an agent can do through it that it could not do at 2a13f09. **Unverified:** codex 0.167 also has permission profiles (`default_permissions`, `[permissions.<name>.network]`). Whether a user's own default profile beats `--sandbox workspace-write` plus the legacy key was not probed. test:codex (e) shows it does not on this machine's config. |
| S2 restore | **Partly** | The set-aside list is complete for app settings (`config/restore.go:14-25`, enforced by `TestEverySettingHasARestoreRule`), applied in `restore.go:511-520`. **The incoming database is never scrubbed.** `ScrubSnapshot` runs when a backup is made (`backup.go:140`) and on the kept copy (`restore.go:351`), never on the staged file. So a crafted backup still brings things that act outward or grant and are not on the list: `push_subscriptions` (every notice is pushed to the author's browser), `auth_sessions`, `api_tokens`, `action_tokens`, `machines.token`, the `codex.config-seen.*` marks (new with S5: they pass the `.codex/` gate) and `job_checks.workspace` (new-H1). Still restored, as reported: `risk.yaml`, role files, `feedback_json` and pane ids. |
| S3 tailscale-auth with publishing | **Fixed** | Flag, env or file: refused at start (`config/tsserve.go:53-71`). A saved setting: `planServe` refuses on every plan (`tsserve/service.go:310-313`), and `run.go:65` re-plans on every start and every health restart. `--tailscale-auth` is flag-only (`config/ops.go:46`), so it cannot be switched on at run time. A restore's "Use it" goes through the same PUT and then the same plan. Doctor fails (`doctor/tailscale.go:134-145`). Low: the refusal's Fix text is the `--no-auth` one (`service.go:312`). The hand-run `tailscale serve` case is still DESIGN 4.20, as documented. |
| S4 trust default | **Partly** | The form defaults to unticked until the facts load, then ticks only when nothing is found (`ProjectForm.tsx:77,93-94`). **`Inspect` misses most of what a settings file can run** (`inspect.go:88-108`): it counts `.claude/settings*.json` only when its `hooks` key is non-empty. A hostile clone's `settings.json` holding only `statusLine` or `apiKeyHelper` commands, or one of these: `env` (`NODE_OPTIONS=--require ./x.js`), `permissions.allow: ["Bash"]` (Claude never asks, so crowsnest never gets a PermissionRequest and there is no card), `enabledPlugins` plus `extraKnownMarketplaces` (plugins bring hooks and MCP servers), `enableAllProjectMcpServers`. Any of these leaves the box **ticked by default**, and the code runs or the grant applies at the first agent start: R9-S4's exploit with a different key (new-M1). That these keys take effect once a folder is trusted is from Claude Code's documented settings, not probed here. Also not covered: only the project folder is inspected, while trust covers its subfolders, so a job given `<project>/sub` loads `sub/.claude/settings.json` unseen. A folder that holds other repositories is still allowed (stated by the builder). Symlinks: a linked settings file or `.mcp.json` counts as found (`exists` uses Lstat; `readSmall` refuses the link, which counts as hooks). |
| S5 hook trust bypass | **Fixed as scoped** | Gate: `codexconfig.go:72-85`, called in `startInPane` (`launch.go:190`) before every agent start. The fingerprint covers every file under `.codex/` and refuses links and special files (`inspect.go:139-191`). The `.codex` allow step compares the page's fingerprint (`codexconfig.go:55-60`). Residuals, low: (a) the trust routes mark the folder's *current* fingerprint as seen without comparing it to what the page showed (`httpd/projects.go:240-247`); (b) only `WorkDir/.codex` is fingerprinted, and codex 0.167 has `project_root_markers`, so `.codex/` layers between the git root and a subfolder job's directory may load unseen (unverified); `.agents/` is not looked at; (c) a restore brings `codex.config-seen.*` (S2). |
| S6 rank | **Fixed** (agent-read) | `FromCrowsnest` sorts before rank within a band (`attention/model.go:292-307`). Gate status is from reserved context keys, which agents cannot set (`agentapi/server.go:342`), not from wording. Push has separate buckets. No per-job cap, as stated. |
| S7 RuleAnswerable | **Fixed** (agent-read) | `policy.go:520` goes through `insideRule`, which fails closed with no root. An edit with no job root is elevated (`risk.go:255-266`). |
| S8 trust file handling | **Fixed** | Writes through a link to its target (`file.go:132-143`). Reads are bounded with `O_NOFOLLOW|O_NONBLOCK` (`inspect.go:194-211`), also on the view path (`ReadConfig`). |
| S9 low allowlist | **Partly** | Every listed row is closed (agent-read). Still low: `env -u X -C /other git commit` (re-read: `guard/parse.go:157-168` stops at the first non-dash word), `env -iC/dir`, GNU abbreviations `--ch=` and `--chd=`, `$HOME/...` read as relative, `<root>/.GIT/hooks/...` on APFS (re-read: `risk.go:456-460` compares case-sensitively), a dangling link inside the root, and git long-option prefixes (`branch --forc`, `--dele`). |
| S10 GateShaped | **Partly** (agent-read) | Listed spellings fold. Still missed: digits and separators split words (`A11ow`, `Al.low`), Cyrillic and Greek capital-I look-alikes, `ø`, and the synonyms ok, enable, run, execute. Impact is buttons on a notification only. |
| S11 raw text | **Fixed** (agent-read) | Subjects and branches go through `Revealed`. The look-alike flag is partial. Browse project roots must still resolve to `RootDir`. |
| S12 comment details | **Fixed** (agent-read) | `revealLine` for paths; `Co` and `RuneError` written out; shadowing gone; one fix per parent (`changes.go:104-111`). |
| S13 settings by Tailscale session | **Fixed for settings** (agent-read) | `system.go:152-155` gives 403 on every set-aside setting. "Use it" and "Keep" use the same PUT. A Tailscale session can still trust folders and decide approvals: low, now that S3 refuses the local-forgery route. |

### Correctness (R9-C)

| ID | Verdict | Evidence |
|---|---|---|
| C1 send-back still landable | **Fixed** | `takeLand` refuses merge and PR while `changes_state` is set, under the jobs lock (`changes.go:214-223`). Every merge and PR goes through `LandWith`, then `takeLand` (`land.go:334`): the app, the palette, an approved agent land request (`landrequest.go:271`) and recovery. `landing.Merge` and `Push` have no other callers. Changes requested during a land are refused (`fixAdmissible` → `branchBusy` land). A stale tab: `ResolveChanges` refuses while a fix or check works (`changes.go:194-203`), and a stale head fails `flags.Head` (`land.go:342-348`). Only the human's resolve clears it. |
| C2 fix admission | **Fixed** (agent-read; lock span re-read) | Read, admit, insert and reserve all happen under one `s.mu` hold (`changes.go:104-132`). Recheck, Land and rebase refuse while it is held. An unreadable fix list refuses. Regression: new-M3. |
| C3 paused comments on a blocked job | **Fixed** (agent-read) | |
| C4 feedback batches | **Fixed** (agent-read) | Residual: a pre-upgrade stale claim retypes once. |
| C5 send claim | **Fixed** (agent-read) | Low: the start-up sweep races sends made right after boot. |
| C6 isolated tree | **Fixed for tracked files** | `VerifyCheckout` (`isolated.go:151-286`): HEAD, then every tracked path's raw blob id and mode, failing closed. It runs after the checkout, after prepare and after the check (`accept.go:516-541`). Attributes are off (`attr.tree` is empty, `core.attributesFile=/dev/null`). Residuals, low: untracked non-ignored files are allowed by decision, so a prepare that *generates new* source files (`go generate`) still tests a tree that is not H; `GIT_NO_REPLACE_OBJECTS` is not set, so a `refs/replace` entry fools `ls-tree` (info: a check's own code can just exit 0). |
| C7 leaked copies | **Fixed**, but see new-H1 | |
| C8 lost update | **Fixed** | Claude's own `<file>.lock` mkdir lock, then re-read and compare before the rename, retried 5 times, otherwise nothing is written (`file.go:65-129`). Adversarial notes, all low: (a) stale takeover is Lstat, then Remove, then retry, so a lock another process re-took in between can be removed (the same race as proper-lockfile); (b) the compare-then-rename instant remains (stated); (c) **the 15 s wait blocks a request**: `updateFile` holds the process-wide `writers` mutex across the lock wait, and Claude and Codex are done in turn, so a project create or trust request can take ~30 s, and every other trust write waits behind it. |
| C9 null config | **Fixed** | `agenttrust.go:134-136`. |
| C10 chip hides holds | **Fixed** (agent-read) | |
| C11 outdated anchor | **Fixed** (agent-read) | |
| C12 push vs queue | **Fixed** (agent-read) | Low: the label is evaluated at push time. |
| C13 publishing health | **Fixed** (agent-read) | Low: `servesOn` matches any foreground handler on the same port and target, not the child's own. |
| C14 draft protection | **Fixed** (agent-read) | |

**Continuation gate before every typed message.** Every automatic typing path goes through `continuationRefusal`:
- the restart note (`launch.go:221`);
- `typeContinuation` for budget notices, raises, finish-step and the spinning nudge (`holdset.go:403-414`);
- timed and dependency wakes (`waits.go:298-332`);
- `deliverFeedback`.

Human messages and keys stay direct, as DESIGN documents. The brief is preceded by `holds` twice in `startInPane` (`:186, :197`).

## 2. New since R9 (high and medium)

### new-H1 (high for a foreign or crafted backup): the first tick after a restore can `os.RemoveAll` any folder

- **Where:**
  - `reconcileCheckWorkspaces` (`jobs/checkspace.go:78-97`) takes every `job_checks.workspace` from the database and calls `removeLeftCopy`;
  - that calls `landing.RemoveIsolatedDir` (`isolated.go:112-127`), which calls `removeCopy`;
  - `removeCopy` runs `os.RemoveAll(parent)` (`:133`).
- **Weakness:**
  - Nothing checks that the path is under `checksDir()` or carries the marker. The ID parse in `removeLeftCopy` (`:115`) comes *after* the removal.
  - `markRestored` does not clear the column.
  - The restored instance is paused, but `Tick` runs this anyway (`recovery.go:79`).
- **Exploit:** a backup with one `job_checks` row whose `workspace` is `/Users/<you>` deletes the home folder at the first tick after the restore.
- **Fix:** accept only a direct child of `checksDir()` (`filepath.Rel` gives one element that is not `..`, and it is a plain directory, not a link) holding the marker. Also clear `workspace` in `markRestored`.

### new-M1 (medium): `Inspect` decides the trust default from the `hooks` key alone

See S4. **Fix:** count any `.claude/settings.json` or `settings.local.json` that is present as "runs" (list it and leave the box unticked), not only one with hooks.

### new-M2 (medium, needs the user's own config): the Codex orchestrator has no explicit network-off

- `orch/start.go:257-260` passes no network override, while the jobs' comment says it is passed explicitly because `~/.codex/config.toml` may turn it on.
- With approval `never`, nothing the orchestrator runs is ever a card.
- **Fix:** add `-c sandbox_workspace_write.network_access=false`, as for jobs.

### new-M3 (medium): an approved agent land is dropped after a crash mid-land

Confirmed by reading.
- `Tick` runs `ReconcileLandRequests` (`recovery.go:81`) before `reconcileBranchOps` (`:84-87`). `serve` also calls it at start (`cmd/crowsnest/jobs_wiring.go:228`).
- The dead process's `branch_op=land` is still set, so `runApprovedLand`, then `LandWith`, then `takeLand` is refused with "crowsnest is landing this job right now".
- `runApprovedLand` then journals "refused" and clears the request (`landrequest.go:270-283`).
- A PR or discard the human allowed is silently abandoned. The merge shortcut (`:264`) covers only a merge that already happened.
- `changes.go:249` says "the land's own recovery covers an approved request", and this ordering defeats that.
- **Fix:** drop stale land reservations before reconciling land requests, at start and in `Tick`.

### Also on the S2 row (medium for a foreign backup, predates R9)

The staged database is not scrubbed. **Fix:** in `markRestored`:
- run the `scrubStatements` deletes (push subscriptions, sessions, API and action tokens, machine tokens);
- delete `codex.config-seen.*`;
- clear `job_checks.workspace`.

The owner record stays, so the human can sign in to their own restore.

## Lows (introduced or left by the fix rounds)

- S5: the trust routes mark the folder's current `.codex/` fingerprint as seen, not the one the page showed (`httpd/projects.go:240-247`).
- S5: ancestor `.codex/` layers (`project_root_markers`) and `.agents/` are not fingerprinted (unverified that 0.167 loads them for a subfolder job).
- S9: `env -u X -C`, `env -iC/dir`, `--ch=`, `$HOME/...`, `.GIT` on APFS, dangling links, git option prefixes.
- S10: digits and separators, capital-I look-alikes, `ø`, synonyms.
- C6: generated untracked sources still count; replace refs are not disabled.
- C8: the 15 s lock wait inside a request, behind a process-wide mutex; the stale-takeover race.
- C13: `servesOn` matches any session on the port and target.
- C7 (agent-read): the sweep never runs without herdr (`recovery.go:76`); `check-child` kills `Getpgrp()` without checking that it leads the group.
- C5 (agent-read): the start-up claim sweep races sends made right after boot.
- 0023 backfill (agent-read): a comments fix live across the upgrade is marked `rebase:`, so it has no requested-changes hold.
- S3: the refusal's Fix sentence is the `--no-auth` one (`tsserve/service.go:312`).

## 3. Must-fix before the tag

1. **new-H1:** validate `job_checks.workspace` (a direct child of `checksDir()`, a plain directory with the marker) before removing it, and clear the column in `markRestored`.
2. **new-M3:** run `reconcileBranchOps` before `ReconcileLandRequests`, in `Tick` and at serve start.
3. **new-M1 / S4:** any `.claude/settings*.json` that is present counts as "runs" in `agenttrust.Inspect`.
4. **S2 residual:** scrub the staged database in `markRestored` (`scrubStatements`, `codex.config-seen.*`).
5. **new-M2 / S1 residual:** pass `-c sandbox_workspace_write.network_access=false` to the Codex orchestrator.

All five are small, local changes. Each needs one test.

## Not verified

- No live Codex probe:
  - whether a user `default_permissions` profile beats `--sandbox` plus the legacy network key;
  - ancestor `.codex/` layers;
  - `.agents/`.
- No live check of which Claude settings keys a trusted folder's `settings.json` activates. That comes from Claude Code's documentation.
- No test, e2e or upgrade run. The fix reports' green gates are their claims.
