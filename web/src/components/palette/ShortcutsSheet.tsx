// The keyboard shortcuts sheet (`?`, or "Keyboard shortcuts" in the palette): every shortcut the
// app has, in one place. Keep it in step when a view adds one.
import { viewKeys } from "../../lib/palette";
import { Sheet } from "../ui/Sheet";

interface Shortcut {
  keys: string[];
  what: string;
}

function sections(mac: boolean): Array<{ title: string; shortcuts: Shortcut[] }> {
  const mod = mac ? "⌘" : "Ctrl";
  const talkChord = mac ? "⌥" : "Ctrl";
  return [
    {
      title: "Anywhere",
      shortcuts: [
        { keys: [`${mod} K`], what: mac ? "Command palette (also from inside a terminal)" : "Command palette (not inside a terminal, where Ctrl K is the shell's)" },
        { keys: ["?"], what: "This sheet" },
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
      ],
    },
  ];
}

export function ShortcutsSheet({ open, onClose, mac }: { open: boolean; onClose: () => void; mac: boolean }) {
  return (
    <Sheet open={open} onClose={onClose} title="Keyboard shortcuts" size="md">
      <div className="flex flex-col gap-4 p-3 md:p-4">
        {sections(mac).map((section) => (
          <section key={section.title} aria-label={section.title}>
            <h3 className="mb-1 text-2xs font-semibold uppercase tracking-wide text-muted">{section.title}</h3>
            <dl className="divide-y divide-border rounded border border-border">
              {section.shortcuts.map((shortcut) => (
                <div key={shortcut.keys.join("+") + shortcut.what} className="flex min-h-9 items-center gap-3 px-2 py-1">
                  <dt className="flex w-24 shrink-0 gap-1">
                    {shortcut.keys.map((key) => (
                      <kbd key={key} className="rounded border border-border-strong bg-surface-sunken px-1.5 font-mono text-2xs text-heading">
                        {key}
                      </kbd>
                    ))}
                  </dt>
                  <dd className="text-xs text-body">{shortcut.what}</dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
        <p className="text-2xs text-muted">The g shortcuts and ? do nothing while you type in a field or a terminal.</p>
      </div>
    </Sheet>
  );
}
