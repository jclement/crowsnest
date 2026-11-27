// LimitBanner: when an agent hits its plan's usage limit, every agent on that account will
// (DESIGN.md 6.17). One banner with the reset countdown replaces a stuck card per agent: new
// starts, nudges, retries and pokes for that engine wait, and resume in order after the reset.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Hourglass } from "lucide-react";
import { useEffect, useState } from "react";
import { resetCountdown } from "../../lib/accept";
import { endpoints, queryKeys } from "../../lib/api";
import { useLimits } from "../../lib/queries";
import { Button } from "../ui/Button";
import { useToast } from "../ui/toast-context";

export function LimitBanner() {
  const limits = useLimits();
  const client = useQueryClient();
  const toast = useToast();
  // The human's Clear hold (R6-S3): the limit is text agents report, so the human can always end it.
  const clear = useMutation({
    mutationFn: endpoints.clearLimit,
    onSuccess: (result) => {
      void client.invalidateQueries({ queryKey: queryKeys.limits });
      toast.show({ tone: "success", title: `Cleared ${result.engine}'s usage limit`, description: `${result.released} job(s) let go; crowsnest resumes them one at a time.` });
    },
    onError: (error) => toast.show({ tone: "danger", title: "Not cleared", description: error.message }),
  });
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 15_000);
    return () => window.clearInterval(timer);
  }, []);
  const list = limits.data ?? [];
  if (list.length === 0) return null;
  return (
    <>
      {list.map((limit) => (
        <div
          key={limit.engine}
          role="status"
          aria-label={`${limit.engine} usage limit`}
          className="flex flex-wrap items-center gap-2 border-b border-warning/40 bg-warning/15 px-3 py-1.5 text-sm text-heading"
        >
          <Hourglass className="h-4 w-4 shrink-0 text-warning" aria-hidden />
          <span className="min-w-0 flex-1">
            <strong className="font-semibold">
              {limit.engine} usage limit: {resetCountdown(limit, now)}
              {!limit.resuming && ` (${new Date(limit.reset_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}${limit.guessed ? ", a guess" : ""})`}.
            </strong>{" "}
            <span className="text-body">
              {limit.held} job{limit.held === 1 ? "" : "s"} waiting; new {limit.engine} starts, nudges and retries are held and resume in order. First seen on {limit.job}.
            </span>
          </span>
          <Button compact variant="secondary" disabled={clear.isPending} onClick={() => clear.mutate(limit.engine)}>
            Clear hold
          </Button>
        </div>
      ))}
    </>
  );
}
