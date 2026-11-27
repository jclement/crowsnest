# Notifications

crowsnest can reach you three ways when something needs you: on screen (the tray, a sound), Web
Push to your devices, and a one-line POST to a notify URL (ntfy-style). Which ones it uses depends
on whether you are watching: [Presence](../concepts/presence.md).

## Kinds

Switch each on or off for every device in **Notifications** → **What to send**
([Open Notifications](app:sheet/notifications)):

| Kind | Sent when |
|---|---|
| **Approvals** | A tool call waits for your approval (low-risk ones get Allow and Deny buttons where the browser shows them). |
| **Questions** | An agent asks something, blocking or normal. Low-urgency questions are never pushed. |
| **Terminal dialogs** | An agent waits at a permission or question dialog. |
| **Jobs stuck or failed** | A job stopped moving, failed, or needs a login or fix. |
| **Jobs done** | A job finished. |
| **Orchestrator** | It needs the terminal (a start-up prompt) or has stopped. |

## What a notification says

The title says where it belongs, the body what is asked:

- "quark: Website: blocking question" / "j_15 Homepage hero: Which hero copy? · Recommended: Short
  headline"
- "quark: Crowsnest: DANGEROUS approval" / the command
- "quark: Handbook: terminal dialog"
- "Unfiled" when the item has no project.

Tapping it opens the [decision packet](../how-to/answer.md#the-decision-packet) for that item.

## Buttons

Only where the platform shows them (Android and desktop browsers; **never on iOS**), and only for
safe cases:

| Item | Buttons |
|---|---|
| Low-risk approval whose command fits uncut | Allow, Deny |
| Low-risk approval whose command was cut | Snooze |
| Elevated or dangerous approval | none |
| A gate: a question that lands, allows, approves, merges, pushes, deploys, deletes, grants or permits | none |
| Routine question (one choice, no files, short) with a recommendation | the recommended option, Snooze |
| Any other question, a dialog | Snooze |

A button carries a single-use token for that one item and action, valid 15 minutes. **Snooze**
hides the item for ten minutes on every device and decides nothing. Gates are refused by the
server even if a button were forged.

## Do not disturb

Account menu → **Do not disturb…** holds every non-blocking push and notify line until a time and
sends one summary then. Blocking questions and dangerous approvals still get through.

## The notify URL

Settings → Notices → **Notify URL** (or `--notify-url`, `--notify-url-file`): crowsnest POSTs a
one-line notice there when something needs you, for ntfy or anything that takes a plain POST. It
is write-only in the app (never shown again) and treated as a secret. It is used when you are away
(and for blocking items when you are "maybe").

## Devices

**Devices** lists every subscribed device (at most 20) with **Remove**. A device that fails five
times with no success in 30 days is removed by the nightly clean-up. A new device is recorded in
the Journal. After a restore from backup, every device must turn notifications on again.

## Requirements

Web Push needs a secure context: `localhost`, or HTTPS (`tailscale serve`, or `--tls-cert`). On an
iPhone or iPad it works only in the app added to the Home Screen and opened from there (iOS 16.4 or
later). See [Reach it from your phone](../how-to/phone.md).
