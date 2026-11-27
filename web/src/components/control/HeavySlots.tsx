// Heavy-command slots (DESIGN.md 6.12): who holds and who waits for the slots `crowsnest heavy`
// hands out. HeavyChip sits beside the header's machine strip (only while a slot is busy, so an
// idle machine shows nothing extra); HeavyDetail is the System page machine card's section.
import { Hammer } from "lucide-react";
import { cn } from "../../lib/cn";
import { relativeTime } from "../../lib/format";
import { useHeavy } from "../../lib/queries";

export function HeavyChip({ className }: { className?: string }) {
  const heavy = useHeavy();
  const data = heavy.data;
  if (!data || data.deferred || (data.running.length === 0 && data.waiting.length === 0)) return null;
  const full = data.running.length >= data.slots;
  const title =
    data.running.map((owner) => `${owner.command}${owner.job ? ` (${owner.job})` : ""}`).join("; ") +
    (data.waiting.length ? `; ${data.waiting.length} waiting` : "");
  return (
    <span
      title={`Heavy commands: ${title}`}
      aria-label={`Heavy commands: ${data.running.length} of ${data.slots} slots busy, ${data.waiting.length} waiting`}
      className={cn("hidden items-center gap-1 px-1.5 text-xs tabular-nums md:inline-flex", full ? "text-chrome-warning" : "text-chrome-fg", className)}
    >
      <Hammer className="h-3.5 w-3.5 text-chrome-muted" aria-hidden />
      {data.running.length}/{data.slots}
      {data.waiting.length > 0 && <span className="text-chrome-warning">+{data.waiting.length}</span>}
    </span>
  );
}

export function HeavyDetail() {
  const heavy = useHeavy();
  const data = heavy.data;
  if (!data) return null;
  return (
    <section aria-label="Heavy commands">
      <h3 className="mb-1 text-2xs uppercase tracking-wide text-muted">Heavy commands ({data.slots} slots, crowsnest heavy)</h3>
      {data.deferred && <p className="text-xs text-muted">Commands go to {data.deferred}, whose own slots `heavy --status` shows.</p>}
      {!data.deferred && data.running.length === 0 && data.waiting.length === 0 && <p className="text-xs text-muted">No heavy command running.</p>}
      <ul className="flex flex-col gap-0.5 text-xs">
        {data.running.map((owner) => (
          <li key={`r${owner.pid}`} className="truncate">
            <span className="font-mono text-muted">slot {owner.slot}</span> {owner.command} {owner.job && <span className="text-muted">({owner.job})</span>}{" "}
            <span className="text-muted">since {relativeTime(owner.since)}</span>
          </li>
        ))}
        {data.waiting.map((owner) => (
          <li key={`w${owner.pid}`} className="truncate text-warning">
            waiting: {owner.command} {owner.job && `(${owner.job})`}
          </li>
        ))}
      </ul>
    </section>
  );
}
