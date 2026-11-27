# Presence: here, maybe, away

crowsnest works out whether you are watching, so it tells you things in the right place: on
screen when you are there, on your phone when you are not, and never both at once.

## The three states

Each open browser tab sends a small heartbeat every 30 seconds and whenever it gains or loses focus
or visibility: whether it is visible and focused, and how long since you last typed, clicked,
touched or scrolled. From all of them crowsnest decides:

| State | When |
|---|---|
| **here** | A visible, focused tab with input in the last 2 minutes. |
| **maybe** | A visible tab, or input in the last 15 minutes. |
| **away** | Otherwise (no tab open counts as away). |

Nothing but these coarse states is kept, in memory only.

## What it changes

| You are | Something needs you |
|---|---|
| **here** | The tray and (with speak mode on) a sound. No phone notification, no notify URL. |
| **maybe** | A phone notification (Web Push). A blocking item also goes to the notify URL. |
| **away** | Both: Web Push and the notify URL. |

The on-screen and remote channels are never both used for one item within 2 minutes.

Pokes carry it too: the orchestrator's and leads' instructions tell them what `human=away` means
(do not wait on an answer; keep going on what it does not block).

## Do not disturb

Account menu → **Do not disturb…**: **Do not disturb until (HH:MM)**, then **Hold notifications**.
Until then every non-blocking push and notify line is held, and one summary is sent when it ends.
Blocking questions and dangerous approvals still get through. **Turn off** ends it early.

## When you use it

You do not, mostly: it works by itself. Use **Do not disturb** for a meeting or the night, and the
[pause](budgets-limits-pause.md#the-pause) when you want crowsnest to stop acting, not just stop
talking.

## What can go wrong

- **No notification while the laptop is open.** A visible tab with recent input counts as here.
  Lock the screen or close the tab, and it becomes maybe, then away.
- **Notifications on the phone while you are at the desk.** The desk's tab was not focused or had
  no input for 2 minutes; that is maybe, which sends Web Push.
