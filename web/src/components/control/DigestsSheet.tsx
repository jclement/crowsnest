// DigestsSheet: the daily digests, newest first (DESIGN.md 7.13), with "Digest now".
import { Newspaper } from "lucide-react";
import { useDigestNow, useDigests } from "../../lib/queries";
import { Markdown } from "../Markdown";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Chip } from "../ui/Chip";
import { EmptyState } from "../ui/EmptyState";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";

export function DigestsSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const digests = useDigests(open);
  const now = useDigestNow();
  const toast = useToast();
  return (
    <Sheet
      open={open}
      onClose={onClose}
      title="Digests"
      headerExtra={
        <Button
          compact
          disabled={now.isPending}
          onClick={() =>
            now.mutate(undefined, {
              onSuccess: (result) => toast.show({ tone: "success", title: "Digest on its way", description: result.note }),
              onError: (error) => toast.show({ tone: "danger", title: "No digest", description: error.message }),
            })
          }
        >
          Digest now
        </Button>
      }
    >
      <div className="flex flex-col gap-3 p-3">
        {(digests.data ?? []).length === 0 ? (
          <EmptyState icon={Newspaper} title="No digests yet">Turn the daily digest on in Schedule, or write one now.</EmptyState>
        ) : (
          (digests.data ?? []).map((digest) => (
            <Card key={digest.id} title={digest.day} actions={<Chip>{digest.source === "orchestrator" ? "by the orchestrator" : "by crowsnest"}</Chip>}>
              <Markdown className="text-sm">{digest.body_md}</Markdown>
            </Card>
          ))
        )}
      </div>
    </Sheet>
  );
}
