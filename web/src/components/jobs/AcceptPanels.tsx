// The job view's acceptance, budget and wait panels (DESIGN.md 6.15 to 6.18): crowsnest's own
// evidence for a finished coding job (the check it ran, against which commit, its output) with
// "Run the check again" and "Ask an agent to rebase"; the budget against its limits with Raise,
// Let it finish this step and Stop; who owns the wait and what ends it, with Wake it; and what
// the agent last said when it ended a turn.
import { AlarmClock, Gauge, GitMerge, MessageSquareQuote, RefreshCw, ShieldCheck, Square, StepForward } from "lucide-react";
import { useState } from "react";
import { acceptChip, budgetRows, durationText } from "../../lib/accept";
import { relativeTime } from "../../lib/format";
import { isFinished, type JobDetail as Detail, type Job } from "../../lib/jobs";
import { useJobAction, useJobGuard, useSetBudget, type GuardAction } from "../../lib/queries";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Chip } from "../ui/Chip";
import { useToast } from "../ui/toast-context";
import { HelpButton } from "../help/HelpButton";

function useGuard(id: string) {
  const run = useJobGuard(id);
  const toast = useToast();
  const press = (action: GuardAction, done: string) =>
    run.mutate(action, {
      onSuccess: () => toast.show({ tone: "success", title: done }),
      onError: (error) => toast.show({ tone: "danger", title: "Not done", description: error.message }),
    });
  return { press, pending: run.isPending };
}

/** crowsnest's evidence: the check it ran itself, pinned to a commit. */
export function EvidenceCard({ job }: { job: Detail }) {
  const guard = useGuard(job.id);
  const chip = acceptChip(job);
  if (!job.accept_state) return null;
  const evidence = job.evidence;
  const canRecheck = (job.state === "done" || (job.state === "needs_you" && job.cause === "check")) && job.accept_state !== "checking" && !job.torn_down_at;
  return (
    <Card
      aria-label="crowsnest's check"
      title={
        <>
          <ShieldCheck className="h-4 w-4" aria-hidden />
          crowsnest's check
        </>
      }
      tone={job.accept_state === "failed" ? "danger" : job.accept_state === "stale" || job.accept_state === "needs_rebase" || job.accept_state === "unverified" ? "warning" : "default"}
      actions={
        <span className="flex items-center gap-1">
          {chip && <Chip tone={chip.tone} title={chip.title}>{chip.label}</Chip>}
          <HelpButton topic="concepts/acceptance#what-accepted-proves-and-what-it-does-not" what="what accepted proves" />
        </span>
      }
    >
      <p className="text-sm text-body">{job.accept_detail}</p>
      {job.no_commits_reason && <p className="mt-1 text-xs text-muted">No commits, because: {job.no_commits_reason}</p>}
      {evidence && (
        <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs">
          <dt className="text-muted">Command</dt>
          <dd className="break-all font-mono text-body">{evidence.command}</dd>
          <dt className="text-muted">Result</dt>
          <dd className={evidence.status === "passed" ? "text-success" : evidence.status === "running" ? "text-accent" : "text-danger"}>
            {evidence.status}
            {evidence.status !== "running" && ` · exit ${evidence.exit_code} · ${durationText(evidence.duration_ms)}`}
            {evidence.finished_at && <span className="text-muted"> · {relativeTime(evidence.finished_at)}</span>}
          </dd>
          <dt className="text-muted">Commit</dt>
          <dd className="font-mono text-body">
            {evidence.commit.slice(0, 10)}
            {evidence.stale && evidence.head && <span className="text-warning"> (the branch is now at {evidence.head.slice(0, 10)})</span>}
          </dd>
        </dl>
      )}
      {evidence?.output_tail && (
        <details className="mt-2" open={evidence.status !== "passed"}>
          <summary className="cursor-pointer text-xs text-muted">The end of its output</summary>
          <pre className="mt-1 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded border border-border bg-surface-raised p-2 font-mono text-2xs text-body">{evidence.output_tail}</pre>
        </details>
      )}
      {(canRecheck || job.accept_state === "needs_rebase") && (
        <div className="mt-2 flex flex-wrap gap-2">
          {canRecheck && (
            <Button compact icon={RefreshCw} disabled={guard.pending} onClick={() => guard.press("recheck", "Running the check again")}>
              Run the check again
            </Button>
          )}
          {job.accept_state === "needs_rebase" && job.state === "done" && (
            <Button compact icon={GitMerge} disabled={guard.pending} onClick={() => guard.press("rebase", "An agent is bringing the branch up to date")}>
              Ask an agent to rebase
            </Button>
          )}
        </div>
      )}
    </Card>
  );
}

