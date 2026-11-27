# Reach it from your phone

crowsnest listens on `localhost` by default. To use it from a phone, put it on your tailnet. The
recommended way is `tailscale serve`, because the phone then gets HTTPS, which three things need:
the microphone, Web Push notifications, and installing the app to the Home Screen.

## With `tailscale serve` (recommended)

1. On the machine running crowsnest, once:

   ```sh
   tailscale serve --bg 2769
   ```

   crowsnest prints this command (in `crowsnest doctor`, the first-run checklist and System); it
   never runs a Tailscale command that changes anything itself.
2. Tell crowsnest the proxy is local, so the login limiter counts each client separately, then
   start it:

   ```sh
   export CROWSNEST_TRUSTED_PROXY=loopback
   crowsnest
   ```

3. On the phone open `https://<machine>.<tailnet>.ts.net`. crowsnest finds that name at start
   (`tailscale status --json`) and allows it; nothing else is needed. HTTPS certificates must be
   enabled for your tailnet (Tailscale admin console → DNS).
4. Sign in.

**Optional: sign in with Tailscale.** Start with `--tailscale-auth`, sign in once with the password
through serve, open **System**, **Sign in with Tailscale**, and press **Allow me**. From then on the
phone is signed in by its tailnet identity. Leave it off on a machine other people use.

## Add it to the Home Screen

On an iPhone or iPad, Web Push works only for the app added to the Home Screen (iOS 16.4 or
later):

1. Open the `https://…ts.net` address in **Safari**.
2. Share → **Add to Home Screen**.
3. Open crowsnest from the Home Screen icon (not from Safari) from now on.

On Android, Chrome offers **Install app** or **Add to Home screen** in its menu.

## Turn on notifications

1. Account menu → **Notifications** ([Open Notifications](app:sheet/notifications)).
2. **Turn on for this device**, and allow notifications when the browser asks.
3. **Send a test** to check it arrives.
4. Under **What to send**, switch kinds on or off for every device: Approvals, Questions, Terminal
   dialogs, Jobs stuck or failed, Jobs done, Orchestrator.

The **Devices** list shows every subscribed device (at most 20), with Remove. Notifications are
routed by [presence](../concepts/presence.md): while you are at the desk, the phone stays quiet.

What a notification says and which buttons it carries: [Notifications](../reference/notifications.md).

## The other way: `--bind tailnet`

`crowsnest --bind tailnet` listens on the machine's Tailscale address directly, plain HTTP over
WireGuard. Simpler, but a plain-HTTP page is not a secure context: no microphone, no Web Push, no
Home Screen install.

## Using it on a phone

- The bottom bar is **Now**, **Projects**, **Inbox**, **Chat** and **More** (Jobs, Fleet,
  Terminal, Help, System).
- Questions, approvals and dialogs open as a [decision packet](answer.md#the-decision-packet)
  with the answer bar under your thumb.
- A stuck job has **Rescue** buttons so you rarely need the terminal: [Rescue a stuck
  agent](rescue.md).
- The search icon in the header opens the command palette full screen.
- `docs/PHONE-CHECK.md` in the repository is a ten-minute checklist for a real iPhone.

[Open Now](app:/) on the phone to start.

## What can go wrong

- **"Host not allowed" (403)**: you opened an address crowsnest does not know, such as a custom
  DNS name. Add it with `--allowed-host`.
- **The mic button explains it needs HTTPS**: you are on plain HTTP (`--bind tailnet` or an IP).
  Use `tailscale serve`.
- **No notifications on iOS**: open the app from the Home Screen icon, not Safari, and turn
  notifications on there. See [Common problems](../troubleshooting/common-problems.md#push-not-arriving-on-ios).
- **Everyone fails to sign in after a few tries**: behind serve without
  `CROWSNEST_TRUSTED_PROXY=loopback`, every client looks like one address to the login limiter.
