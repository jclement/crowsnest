// A first-visit hint: one line at the top of a view the first time you open it, with a link to the
// manual and "Got it". Sparse (a handful in the whole app), dismissible, never modal, remembered per
// browser (localStorage crowsnest-hint:<id>). A plain strip in the flow of the page.
import { Lightbulb, X } from "lucide-react";
import { useState, type ReactNode } from "react";
import { usePaletteMaybe } from "../palette/palette-context";

const PREFIX = "crowsnest-hint:";

function hintSeen(id: string, storage: Pick<Storage, "getItem"> | undefined = safeStorage()): boolean {
  try {
    return storage?.getItem(PREFIX + id) === "1";
  } catch {
    return true;
  }
}

function markSeen(id: string) {
  try {
    safeStorage()?.setItem(PREFIX + id, "1");
  } catch {
    // Private mode: the hint comes back next time, which is harmless.
  }
}

function safeStorage(): Storage | undefined {
  try {
    return typeof window === "undefined" ? undefined : window.localStorage;
  } catch {
    return undefined;
  }
}

export function Hint({ id, topic, children }: { id: string; topic?: string; children: ReactNode }) {
  const [seen, setSeen] = useState(() => hintSeen(id));
  const palette = usePaletteMaybe();
  if (seen) return null;
  const dismiss = () => {
    markSeen(id);
    setSeen(true);
  };
  return (
    <aside aria-label="Hint" data-hint={id} className="flex flex-wrap items-center gap-x-2 rounded border border-accent/30 bg-accent/5 px-2.5 py-1 text-xs text-body">
      <Lightbulb className="h-3.5 w-3.5 shrink-0 text-accent" aria-hidden />
      <p className="min-w-0 flex-1 basis-[calc(100%-1.5rem)] py-1 sm:basis-0">{children}</p>
      <span className="ml-auto flex items-center">
        {topic && palette && (
          <button type="button" className="inline-flex h-11 items-center rounded px-2 font-medium text-accent hover:bg-hover md:h-7" onClick={() => palette.openHelp(topic)}>
            Read more
          </button>
        )}
        <button type="button" onClick={dismiss} className="inline-flex h-11 items-center gap-1 rounded px-2 text-muted hover:bg-hover hover:text-heading md:h-7">
          <X className="h-3.5 w-3.5" aria-hidden />
          Got it
        </button>
      </span>
    </aside>
  );
}
