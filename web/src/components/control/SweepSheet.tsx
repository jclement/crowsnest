// SweepSheet: finished jobs whose worktrees are still around (DESIGN.md 6.11), oldest first with
// their age, so nothing lingers; open one to land it, or discard several at once after a
// confirmation. A dirty worktree is never forced: its row says so and its discard is refused.
import { useState } from "react";
import { ageText } from "../../lib/landing";
import { landedWord } from "../../lib/jobs";
import { useSweep, useSweepDiscard } from "../../lib/queries";
import { Button } from "../ui/Button";
import { Chip } from "../ui/Chip";
import { EmptyState } from "../ui/EmptyState";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";
import { FolderGit2 } from "lucide-react";

export function SweepSheet({ open, onClose, onOpenJob }: { open: boolean; onClose: () => void; onOpenJob: (id: string) => void }) {
  const sweep = useSweep(open);
  const discard = useSweepDiscard();
  const toast = useToast();
  const [chosen, setChosen] = useState<string[]>([]);
  const [confirming, setConfirming] = useState(false);
  const items = sweep.data ?? [];
  const toggle = (id: string) => setChosen((list) => (list.includes(id) ? list.filter((item) => item !== id) : [...list, id]));
  return (
    <Sheet open={open} onClose={onClose} title="Worktrees still around">
      <div className="flex flex-col gap-2 p-3 text-sm">
        {items.length === 0 ? (
          <EmptyState icon={FolderGit2} title="Nothing lingering">Every finished job's worktree has been landed or discarded.</EmptyState>
        ) : (
          <>
            <ul aria-label="Finished jobs with worktrees" className="divide-y divide-border rounded border border-border">
              {items.map((item) => (
                <li key={item.job.id} className="flex items-center gap-2 px-2 py-1.5">
                  <input type="checkbox" aria-label={`Choose ${item.job.id}`} checked={chosen.includes(item.job.id)} onChange={() => toggle(item.job.id)} />
                  <button type="button" className="min-w-0 flex-1 truncate text-left hover:underline" onClick={() => onOpenJob(item.job.id)}>
                    <span className="font-mono text-2xs text-muted">{item.job.id}</span> {item.job.title}
                  </button>
                  {item.job.land_state && <Chip>{landedWord(item.job.land_state)}</Chip>}
                  {item.dirty && <Chip tone="warning">uncommitted</Chip>}
                  <Chip>{item.job.state}</Chip>
                  <span className="w-12 shrink-0 text-right text-2xs text-muted">{ageText(item.age_seconds)}</span>
                </li>
              ))}
            </ul>
            {!confirming ? (
              <div className="flex gap-2">
                <Button compact variant="danger" disabled={chosen.length === 0} onClick={() => setConfirming(true)}>
                  Discard {chosen.length || ""} chosen
                </Button>
                <Button compact variant="ghost" onClick={() => setChosen(items.map((item) => item.job.id))}>
                  Choose all
                </Button>
              </div>
            ) : (
              <div role="alertdialog" aria-label="Confirm discard" className="rounded border border-danger/40 bg-danger/10 p-2 text-sm">
                <p className="text-body">
                  Discard {chosen.length} job(s): stop their agents, remove their worktrees and workspaces, and delete their branches (a landed job keeps
                  its branch). A worktree with uncommitted changes is refused, not forced. This cannot be undone.
                </p>
                <div className="mt-2 flex gap-2">
                  <Button
                    compact
                    variant="danger"
                    disabled={discard.isPending}
                    onClick={() =>
                      discard.mutate(chosen, {
                        onSuccess: (results) => {
                          const failed = results.filter((result) => !result.ok || result.error);
                          toast.show(
                            failed.length
                              ? { tone: "warning", title: `${results.length - failed.length} discarded, ${failed.length} not`, description: failed.map((f) => `${f.id}: ${f.error}`).join("; ") }
                              : { tone: "success", title: `${results.length} discarded` },
                          );
                          setChosen([]);
                          setConfirming(false);
                        },
                        onError: (error) => toast.show({ tone: "danger", title: "Not discarded", description: error.message }),
                      })
                    }
                  >
                    Yes, discard them
                  </Button>
                  <Button compact variant="ghost" onClick={() => setConfirming(false)}>
                    Not now
                  </Button>
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </Sheet>
  );
}
