// JobRulesCard: the rules remembered for one job ("Allow and remember for this job", DESIGN.md 8.7
// Policy), on the job's page, each with Remove. Nothing is drawn while the job has none. They stop
// applying when the job ends; the list then says so.
import { Trash2 } from "lucide-react";
import { relativeTime } from "../../lib/format";
import { useJobRules, useRemoveRule } from "../../lib/policy";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Chip } from "../ui/Chip";
import { useToast } from "../ui/toast-context";

export function JobRulesCard({ jobId, finished }: { jobId: string; finished: boolean }) {
  const rules = useJobRules(jobId);
  const remove = useRemoveRule();
  const toast = useToast();
  const list = rules.data ?? [];
  if (list.length === 0) return null;
  return (
    <Card title="Remembered for this job" aria-label="Remembered for this job" bodyClassName="p-0">
      <p className="px-3 pt-2 text-2xs text-muted">
        {finished ? "This job has ended, so these no longer allow anything." : `Until ${jobId} ends, crowsnest allows these without asking (never a dangerous call).`}
      </p>
      <ul className="divide-y divide-border">
        {list.map((rule) => (
          <li key={rule.id} data-rule={rule.id} className="flex flex-wrap items-center gap-2 px-3 py-1.5 text-xs">
            <span className="min-w-0 flex-1 break-words text-body">
              {rule.sentence.replaceAll("`", "")}
              {rule.max_risk === "elevated" && (
                <Chip tone="warning" className="ml-1.5">
                  also when elevated
                </Chip>
              )}
            </span>
            <span className="text-2xs text-muted">
              {rule.id} · {relativeTime(rule.created_at)}
            </span>
            {!finished && (
              <Button
                compact
                variant="ghost"
                icon={Trash2}
                disabled={remove.isPending}
                aria-label={`Remove ${rule.id}: ${rule.sentence}`}
                onClick={() =>
                  void remove.mutateAsync(rule.id).catch((error: Error) => toast.show({ tone: "danger", title: "Could not remove the rule", description: error.message }))
                }
              >
                Remove
              </Button>
            )}
          </li>
        ))}
      </ul>
    </Card>
  );
}
