// WaitsCard: the System page's list of live jobs that break "every wait has an owner and a
// wake-up" (DESIGN.md 6.18). Empty is the healthy state, and says so.
import { Link } from "@tanstack/react-router";
import { AlarmClockOff } from "lucide-react";
import { relativeTime } from "../../lib/format";
import { useWaits } from "../../lib/queries";
import { Card } from "../ui/Card";
import { Chip } from "../ui/Chip";

export function WaitsCard() {
  const waits = useWaits();
  const list = waits.data?.violations ?? [];
  return (
    <Card
      aria-label="Waits without an owner"
      tone={list.length > 0 ? "warning" : "default"}
      title={
        <>
          <AlarmClockOff className="h-4 w-4" aria-hidden />
          Waits without an owner
        </>
      }
      actions={<Chip tone={list.length > 0 ? "warning" : "success"}>{list.length}</Chip>}
    >
      {waits.isPending ? (
        <p className="text-xs text-muted">Checking…</p>
      ) : list.length === 0 ? (
        <p className="text-xs text-muted">
          Every live job has an owner and a wake-up{waits.data?.checked_at ? ` (checked ${relativeTime(waits.data.checked_at)})` : ""}.
        </p>
      ) : (
        <ul className="flex flex-col gap-1 text-xs">
          {list.map((violation) => (
            <li key={violation.job_id} className="flex flex-wrap items-center gap-1.5">
              <Link to="/" search={{ job: violation.job_id }} className="font-mono text-accent underline">
                {violation.job_id}
              </Link>
              <span className="text-body">{violation.title}</span>
              <Chip>{violation.state}</Chip>
              <span className="text-warning">{violation.rule}</span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
