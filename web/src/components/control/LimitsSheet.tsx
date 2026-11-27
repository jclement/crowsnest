// Settings → Limits: the throttle (DESIGN.md 6.12) as it stands: how many job agents run and may
// run at once (waiting jobs start in order), the load hold, and who holds the heavy-command slots
// right now. The settings that bound them sit just below it in the Settings view.
import { relativeTime } from "../../lib/format";
import { useThrottle } from "../../lib/queries";
import { Card } from "../ui/Card";

/** The throttle's live state. */
export function LimitsBody() {
  const throttle = useThrottle(true);
  const data = throttle.data;
  return (
      <div className="flex flex-col gap-3 text-sm">
        {data && (
          <>
            <Card title="Running jobs">
              <p className="mb-2 text-body">
                {data.queue ? `${data.queue.running} of ${data.queue.limit} running, ${data.queue.waiting} waiting.` : ""} Leads and the orchestrator are not
                counted. Load now {data.load.per_core.toFixed(1)} per core on {data.load.cores} cores{data.load.holding ? `: ${data.load.reason}` : ""}.
              </p>
              <p className="text-xs text-muted">
                Limit {data.settings.max_running} at once · hold above load {data.settings.load_per_core || "off"} per core: the settings below (with the
                usual precedence: a flag or variable locks them).
              </p>
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
  );
}
