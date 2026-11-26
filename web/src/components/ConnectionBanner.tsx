// The reconnect banner, driven by the event stream's state (lib/sse.ts). Shown while the stream
// is down so a stale screen is never mistaken for a quiet one, with a countdown to the next
// attempt and a button to try at once.
import { WifiOff } from "lucide-react";
import { useEffect, useState } from "react";
import { MAX_BACKOFF_MS, type StreamState } from "../lib/sse";
import { Banner } from "./ui/Banner";

export function ConnectionBanner({ stream }: { stream: StreamState }) {
  const seconds = useCountdown(stream.retryAt);
  if (stream.status !== "reconnecting") return null;
  return (
    <Banner
      tone="warning"
      icon={WifiOff}
      className="border-x-0 border-t-0"
      action={
        stream.retryAt !== null && (
          <button
            type="button"
            onClick={stream.retryNow}
            className="inline-flex min-h-9 items-center rounded px-2 font-medium underline-offset-2 hover:underline"
          >
            Retry now
          </button>
        )
      }
    >
      Connection lost. {stream.retryAt !== null ? `Reconnecting in ${seconds} s…` : "Reconnecting…"}
      {stream.attempt > 1 && <span className="opacity-75"> (attempt {stream.attempt})</span>}
    </Banner>
  );
}

/** Whole seconds until `at`, re-rendering each second; 0 once it has passed. */
function useCountdown(at: number | null): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (at === null) return;
    // A quarter-second tick keeps the number honest without a synchronous set in the effect.
    const timer = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(timer);
  }, [at]);
  return at === null ? 0 : Math.max(0, Math.min(Math.ceil((at - now) / 1_000), Math.ceil(MAX_BACKOFF_MS / 1_000)));
}
