// ToolCard: one tool call in a conversation (DESIGN.md 7.2). Collapsed it is a single line, the
// tool's name and a short summary of its input, so a turn with twenty tool calls stays readable on
// a phone; expanded it shows the input and the (server-truncated) result. A call still waiting for
// its result spins; a failed one is marked.
import { AlertCircle, ChevronRight, Loader2, Wrench } from "lucide-react";
import { useId, useState } from "react";
import { cn } from "../../lib/cn";
import type { ToolCall } from "../../lib/conversation";

export function ToolCard({ tool }: { tool: ToolCall }) {
  const [open, setOpen] = useState(false);
  const bodyId = useId();
  const state = !tool.done ? "running" : tool.is_error ? "failed" : "done";
  return (
    <div className={cn("min-w-0 rounded border bg-surface-raised", tool.is_error ? "border-danger/40" : "border-border")}>
      <button
        type="button"
        aria-expanded={open}
        aria-controls={bodyId}
        onClick={() => setOpen((value) => !value)}
        className="flex min-h-11 w-full min-w-0 items-center gap-2 px-2 text-left text-xs hover:bg-hover md:min-h-8"
      >
        <ChevronRight aria-hidden className={cn("h-3.5 w-3.5 shrink-0 text-muted transition-transform", open && "rotate-90")} />
        <Wrench aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted" />
        <span className="shrink-0 font-mono font-medium text-heading">{tool.name}</span>
        <span className="min-w-0 flex-1 truncate font-mono text-muted" title={tool.summary}>
          {tool.summary}
        </span>
        {state === "running" && <Loader2 aria-label="Running" className="h-3.5 w-3.5 shrink-0 animate-spin text-accent" />}
        {state === "failed" && <AlertCircle aria-label="Failed" className="h-3.5 w-3.5 shrink-0 text-danger" />}
      </button>
      {open && (
        <div id={bodyId} className="flex flex-col gap-2 border-t border-border p-2">
          {tool.input && <ToolBlock label="Input" text={tool.input} />}
          {tool.done ? (
            <ToolBlock
              label={tool.is_error ? "Error" : "Result"}
              text={tool.result || "(no output)"}
              note={tool.result_truncated ? "Truncated; the terminal has the rest." : undefined}
              danger={tool.is_error}
            />
          ) : (
            <p className="text-2xs text-muted">Waiting for the result…</p>
          )}
        </div>
      )}
    </div>
  );
}

function ToolBlock({ label, text, note, danger }: { label: string; text: string; note?: string; danger?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="mb-0.5 text-2xs font-medium uppercase tracking-wide text-muted">{label}</div>
      <pre
        data-allow-overflow
        className={cn(
          "max-h-72 overflow-auto rounded bg-surface-sunken p-2 font-mono text-2xs leading-relaxed whitespace-pre-wrap break-words",
          danger ? "text-danger" : "text-body",
        )}
      >
        {text}
      </pre>
      {note && <p className="mt-0.5 text-2xs text-muted">{note}</p>}
    </div>
  );
}
