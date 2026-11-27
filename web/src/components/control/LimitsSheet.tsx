// LimitsSheet: the throttle (DESIGN.md 6.12): how many job agents may run at once (waiting jobs
// start in order), the load hold, and who holds the heavy-command slots right now.
import { useState } from "react";
import { relativeTime } from "../../lib/format";
import { useSaveThrottle, useThrottle } from "../../lib/queries";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";

export function LimitsSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const throttle = useThrottle(open);
  const save = useSaveThrottle();
  const toast = useToast();
  // null: not edited, so the field shows the saved value.
  const [maxEdit, setMax] = useState<string | null>(null);
  const [loadEdit, setLoad] = useState<string | null>(null);
  const data = throttle.data;
  const max = maxEdit ?? String(data?.settings.max_running ?? "");
  const load = loadEdit ?? String(data?.settings.load_per_core ?? "");
  return (
    <Sheet open={open} onClose={onClose} title="Limits" size="md">
      <div className="flex flex-col gap-3 p-4 text-sm">
        {data && (
          <>
            <Card title="Running jobs">
              <p className="mb-2 text-body">
                {data.queue ? `${data.queue.running} of ${data.queue.limit} running, ${data.queue.waiting} waiting.` : ""} Leads and the orchestrator are not
                counted. Load now {data.load.per_core.toFixed(1)} per core on {data.load.cores} cores{data.load.holding ? `: ${data.load.reason}` : ""}.
              </p>
              <form
                className="flex flex-wrap items-end gap-2"
                onSubmit={(event) => {
                  event.preventDefault();
                  save.mutate(
                    { max_running: Number(max), load_per_core: Number(load), project_limits: data.settings.project_limits ?? {} },
                    {
                      onSuccess: () => {
                        setMax(null);
                        setLoad(null);
                        toast.show({ tone: "success", title: "Limits saved" });
                      },
                      onError: (error) => toast.show({ tone: "danger", title: "Not saved", description: error.message }),
                    },
                  );
                }}
              >
                <label className="flex flex-col gap-1">
                  <span className="text-xs font-medium text-heading">At most</span>
                  <input aria-label="Running-job limit" value={max} onChange={(event) => setMax(event.target.value)} inputMode="numeric" className="h-9 w-20 rounded border border-border bg-surface px-2" />
                </label>
                <label className="flex flex-col gap-1">
                  <span className="text-xs font-medium text-heading">Hold starts above load per core (0 off)</span>
                  <input aria-label="Load hold" value={load} onChange={(event) => setLoad(event.target.value)} inputMode="decimal" className="h-9 w-24 rounded border border-border bg-surface px-2" />
                </label>
                <Button type="submit" disabled={save.isPending}>Save</Button>
                <Button variant="ghost" onClick={() => save.mutate(null)} title={`Back to ${data.defaults.max_running} running, load ${data.defaults.load_per_core}`}>
                  Use the defaults
                </Button>
              </form>
            </Card>
            <Card title={`Heavy commands (${data.heavy.slots} slots)`}>
              {data.heavy.deferred && <p className="mb-1 text-xs text-muted">`crowsnest heavy` hands commands to {data.heavy.deferred}, whose slots are its own.</p>}
              {data.heavy.running.length === 0 && data.heavy.waiting.length === 0 ? (
                <p className="text-xs text-muted">No heavy command running.</p>
              ) : (
                <ul className="flex flex-col gap-1 text-xs">
                  {data.heavy.running.map((owner) => (
                    <li key={`r${owner.pid}`}>
                      <span className="font-mono">slot {owner.slot}</span> {owner.command} {owner.job && <span className="text-muted">({owner.job})</span>}{" "}
                      <span className="text-muted">since {relativeTime(owner.since)}</span>
                    </li>
                  ))}
                  {data.heavy.waiting.map((owner) => (
                    <li key={`w${owner.pid}`} className="text-warning">
                      waiting: {owner.command} {owner.job && `(${owner.job})`}
                    </li>
                  ))}
                </ul>
              )}
            </Card>
          </>
        )}
      </div>
    </Sheet>
  );
}
