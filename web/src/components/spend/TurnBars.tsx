// TurnBars: a job's tokens per model call as a tiny bar chart (DESIGN.md 7.11), oldest call on
// the left. One series, so no legend: the caption names it. Built to the dataviz skill's specs:
// one hue (the accent token, so it is selected per theme, not flipped), a baseline, bars with a
// gap between them, rounded data ends, the largest value labelled once in text ink, and a
// tooltip per bar on hover and keyboard focus with a hit target the full height of the plot.
import { useId, useState } from "react";
import { cn } from "../../lib/cn";
import { bucketTurns, formatTokens, formatUSD, type SpendTurn } from "../../lib/spend";

/** Plot height in px: tiny on purpose, it sits inside a card. */
const HEIGHT = 48;
function timeOf(at: string): string {
  return new Date(at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function TurnBars({ turns }: { turns: SpendTurn[] }) {
  const captionId = useId();
  const [active, setActive] = useState<number | null>(null);
  const bars = bucketTurns(turns);
  if (bars.length === 0) return null;
  const peak = Math.max(...bars.map((bar) => bar.tokens), 1);
  const shown = active === null ? null : bars[active];
  const grouped = bars.some((bar) => bar.calls > 1);
  return (
    <figure className="flex min-w-0 flex-col gap-1">
      <figcaption id={captionId} className="flex items-baseline justify-between gap-2 text-2xs text-muted">
        <span>Tokens per model call{grouped ? " (grouped)" : ""}, oldest first</span>
        <span className="font-mono">max {formatTokens(peak)}</span>
      </figcaption>
      <div
        role="list"
        aria-labelledby={captionId}
        className="relative flex items-end gap-[2px] border-b border-border-strong"
        style={{ height: HEIGHT }}
        onMouseLeave={() => setActive(null)}
      >
        {bars.map((bar, index) => {
          const label = `${bar.calls > 1 ? `${bar.calls} calls ` : ""}${timeOf(bar.from)}: ${formatTokens(bar.tokens)} tokens, ${
            bar.cost === null ? "cost unknown" : `${formatUSD(bar.cost)} est.`
          }, ${bar.model}`;
          return (
            <div
              key={`${bar.from}-${index}`}
              role="listitem"
              tabIndex={0}
              aria-label={label}
              onMouseEnter={() => setActive(index)}
              onFocus={() => setActive(index)}
              onBlur={() => setActive(null)}
              className="group flex h-full min-w-[2px] flex-1 items-end outline-none"
            >
              <div
                className={cn("w-full rounded-t-[2px] bg-accent transition-opacity", active !== null && active !== index && "opacity-40", "group-focus-visible:ring-2 group-focus-visible:ring-accent")}
                style={{ height: `${Math.max(1, (bar.tokens / peak) * HEIGHT)}px` }}
              />
            </div>
          );
        })}
      </div>
      <p aria-live="polite" className="min-h-4 truncate text-2xs text-body">
        {shown
          ? `${shown.calls > 1 ? `${shown.calls} calls, ` : ""}${timeOf(shown.from)}${shown.calls > 1 ? `–${timeOf(shown.to)}` : ""} · ${formatTokens(shown.tokens)} tokens · ${
              shown.cost === null ? "cost unknown" : `${formatUSD(shown.cost)} est.`
            } · ${shown.model}`
          : "Point at a bar for the call."}
      </p>
    </figure>
  );
}
