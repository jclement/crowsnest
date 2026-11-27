// AcceptCard: a project's acceptance and budget defaults (DESIGN.md 6.15, 6.16): the check
// crowsnest runs on a finished job's worktree (empty: automatic, `mise run check` when the
// checkout's mise.toml has that task; "none": no check), and the dollars and minutes its jobs
// may use. It shows which check would run now and why.
import { ShieldCheck } from "lucide-react";
import { useState } from "react";
import type { ProjectAccept } from "../../lib/accept";
import { useProjectAccept, useSetProjectAccept } from "../../lib/queries";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { useToast } from "../ui/toast-context";

export function AcceptCard({ projectId }: { projectId: number }) {
  const query = useProjectAccept(projectId);
  // The form starts from the saved values; keyed so a save (or another tab's) starts it afresh.
  const key = query.data
    ? [query.data.check_command, query.data.budget_usd, query.data.budget_minutes, query.data.restored_check_command, query.data.check_isolated, query.data.check_prepare, query.data.restored_check_prepare].join("|")
    : "loading";
  return <AcceptForm key={key} projectId={projectId} data={query.data} loading={query.isPending} />;
}

function AcceptForm({ projectId, data, loading }: { projectId: number; data?: ProjectAccept; loading: boolean }) {
  const save = useSetProjectAccept(projectId);
  const toast = useToast();
  const [command, setCommand] = useState(data?.check_command ?? "");
  const [usd, setUsd] = useState(data?.budget_usd ? String(data.budget_usd) : "");
  const [minutes, setMinutes] = useState(data?.budget_minutes ? String(data.budget_minutes) : "");
  const [isolated, setIsolated] = useState(data?.check_isolated ?? false);
  const [prepare, setPrepare] = useState(data?.check_prepare ?? "");
  const submit = (checkCommand: string, prepareCommand = prepare.trim()) =>
    save.mutate(
      { check_command: checkCommand, budget_usd: Number(usd) || 0, budget_minutes: Number(minutes) || 0, check_isolated: isolated, check_prepare: prepareCommand },
      {
        onSuccess: () => toast.show({ tone: "success", title: "Saved" }),
        onError: (error) => toast.show({ tone: "danger", title: "Not saved", description: error.message }),
      },
    );
  return (
    <Card
      aria-label="Acceptance and budget"
      title={
        <>
          <ShieldCheck className="h-4 w-4" aria-hidden />
          Acceptance and budget
        </>
      }
    >
      <form
        className="flex flex-col gap-2 text-xs"
        onSubmit={(event) => {
          event.preventDefault();
          submit(command.trim());
        }}
      >
        {(data?.restored_check_command || data?.restored_check_prepare) && (
          <div role="alert" className="flex flex-col gap-1 rounded border border-danger/40 bg-danger/10 p-2 text-body">
            <p>
              <strong>A restore brought {data.restored_check_command && data.restored_check_prepare ? "these commands" : "this command"}, and crowsnest does not run{" "}
              {data.restored_check_command && data.restored_check_prepare ? "them" : "it"}</strong> until you say so (they run on every finished job, and a
              backup is not your word on this machine). Until then the check is automatic.
            </p>
            {data.restored_check_command && (
              <code data-testid="restored-check-command" className="break-all font-mono">check: {data.restored_check_command}</code>
            )}
            {data.restored_check_prepare && (
              <code data-testid="restored-check-prepare" className="break-all font-mono">prepare: {data.restored_check_prepare}</code>
            )}
            <div className="flex flex-wrap gap-2">
              <Button
                compact
                variant="secondary"
                disabled={save.isPending}
                onClick={() => submit(data.restored_check_command || command.trim(), data.restored_check_prepare || prepare.trim())}
              >
                {data.restored_check_command && data.restored_check_prepare ? "Use these commands" : "Use this command"}
              </Button>
              <Button compact variant="secondary" disabled={save.isPending} onClick={() => submit(command.trim())}>
                Keep the current check
              </Button>
            </div>
          </div>
        )}
        <label className="flex flex-col gap-0.5">
          <span className="text-muted">Check command (empty: automatic; "none": no check)</span>
          <input aria-label="Check command" value={command} onChange={(event) => setCommand(event.target.value)} placeholder="mise run check"
            className="h-9 rounded border border-border bg-surface px-2 font-mono text-body" />
        </label>
        <label className="flex items-start gap-2">
          <input type="checkbox" aria-label="Isolated check" checked={isolated} onChange={(event) => setIsolated(event.target.checked)} className="mt-0.5" />
          <span className="text-muted">
            <span className="text-body">Isolated check</span>: run it in a temporary copy of exactly the commit being accepted, not the job's working
            tree. Slower, and the copy has no ignored files (dependencies, caches, build output) until the prepare command makes them; but nothing the
            agent changes or leaves running can touch it.
          </span>
        </label>
        {isolated && (
          <label className="flex flex-col gap-0.5">
            <span className="text-muted">Prepare command, run in the copy first (empty: none)</span>
            <input aria-label="Prepare command" value={prepare} onChange={(event) => setPrepare(event.target.value)} placeholder="npm ci"
              className="h-9 rounded border border-border bg-surface px-2 font-mono text-body" />
          </label>
        )}
        {data && (
          <p className="text-muted">
            Runs now: {data.effective ? <code className="font-mono text-body">{data.effective}</code> : <span className="text-warning">nothing</span>} ({data.effective_source}).
            Jobs with no check finish as "unverified".
          </p>
        )}
        <div className="flex flex-wrap items-end gap-2">
          <label className="flex flex-col gap-0.5">
            <span className="text-muted">Budget per job ($)</span>
            <input aria-label="Project budget in dollars" inputMode="decimal" value={usd} onChange={(event) => setUsd(event.target.value)} placeholder="default"
              className="h-9 w-28 rounded border border-border bg-surface px-2 text-body" />
          </label>
          <label className="flex flex-col gap-0.5">
            <span className="text-muted">Budget per job (minutes)</span>
            <input aria-label="Project budget in minutes" inputMode="numeric" value={minutes} onChange={(event) => setMinutes(event.target.value)} placeholder="default"
              className="h-9 w-28 rounded border border-border bg-surface px-2 text-body" />
          </label>
          <Button compact variant="primary" type="submit" disabled={save.isPending || loading}>
            Save
          </Button>
        </div>
      </form>
    </Card>
  );
}
