// LimitBanner: when an agent hits its plan's usage limit, every agent on that account will
// (DESIGN.md 6.17). One banner with the reset countdown replaces a stuck card per agent: new
// starts, nudges, retries and pokes for that engine wait, and resume in order after the reset.
import { Hourglass } from "lucide-react";
import { useEffect, useState } from "react";
import { resetCountdown } from "../../lib/accept";
import { useLimits } from "../../lib/queries";

export function LimitBanner() {
  const limits = useLimits();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 15_000);
    return () => window.clearInterval(timer);
  }, []);
  const list = limits.data ?? [];
  if (list.length === 0) return null;
  return (
    <>
      {list.map((limit) => (
        <div
          key={limit.engine}
          role="status"
          aria-label={`${limit.engine} usage limit`}
          className="flex flex-wrap items-center gap-2 border-b border-warning/40 bg-warning/15 px-3 py-1.5 text-sm text-heading"
        >
          <Hourglass className="h-4 w-4 shrink-0 text-warning" aria-hidden />
          <span className="min-w-0 flex-1">
            <strong className="font-semibold">
              {limit.engine} usage limit: {resetCountdown(limit, now)}
              {!limit.resuming && ` (${new Date(limit.reset_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}${limit.guessed ? ", a guess" : ""})`}.
            </strong>{" "}
            <span className="text-body">
              {limit.held} job{limit.held === 1 ? "" : "s"} waiting; new {limit.engine} starts, nudges and retries are held and resume in order. First seen on {limit.job}.
            </span>
          </span>
        </div>
      ))}
    </>
  );
}
