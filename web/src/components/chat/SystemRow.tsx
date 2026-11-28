// SystemRow: a message crowsnest typed into an agent's conversation (a job's brief header, a check
// result, review comments, a nudge, a restart), drawn as one line rather than as the human's own
// bubble (R10 Opus F18: "[crowsnest job j_10] You hold job j_10 …" read as something you said).
// What was sent stays one tap away.
import { ChevronRight, Radio } from "lucide-react";
import { useState } from "react";
import { cn } from "../../lib/cn";

export function SystemRow({ line, text }: { line: string; text: string }) {
  const [open, setOpen] = useState(false);
  return (
    <div role="note" aria-label="From crowsnest" className="min-w-0 text-xs text-muted">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        className="flex min-h-11 w-full min-w-0 items-center gap-1.5 rounded px-1 text-left hover:text-heading md:min-h-7"
      >
        <span aria-hidden className="h-px w-3 shrink-0 bg-border" />
        <ChevronRight aria-hidden className={cn("h-3.5 w-3.5 shrink-0 transition-transform", open && "rotate-90")} />
        <Radio aria-hidden className="h-3.5 w-3.5 shrink-0" />
        <span className="min-w-0 truncate font-medium">{line}</span>
        <span aria-hidden className="h-px min-w-3 flex-1 bg-border" />
      </button>
      {open && <p className="mb-1 ml-6 whitespace-pre-wrap break-words border-l-2 border-border pl-4">{text}</p>}
    </div>
  );
}
