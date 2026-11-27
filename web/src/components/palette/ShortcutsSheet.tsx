// The keyboard shortcuts sheet (`?`, or "Keyboard shortcuts" in the palette): every shortcut the
// app has (lib/shortcuts.ts), in one place, and the way into the manual.
import { Link } from "@tanstack/react-router";
import { BookOpen } from "lucide-react";
import { shortcutSections } from "../../lib/shortcuts";
import { Sheet } from "../ui/Sheet";

export function ShortcutsSheet({ open, onClose, mac }: { open: boolean; onClose: () => void; mac: boolean }) {
  return (
    <Sheet open={open} onClose={onClose} title="Keyboard shortcuts" size="md">
      <div className="flex flex-col gap-4 p-3 md:p-4">
        {shortcutSections(mac).map((section) => (
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
        <Link to="/help" onClick={onClose} className="inline-flex min-h-11 items-center gap-1.5 self-start text-sm font-medium text-accent hover:underline md:min-h-8">
          <BookOpen className="h-4 w-4" aria-hidden />
          Manual: how crowsnest works, step by step
        </Link>
      </div>
    </Sheet>
  );
}
