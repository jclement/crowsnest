# Use the terminal views

Most of the time you will not need a terminal: answers, rescue and landing all have buttons. When
you do, there are two kinds, and both are real: what you type reaches the agent.

## The two terminals

| | What it is | Where |
|---|---|---|
| **Session terminal** | herdr's own TUI for the whole session: every workspace, tab and pane, exactly as `herdr --session crowsnest` shows it | **Agents** (under **More**), beside the tree when no agent is picked; on a phone its **Session terminal** row; `g` then `a` |
| **A pane's terminal** | One pane only (an agent's, or a plain shell), sized to fit your screen | A job's **Terminal** tab; Agents → an agent → **Terminal**; **Open terminal** on a stuck card |

Closing a browser tab only closes that client: never the herdr server, never an agent.

## When you use which

- **A pane's terminal**: to answer something only the screen shows (a login, a form), to look at
  a stuck agent, to type a slash command, or to clear a half-typed draft in its input box.
- **The session terminal**: to work with herdr itself: move between workspaces, open a new tab,
  start a shell, see everything at once. It is the same TUI you use on your desktop.

## How

1. Open the job and press its **Terminal** tab (or **Open terminal** / **Open the terminal**).
2. Click or tap into it and type. The status line says **connected**, **reconnecting** (with a
   countdown and **Retry now**), or **Ended** with the reason and **Reconnect**.
3. On a phone or touch screen, the key bar under it supplies what the on-screen keyboard lacks: on
   a narrow screen two rows, **Esc**, **Tab**, a sticky **Ctrl** (the next character is sent as
   its control code), arrows and **Enter**; then **^C**, **Paste**, **Copy** (the selection, or the
   visible screen when nothing is selected), and **A−** / **A+** for the font size (remembered).

The Agents view ([Open Agents](app:/agents), `g` then `a`) lists every workspace, tab and pane with
its agent's state; pick an agent for its **Conversation** or **Terminal**. `/` filters it. Plain
shell panes can be opened too. (It was two views, Fleet and Terminal; their old links land here.)

## Keys to know

- `⌘K` opens the command palette even inside a terminal. `Ctrl K` does not: it belongs to the
  shell (kill-line).
- The `g` shortcuts and `?` do nothing while a terminal has focus.
- A right click stays in the terminal (for the shell's own menu or selection), not the page menu.

## One browser at a time

One browser controls a pane at a time. Opening it elsewhere takes it over; the first browser says
so ("Ended: terminal attach taken over") and offers **Reconnect**.

## What can go wrong

- **Paste does nothing**: the clipboard needs a secure context (HTTPS or localhost); otherwise a
  toast says to long-press and paste.
- **Characters look wrong**: the terminals use the JetBrains Mono Nerd Font if installed, else
  bundled fonts that load as glyphs appear; give them a second.
- **The session terminal shows machines you did not expect**: herdr's TUI shows every machine your
  herdr config names. crowsnest starts it with its own state directory unless
  `--terminal-machines` is set.
- **A 44-column phone terminal is cramped**: rotate to landscape (one row of keys), or use the
  Rescue buttons instead ([Rescue a stuck agent](rescue.md)).

[Open the session terminal](app:/agents?session=true).
