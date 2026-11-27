// The session's heartbeat, in the header where the "Live" dot was (DESIGN.md "Design language").
// A short trace in the chrome's accent that beats once for every event the stream delivers
// (lib/live.ts BEAT_EVENT): machine samples every two seconds keep a live session beating
// steadily, and a stalled stream goes still. Reconnecting draws a flat line in the warning ink
// with a dash cut out of it; connecting, a flat line in the muted ink. The word beside it carries
// the state for screen readers and for anyone who cannot tell the traces apart.
import { useEffect, useState } from "react";
import { cn } from "../../lib/cn";
import { BEAT_EVENT } from "../../lib/live";
import type { StreamStatus } from "../../lib/sse";

const labels: Record<StreamStatus, string> = {
  open: "Live",
  connecting: "Connecting",
  reconnecting: "Offline",
};

export function Heartbeat({ status }: { status: StreamStatus }) {
  const [beat, setBeat] = useState(0);
  useEffect(() => {
    const onBeat = () => setBeat((count) => count + 1);
    window.addEventListener(BEAT_EVENT, onBeat);
    return () => window.removeEventListener(BEAT_EVENT, onBeat);
  }, []);
  return (
    <span
      role="status"
      title={status === "open" ? "Live updates connected: beats with every event" : "Live updates not connected"}
      data-status={status}
      className="inline-flex items-center gap-1.5 px-1 font-mono sm:mr-1 sm:px-1.5 text-2xs uppercase tracking-wider text-chrome-muted"
    >
      <svg aria-hidden viewBox="0 0 22 12" className={cn("h-3 w-3.5 shrink-0 overflow-visible sm:w-[22px]", status === "open" ? "text-chrome-accent" : status === "reconnecting" ? "text-chrome-warning" : "text-chrome-muted")}>
        {status === "open" ? (
          <polyline
            // Restarting the animation on each beat: a new key remounts the line.
            key={beat}
            className="origin-center animate-beat"
            points="0,7 6,7 8,3 11,11 13,1 15,7 22,7"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        ) : (
          <path d={status === "reconnecting" ? "M0 7h8M14 7h8" : "M0 7h22"} stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" className={status === "connecting" ? "animate-shimmer" : undefined} />
        )}
      </svg>
      <span className="hidden sm:inline">{labels[status]}</span>
      <span className="sr-only sm:hidden">{labels[status]}</span>
    </span>
  );
}
