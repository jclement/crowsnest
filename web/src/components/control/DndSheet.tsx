// DndSheet: presence (here / maybe / away, DESIGN.md 6.14) and "do not disturb until HH:MM",
// which holds every non-blocking push and --notify-url notice and sends one summary at the end.
import { useState } from "react";
import { nextClock } from "../../lib/landing";
import { usePresence, useSetDND } from "../../lib/queries";
import { Button } from "../ui/Button";
import { Chip } from "../ui/Chip";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";

export function DndSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const presence = usePresence(open);
  const set = useSetDND();
  const toast = useToast();
  const [until, setUntil] = useState("");
  const dnd = presence.data?.dnd_until;
  const apply = (at: string) =>
    set.mutate(at, {
      onSuccess: () => toast.show({ tone: "success", title: at ? "Do not disturb is on" : "Do not disturb is off" }),
      onError: (error) => toast.show({ tone: "danger", title: "Not changed", description: error.message }),
    });
  return (
    <Sheet open={open} onClose={onClose} title="Presence and do not disturb" size="md">
      <div className="flex flex-col gap-3 p-4 text-sm">
        {presence.data && (
          <p className="flex flex-wrap items-center gap-2 text-body">
            <Chip tone={presence.data.presence === "here" ? "success" : presence.data.presence === "maybe" ? "warning" : "neutral"}>{presence.data.presence}</Chip>
            {presence.data.reason}
          </p>
        )}
        <p className="text-xs text-muted">
          Here: things reach you in the tray, never as a push. Away: Web Push and the notify URL. Only these coarse states are kept, in memory.
        </p>
        {dnd ? (
          <div className="flex items-center gap-2">
            <span className="text-body">Do not disturb until {new Date(dnd).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}.</span>
            <Button compact onClick={() => apply("")}>Turn off</Button>
          </div>
        ) : (
          <form
            className="flex items-end gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              const at = nextClock(until);
              if (!at) return toast.show({ tone: "danger", title: "Give the time as HH:MM" });
              apply(at);
            }}
          >
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-heading">Do not disturb until (HH:MM)</span>
              <input value={until} onChange={(event) => setUntil(event.target.value)} placeholder="08:00" className="h-9 w-28 rounded border border-border bg-surface px-2 text-body" />
            </label>
            <Button type="submit" disabled={set.isPending}>Hold notifications</Button>
          </form>
        )}
        <p className="text-xs text-muted">Blocking questions and dangerous approvals still get through.</p>
      </div>
    </Sheet>
  );
}
