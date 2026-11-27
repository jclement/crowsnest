// PauseSheet: pause the automation, optionally until a time, with a reason for the banner.
import { useState } from "react";
import { nextClock } from "../../lib/landing";
import { useSetPause } from "../../lib/queries";
import { Button } from "../ui/Button";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";

export function PauseSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [until, setUntil] = useState("");
  const [reason, setReason] = useState("");
  const set = useSetPause();
  const toast = useToast();
  const submit = () => {
    const at = until ? nextClock(until) : "";
    if (until && !at) return toast.show({ tone: "danger", title: "Give the time as HH:MM" });
    set.mutate(
      { paused: true, until: at, reason },
      {
        onSuccess: () => {
          toast.show({ tone: "success", title: "Automation paused" });
          onClose();
        },
        onError: (error) => toast.show({ tone: "danger", title: "Not paused", description: error.message }),
      },
    );
  };
  return (
    <Sheet open={open} onClose={onClose} title="Pause automation" size="md">
      <form
        className="flex flex-col gap-3 p-4 text-sm"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <p className="text-body">
          Agents keep running and you can still chat and answer. Crowsnest stops doing things by itself: no pokes, no nudges, no scheduled tasks, no
          new job starts, no restarts or retries. Agents may end their turns freely meanwhile.
        </p>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-heading">Resume by itself at (optional, HH:MM)</span>
          <input value={until} onChange={(event) => setUntil(event.target.value)} placeholder="18:30" inputMode="numeric"
            className="h-9 rounded border border-border bg-surface px-2 text-body" />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-heading">Reason (shown on the banner)</span>
          <input value={reason} onChange={(event) => setReason(event.target.value)} placeholder="deploy window"
            className="h-9 rounded border border-border bg-surface px-2 text-body" />
        </label>
        <div className="flex gap-2">
          <Button type="submit" variant="warning" disabled={set.isPending}>
            Pause
          </Button>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
        </div>
      </form>
    </Sheet>
  );
}