/** The budget: each limit with what was used ("not measured" where crowsnest cannot see spend). */
export function BudgetCard({ job }: { job: Detail }) {
  const budget = job.budget;
  const guard = useGuard(job.id);
  const cancel = useJobAction(job.id);
  const set = useSetBudget(job.id);
  const toast = useToast();
  const [raising, setRaising] = useState(false);
  const [usd, setUsd] = useState("");
  const [minutes, setMinutes] = useState("");
  if (!budget || isFinished(job.state)) return null;
  const over = job.state === "needs_you" && job.cause === "budget";
  const rows = budgetRows(budget);
  const raise = () => {
    const body: { usd?: number; minutes?: number } = {};
    if (usd.trim()) body.usd = Number(usd);
    if (minutes.trim()) body.minutes = Number(minutes);
    set.mutate(body, {
      onSuccess: () => {
        setRaising(false);
        toast.show({ tone: "success", title: "Budget changed" });
      },
      onError: (error) => toast.show({ tone: "danger", title: "Not changed", description: error.message }),
    });
  };
  return (
    <Card
      aria-label="Budget"
      actions={<HelpButton topic="concepts/budgets-limits-pause#budget-states" what="budget states" />}
      tone={over ? "danger" : budget.state === "warned" || budget.state === "grace" ? "warning" : "default"}
      title={
        <>
          <Gauge className="h-4 w-4" aria-hidden />
          Budget
          {budget.used.children > 0 && <span className="font-normal text-muted">(with {budget.used.children} delegated)</span>}
        </>
      }
    >
      {rows.length === 0 ? (
        <p className="text-xs text-muted">No limit set.</p>
      ) : (
        <ul className="flex flex-col gap-1.5">
          {rows.map((row) => (
            <li key={row.label} className="text-xs">
              <div className="flex justify-between gap-2">
                <span className="text-muted">{row.label}</span>
                <span className={row.share === null ? "italic text-muted" : "text-body"}>
                  {row.used} of {row.limit}
                </span>
              </div>
              {row.share !== null && (
                <div className="mt-0.5 h-1.5 rounded bg-surface-raised" aria-hidden>
                  <div
                    className={row.share >= 1 ? "h-1.5 rounded bg-danger" : row.share >= 0.8 ? "h-1.5 rounded bg-warning" : "h-1.5 rounded bg-accent"}
                    style={{ width: `${Math.min(100, Math.round(row.share * 100))}%` }}
                  />
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      {!budget.used.measured && <p className="mt-1 text-2xs text-muted">This agent's spend is not measured: time and turns bound it.</p>}
      {budget.detail && <p className="mt-1 text-xs text-body">{budget.detail}</p>}
      <div className="mt-2 flex flex-wrap gap-2">
        <Button compact icon={Gauge} variant={over ? "primary" : "secondary"} onClick={() => setRaising(!raising)}>
          Raise the budget
        </Button>
        {over && (
          <>
            <Button compact icon={StepForward} disabled={guard.pending} onClick={() => guard.press("finish-step", "It may finish the step it is in")}>
              Let it finish this step
            </Button>
            <Button
              compact
              variant="danger"
              icon={Square}
              disabled={cancel.isPending}
              onClick={() => cancel.mutate("cancel", { onError: (error) => toast.show({ tone: "danger", title: "Not stopped", description: error.message }) })}
            >
              Stop the job
            </Button>
          </>
        )}
      </div>
      {raising && (
        <form
          className="mt-2 flex flex-wrap items-end gap-2 text-xs"
          onSubmit={(event) => {
            event.preventDefault();
            raise();
          }}
        >
          <label className="flex flex-col gap-0.5">
            <span className="text-muted">Dollars</span>
            <input aria-label="Budget in dollars" inputMode="decimal" value={usd} onChange={(event) => setUsd(event.target.value)} placeholder={String(budget.limits.usd || "")}
              className="h-9 w-24 rounded border border-border bg-surface px-2 text-body" />
          </label>
          <label className="flex flex-col gap-0.5">
            <span className="text-muted">Minutes</span>
            <input aria-label="Budget in minutes" inputMode="numeric" value={minutes} onChange={(event) => setMinutes(event.target.value)} placeholder={String(budget.limits.minutes || "")}
              className="h-9 w-24 rounded border border-border bg-surface px-2 text-body" />
          </label>
          <Button compact variant="primary" type="submit" disabled={set.isPending}>
            Save
          </Button>
        </form>
      )}
    </Card>
  );
}

/** Who owns the wait and what ends it, with Wake it for a blocked job. */
export function WaitLine({ job }: { job: Job }) {
  const guard = useGuard(job.id);
  if (isFinished(job.state) || !job.wake) return null;
  return (
    <p className="flex flex-wrap items-center gap-1.5 text-xs text-body" aria-label="Who it waits on">
      <AlarmClock className="h-3.5 w-3.5 text-muted" aria-hidden />
      <span className="text-muted">Waits on</span> {job.wait_owner}: {job.wake}
      {job.rechecks ? <span className="text-muted">· {job.rechecks} recheck{job.rechecks === 1 ? "" : "s"}</span> : null}
      <HelpButton topic="concepts/supervisor#waits-always-have-an-owner" what="who a wait belongs to" />
      {job.state === "blocked" && (
        <Button compact variant="ghost" disabled={guard.pending} onClick={() => guard.press("wake", "Woken")}>
          Wake it
        </Button>
      )}
    </p>
  );
}

/** What the agent last said when it ended a turn: on stuck and waiting jobs, so the human sees it. */
export function LastMessage({ job, clamp }: { job: Job; clamp?: boolean }) {
  if (!job.last_message) return null;
  const waiting = job.stuck || job.state === "needs_you" || job.state === "blocked";
  if (!waiting) return null;
  return (
    // A span, not a blockquote: on the board it sits inside the card's button.
    <span role="note" aria-label="What the agent last said" className="flex gap-1.5 border-l-2 border-border-strong pl-2 text-xs text-body">
      <MessageSquareQuote className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted" aria-hidden />
      <span className={clamp ? "line-clamp-2 break-words" : "whitespace-pre-wrap break-words"}>
        {job.last_message}
        {job.last_message_at && !clamp && <span className="text-2xs text-muted"> · {relativeTime(job.last_message_at)}</span>}
      </span>
    </span>
  );
}
