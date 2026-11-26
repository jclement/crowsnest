// PokeRow: a poke in a conversation (DESIGN.md 7.4). A poke is crowsnest talking to the
// orchestrator, not the human, so it is a compact one-line system row rather than a user bubble:
// "Poke · 2 reasons · 03:10", with the reasons behind a disclosure.
import { BellRing, ChevronRight } from "lucide-react";
import { useState } from "react";
import { cn } from "../../lib/cn";
import { parsePoke } from "../../lib/journal";

/** Local wall-clock time of the poke's header, e.g. "03:10". Empty when it cannot be read. */
function clock(at: string | undefined): string {
  if (!at) return "";
  const time = Date.parse(at);
  if (Number.isNaN(time)) return "";
  return new Date(time).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function PokeRow({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const poke = parsePoke(text) ?? { reasons: [] };
  const count = poke.reasons.length;
  const when = clock(poke.at);
  return (
    <div role="note" aria-label="Poke from crowsnest" className="min-w-0 text-xs text-muted">
      <button
        type="button"
        aria-expanded={open}
        disabled={count === 0}
        onClick={() => setOpen((value) => !value)}
        className="flex min-h-11 w-full min-w-0 items-center gap-1.5 rounded px-1 text-left hover:text-heading disabled:hover:text-muted md:min-h-7"
      >
        <span aria-hidden className="h-px w-3 shrink-0 bg-border" />
        {count > 0 && <ChevronRight aria-hidden className={cn("h-3.5 w-3.5 shrink-0 transition-transform", open && "rotate-90")} />}
        <BellRing aria-hidden className="h-3.5 w-3.5 shrink-0" />
        <span className="font-medium">Poke</span>
        <span>·</span>
        <span>
          {count} reason{count === 1 ? "" : "s"}
        </span>
        {when && (
          <>
            <span>·</span>
            <time dateTime={poke.at}>{when}</time>
          </>
        )}
        {poke.talk && <span className="text-2xs">(talk on)</span>}
        <span aria-hidden className="h-px min-w-3 flex-1 bg-border" />
      </button>
      {open && (
        <ul className="mb-1 ml-6 list-disc space-y-0.5 border-l-2 border-border pl-4">
          {poke.reasons.map((reason, i) => (
            <li key={i} className="break-words">
              {reason}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
