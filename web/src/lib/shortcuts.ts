// Every keyboard shortcut the app has, as data: the shortcuts sheet (`?`) shows it, and the
// manual's reference page (docs/manual/reference/keyboard-shortcuts.md) is generated from it, so
// the two cannot drift (src/manual/manual.test.ts checks the committed page). Keep it in step when
// a view adds a shortcut.
import { viewKeys } from "./palette";

export interface Shortcut {
  keys: string[];
  what: string;
}

export interface ShortcutSection {
  title: string;
  shortcuts: Shortcut[];
}

export function shortcutSections(mac: boolean): ShortcutSection[] {
  const mod = mac ? "⌘" : "Ctrl";
  const talkChord = mac ? "⌥" : "Ctrl";
  return [
    {
      title: "Anywhere",
      shortcuts: [
        { keys: [`${mod} K`], what: mac ? "Command palette (also from inside a terminal)" : "Command palette (not inside a terminal, where Ctrl K is the shell's)" },
        { keys: ["?"], what: "Keyboard shortcuts (this sheet), with the way into the manual" },
        { keys: ["T"], what: "Next theme (Settings → Appearance has them all)" },
        ...viewKeys.map((view) => ({ keys: ["g", view.key], what: `Go to ${view.label}` })),
        { keys: ["g", "n"], what: "Focus the needs-you tray (then y or n answers the approval you open)" },
      ],
    },
    {
      title: "In the palette",
      shortcuts: [
        { keys: ["↑", "↓"], what: "Move (also Ctrl N, Ctrl P)" },
        { keys: ["↵"], what: "Run the highlighted item" },
        { keys: [`${mod} ↵`], what: "Its second action: the full job form, send and open Chat, the other view; else its actions" },
        { keys: ["→"], what: "Its actions (also Tab)" },
        { keys: ["esc"], what: "Back a level, then close (focus returns where it was)" },
        { keys: [">"], what: "At the start: give what follows as a job (so does “job …”)" },
      ],
    },
    {
      title: "Views",
      shortcuts: [
        { keys: [`${mod} ↵`], what: "Jobs: give the job in the text box" },
        { keys: ["↵"], what: "Chat: send (Shift ↵ for a new line), from a wide screen" },
        { keys: ["/"], what: "Fleet: filter the agents" },
        { keys: ["Space"], what: "Chat: hold to talk, let go to send (outside a text field; a quick tap does nothing)" },
        { keys: [`${talkChord} Space`], what: "Chat, in the message box: hold to talk" },
        { keys: ["esc"], what: "Close a sheet or menu; cancel a recording" },
        { keys: ["←", "→"], what: "Move between tabs" },
        { keys: ["j", "k"], what: "Jobs and the Inbox: next and previous row (↵ opens it)" },
        { keys: ["j", "k"], what: "Changes: next and previous file" },
        { keys: ["n", "p"], what: "Changes: next and previous change" },
        { keys: ["v"], what: "Changes: mark the current file viewed (again to unmark)" },
        { keys: ["c"], what: "Changes: comment on the selected lines (or the focused line, else the whole file)" },
        { keys: ["f"], what: "Changes: full screen (esc leaves it)" },
        { keys: ["1", "…", "9"], what: "A decision: press its choices in order (a granting one still asks again)" },
        { keys: ["esc"], what: "A decision: go back where you came from" },
      ],
    },
  ];
}

