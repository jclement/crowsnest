// LimitsSheet: the throttle (DESIGN.md 6.12): how many job agents may run at once (waiting jobs
// start in order), the load hold, and who holds the heavy-command slots right now.
import { useState } from "react";
import { relativeTime } from "../../lib/format";
import { useThrottle } from "../../lib/queries";
import { SettingsSheet } from "../system/SettingsSheet";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Sheet } from "../ui/Sheet";
import { HelpButton } from "../help/HelpButton";

export function LimitsSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const throttle = useThrottle(open);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const data = throttle.data;
  return (
    <Sheet open={open} onClose={onClose} title="Limits" size="md" headerExtra={<HelpButton topic="concepts/budgets-limits-pause#limits-on-running-jobs" what="the running-job limits" inline={false} />}>
      <div className="flex flex-col gap-3 p-4 text-sm">
        {data && (
          <>
            <Card title="Running jobs">
              <p className="mb-2 text-body">
                {data.queue ? `${data.queue.running} of ${data.queue.limit} running, ${data.queue.waiting} waiting.` : ""} Leads and the orchestrator are not
                counted. Load now {data.load.per_core.toFixed(1)} per core on {data.load.cores} cores{data.load.holding ? `: ${data.load.reason}` : ""}.
              </p>
              <p className="text-xs text-muted">
                Limit {data.settings.max_running} at once · hold above load {data.settings.load_per_core || "off"} per core. These are Settings (with the
                usual precedence: a flag or variable locks them).
              </p>
              <Button compact className="mt-2 self-start" onClick={() => setSettingsOpen(true)}>
                Change in Settings
              </Button>
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
      <SettingsSheet open={settingsOpen} onClose={() => setSettingsOpen(false)} />
    </Sheet>
  );
}
