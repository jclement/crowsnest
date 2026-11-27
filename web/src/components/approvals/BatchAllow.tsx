// BatchAllow: "Allow N low-risk calls from <agent>" under an approval's card in a tray row (DESIGN.md
// 8.7, Policy), offered when two or more low-risk approvals from the same job are pending. The
// first tap lists the N calls; only "Allow all N" sends them (POST /approvals/batch-allow), and the
// server refuses the whole batch if any is no longer pending, not low risk or from another job.
import { CheckCheck } from "lucide-react";
import { useState } from "react";
import { requester, type Approval } from "../../lib/approvals";
import { batchable, useBatchAllow } from "../../lib/policy";
import { useApprovals } from "../../lib/queries";
import { Button } from "../ui/Button";
import { useToast } from "../ui/toast-context";

export function BatchAllow({ approval, onDone }: { approval: Approval; onDone?: () => void }) {
  const pending = useApprovals("pending");
  const batch = useBatchAllow();
  const toast = useToast();
  const [confirming, setConfirming] = useState(false);
  const calls = approval.risk === "low" ? batchable(pending.data ?? [], approval.job_id) : [];
  if (calls.length < 2) return null;
  const who = requester(approval);
  const allowAll = () =>
    batch.mutateAsync(calls.map((call) => call.id)).then(
      (allowed) => {
        toast.show({ tone: "success", title: `Allowed ${allowed.length} calls from ${who}` });
        setConfirming(false);
        onDone?.();
      },
      (error: Error) => toast.show({ tone: "danger", title: "Nothing was allowed", description: error.message }),
    );
  if (!confirming) {
    return (
      <Button compact variant="secondary" icon={CheckCheck} onClick={() => setConfirming(true)} className="self-start">
        Allow {calls.length} low-risk calls from {who}
      </Button>
    );
  }
  return (
    <div role="group" aria-label={`Allow ${calls.length} calls from ${who}`} className="flex flex-col gap-2 rounded border border-border-strong bg-surface-raised p-2.5">
      <p className="text-xs font-semibold text-heading">
        Allow these {calls.length} calls from {who}{approval.job_id ? ` (${approval.job_id})` : ""}, once each?
      </p>
      <ul className="flex flex-col gap-0.5" aria-label="Calls in the batch">
        {calls.map((call) => (
          <li key={call.id} className="break-words font-mono text-2xs text-body">
            {call.summary}
          </li>
        ))}
      </ul>
      <div className="flex flex-wrap gap-2">
        <Button variant="primary" icon={CheckCheck} disabled={batch.isPending} onClick={() => void allowAll()}>
          Allow all {calls.length}
        </Button>
        <Button variant="secondary" disabled={batch.isPending} onClick={() => setConfirming(false)}>
          Cancel
        </Button>
      </div>
    </div>
  );
}
