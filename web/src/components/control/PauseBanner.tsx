// PauseBanner: while crowsnest's automation is paused (DESIGN.md 6.13), a clear banner on every
// view says who paused it, when, until when, and why, with Resume. Agents keep running; only
// crowsnest's own pokes, nudges, scheduled tasks, starts and restarts wait.
import { PauseCircle } from "lucide-react";
import { pauseLine } from "../../lib/landing";
import { usePause, useSetPause } from "../../lib/queries";
import { Button } from "../ui/Button";
import { useToast } from "../ui/toast-context";

export function PauseBanner() {
  const pause = usePause();
  const set = useSetPause();
  const toast = useToast();
  if (!pause.data?.paused) return null;
  return (
    <div role="status" aria-label="Automation paused" className="flex flex-wrap items-center gap-2 border-b border-warning/40 bg-warning/15 px-3 py-1.5 text-sm text-heading">
      <PauseCircle className="h-4 w-4 shrink-0 text-warning" aria-hidden />
      <span className="min-w-0 flex-1">
        <strong className="font-semibold">{pauseLine(pause.data)}.</strong>{" "}
        <span className="text-body">Agents keep working; no pokes, nudges, scheduled tasks, new starts or restarts until you resume.</span>
        {pause.data.file_exists && <span className="block text-2xs text-muted">The paused file {pause.data.file} holds it; Resume removes it.</span>}
      </span>
      <Button
        compact
        variant="primary"
        disabled={set.isPending}
        onClick={() =>
          set.mutate({ paused: false }, { onError: (error) => toast.show({ tone: "danger", title: "Not resumed", description: error.message }) })
        }
      >
        Resume
      </Button>
    </div>
  );
}
