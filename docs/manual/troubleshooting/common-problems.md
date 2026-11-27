# Common problems

The real problems seen so far, and what fixes each.

## The folder-trust prompt

**What you see**: a new lead, job or the orchestrator does not start; its card (or the first-run
page) shows a dialog: Claude asks whether you trust the files in this folder.

**Why**: Claude Code asks once per directory before it runs anything there. crowsnest never answers
it for you.

**Fix**: press the option on the dialog card (in the Inbox, on Now, or the first-run page), or open
the agent's terminal and answer it. It is asked once per directory; projects' directories get it
the first time a lead or job starts there.

## Claude sign-in expired

**What you see**: jobs stuck with **Claude needs a login or billing fix**; no retries happen. Or
the orchestrator stops at a login screen.

**Fix**:

1. **Open terminal** on the stuck job (or **More → Terminal**).
2. Run `claude`, then `/login`, and follow the link (it can be opened on any device).
3. Back on the job, **Restart agent** or **Get it moving**.

`crowsnest doctor` shows whether Claude is signed in; `crowsnest doctor --deep` makes a real call,
which catches a login that looks signed in but is not.

## A guard refusal

**What you see**: an agent says a command was refused: "This command addresses nyc1, a protected
(production) host. This is refused by crowsnest's guard and cannot be approved from here…", or "A
push onto main: it is a protected branch, reached only through a pull request."

**Why**: the guard runs before every Bash call and refuses, without asking, commands that would
reach a protected host, commit or push onto a protected branch (or force-push it), stop the herdr
server, or drive another herdr session. It is a seatbelt against accidents, not a boundary
([Safety](../safety.md)).

**Fix**: usually nothing: the agent should take another route (a branch and a pull request). If you
really want it done, do it yourself in a terminal. To change what is protected: Settings → Guard →
**Protected hosts** and **Protected branches** (apply when crowsnest restarts), plus the
protected-hosts file (`--protected-hosts-file`).

## Port in use

**What you see**: `crowsnest` refuses to start: the port (2769) is in use.

**Fix**: `crowsnest doctor` says whether it is another crowsnest (it asks its health endpoint) or
something else. Use the running one, stop it, or start this one with `--port N` (and a different
`--session`, so two crowsnests never share a herdr session).

## Push not arriving on iOS

**What you see**: notifications work on the laptop but never on the iPhone.

**Check, in order**:

1. You opened crowsnest **from the Home Screen icon**, not from Safari. Web Push on iOS works only
   for the installed app (Safari, Share, **Add to Home Screen**; iOS 16.4 or later).
2. It is served over **HTTPS** (`tailscale serve`), not plain HTTP on a tailnet IP.
3. In the installed app: account menu → **Notifications** → **Turn on for this device**, allow it,
   then **Send a test**.
4. iOS Settings → Notifications → crowsnest is allowed, and Focus is not hiding it.
5. You were not "here" on another device: while a focused desk tab is in use, nothing is pushed
   ([Presence](../concepts/presence.md)).

Buttons on notifications never appear on iOS; tap the notification to open the decision packet.

## The mic needs HTTPS

**What you see**: pressing the mic says "The microphone needs a secure page: this one is plain
HTTP on a network address, so the browser offers no microphone."

**Why**: browsers allow the microphone only on HTTPS or localhost.

**Fix**: use `tailscale serve` ([Reach it from your phone](../how-to/phone.md)), or `--tls-cert`
and `--tls-key`. Spoken replies (speak mode) work over plain HTTP; only the mic needs it.

## "Host not allowed" (403)

**Why**: crowsnest checks the Host of every request (this is what stops DNS rebinding). It knows
localhost, the bound addresses, the machine name on a non-loopback bind, and the detected MagicDNS
name.

**Fix**: add the name you use with `--allowed-host` (a reverse proxy, a custom DNS name).

## Sign-in keeps failing after a few tries

**Why**: five failures a minute per client lock it out for a moment. Behind a local proxy
(`tailscale serve`, a tunnel) every client looks like one address unless you set
`CROWSNEST_TRUSTED_PROXY=loopback`.

## The orchestrator says "waiting"

**Why**: it will not start while the first-run checklist is unfinished, herdr is not connected, or
no agent is installed. The status says which; it starts by itself when that changes.

## An answer never reached the agent

Look at its receipt in the Inbox. **Held** means the agent is at a dialog or has text in its input
box; clear it. **Delivery uncertain** or **Not delivered**: press **Did it arrive?**, then **Type it
again** only if it was not seen. See [Receipts](../concepts/questions-approvals-dialogs.md#receipts).

## Something else

[Doctor checks](doctor.md), then **System → Copy diagnostics** for an issue (no secrets in it).
