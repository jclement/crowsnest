// Shown while the event stream is down, so a stale screen is never mistaken for a quiet one.
import { WifiOff } from "lucide-react";
import type { StreamStatus } from "../lib/sse";

export function ConnectionBanner({ status }: { status: StreamStatus }) {
  if (status !== "reconnecting") return null;
  return (
    <div role="status" className="flex items-center gap-2 border-b border-border bg-warning/10 px-3 py-1 text-xs text-warning">
      <WifiOff className="h-4 w-4" aria-hidden />
      Connection lost. Reconnecting…
    </div>
  );
}
