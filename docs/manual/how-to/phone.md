# Reach it from your phone

crowsnest listens on `localhost` by default. To use it from a phone, put it on your tailnet. The
recommended way is to let crowsnest publish itself with `tailscale serve`, because the phone then
gets HTTPS, which three things need: the microphone, Web Push notifications, and installing the
app to the Home Screen.

## Publish to your tailnet (recommended)

1. Install Tailscale on the machine running crowsnest and sign in.
2. Open **System** ([Open System](app:/system)). The **Publish to your tailnet** card shows the
   exact address it will create, such as `https://quark.tail1234.ts.net`, and what that means:
   anyone on your tailnet who can reach this machine gets the sign-in page there. The first-run
   checklist shows the same card under *Reach it from your phone*.
3. Press **Publish**. The card shows the address as a link and a QR code.
4. On the phone (signed in to the same tailnet), scan the QR code with the camera, or type the
   address, and sign in.

crowsnest remembers the choice and publishes again at every start, until you press **Stop
publishing** (or turn *Publish to your tailnet* off in Settings, or start it with
`--tailscale-serve=false`). The address lasts exactly as long as crowsnest runs: it is a
foreground `tailscale serve` that crowsnest supervises, so stopping or killing crowsnest takes it
away, and nothing is left in Tailscale's config. It never uses Funnel: nothing goes on the public
internet.

The address is `https://<machine>.<tailnet>.ts.net` when port 443 is free on this machine's
Tailscale; if another serve config uses 443, crowsnest takes 8443, then 10000, and the card says
which (`--tailscale-serve-port` picks one). It never changes anyone else's serve config.

Set `CROWSNEST_TRUSTED_PROXY=loopback` as well, so the login limiter counts each phone separately
rather than everything arriving through serve as one client.

It will not publish without a login (`--no-auth`): every device on the tailnet would get a shell.

### By hand instead

If you want the address to outlive crowsnest, run `tailscale serve` yourself:

```sh
tailscale serve --bg 2769
export CROWSNEST_TRUSTED_PROXY=loopback
crowsnest
```

crowsnest finds the MagicDNS name at start and allows it; the card then says the address is
already published and leaves it alone. Undo it with `tailscale serve --https=443 off`.

**Optional: sign in with Tailscale.** Start with `--tailscale-auth`, sign in once with the password
at the published address, open **System**, **Sign in with Tailscale**, and press **Allow me**. From then on the
phone is signed in by its tailnet identity. Leave it off on a machine other people use.

## Add it to the Home Screen

On an iPhone or iPad, Web Push works only for the app added to the Home Screen (iOS 16.4 or
later):

1. Open the `https://…ts.net` address in **Safari**.
2. Share → **Add to Home Screen**.
3. Open crowsnest from the Home Screen icon (not from Safari) from now on.

On Android, Chrome offers **Install app** or **Add to Home screen** in its menu.

## Turn on notifications

1. Account menu → **Settings** → **Notifications** ([Open Notifications](app:/settings#notifications)).
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

- The bottom bar is **Now**, **Projects**, **Inbox**, **Chat** and **More** (Jobs, Agents,
  Help, System).
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
