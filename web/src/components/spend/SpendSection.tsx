// SpendSection: the job detail's Spend card (DESIGN.md 7.11): one figure, the estimated cost (R8:
// it is an estimate, not a bill, so the detail is not the point). "Details" opens the breakdown
// (tokens in, out, cache write, cache read; model calls, tool calls, wall time), the share of the
// prompt served from cache as a small meter, the models used, and the per-call chart. A job
// crowsnest cannot measure says why instead of showing zeros.
import { ChevronRight, Coins } from "lucide-react";
import { costText, formatDuration, formatShare, formatTokens, notMeasuredText, SPEND_NOTE, type SpendSummary, type SpendTurn } from "../../lib/spend";
import { Card } from "../ui/Card";
import { TurnBars } from "./TurnBars";

export function SpendSection({ spend, turns }: { spend: SpendSummary | undefined; turns?: SpendTurn[] }) {
  if (!spend) return null;
  return (
    <Card
      aria-label="Spend"
      title={
        <>
          <Coins className="h-3.5 w-3.5 text-muted" aria-hidden />
          Spend
        </>
      }
      actions={spend.measured ? <span className="font-mono text-xs text-heading">{costText(spend)}</span> : undefined}
    >
      {spend.measured ? <Measured spend={spend} turns={turns ?? []} /> : <p className="text-xs text-muted">{notMeasuredText(spend)}</p>}
    </Card>
  );
}

function Measured({ spend, turns }: { spend: SpendSummary; turns: SpendTurn[] }) {
  if (spend.turns === 0 && spend.total_tokens === 0) return <p className="text-xs text-muted">No model calls recorded yet.</p>;
  return (
    <details className="group text-xs">
      <summary className="flex min-h-11 cursor-pointer list-none items-center gap-1 text-muted hover:text-heading md:min-h-7 [&::-webkit-details-marker]:hidden">
        <ChevronRight className="h-3.5 w-3.5 transition-transform group-open:rotate-90" aria-hidden />
        Details
        <span className="ml-1 text-2xs">(tokens, calls, cache, models)</span>
      </summary>
      <div className="pt-2">
        <Breakdown spend={spend} turns={turns} />
      </div>
    </details>
  );
}

function Breakdown({ spend, turns }: { spend: SpendSummary; turns: SpendTurn[] }) {
  const figures: [string, string][] = [
    ["Input", formatTokens(spend.input_tokens)],
    ["Output", formatTokens(spend.output_tokens)],
    ["Cache write", formatTokens(spend.cache_write_tokens)],
    ["Cache read", formatTokens(spend.cache_read_tokens)],
    ["Model calls", String(spend.turns)],
    ["Tool calls", String(spend.tool_calls)],
  ];
  if (spend.wall_seconds > 0) figures.push(["Wall time", formatDuration(spend.wall_seconds)]);
  return (
    <div className="flex flex-col gap-3">
      <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs sm:grid-cols-4">
        {figures.map(([name, value]) => (
          <div key={name} className="flex min-w-0 items-baseline justify-between gap-2 border-b border-border py-0.5">
            <dt className="text-muted">{name}</dt>
            <dd className="font-mono text-heading">{value}</dd>
          </div>
        ))}
      </dl>
      <CacheMeter share={spend.cache_hit_share} />
      <TurnBars turns={turns} />
      <p className="text-2xs text-muted">
        {spend.models.length > 0 && <>Models: {spend.models.join(", ")}. </>}
        {spend.unpriced_tokens > 0 && <>{formatTokens(spend.unpriced_tokens)} tokens are from a model with no known price. </>}
        {SPEND_NOTE}
      </p>
    </div>
  );
}

/** The cache-hit share against the whole prompt: the fill and its track are one hue. */
function CacheMeter({ share }: { share: number }) {
  const percent = Math.round(Math.min(1, Math.max(0, share)) * 100);
  return (
    <div className="flex items-center gap-2 text-2xs text-muted">
      <span className="shrink-0">Prompt from cache</span>
      <div
        role="meter"
        aria-label="Share of the prompt read from the cache"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent}
        className="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-accent/20"
      >
        <div className="h-full rounded-full bg-accent" style={{ width: `${percent}%` }} />
      </div>
      <span className="w-8 shrink-0 text-right font-mono text-heading">{formatShare(share)}</span>
    </div>
  );
}
