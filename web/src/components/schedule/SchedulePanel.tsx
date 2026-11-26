// The Schedule (DESIGN.md 7.8 and 12): every scheduled task file with its cadence, last run, next
// time and last outcome, an on/off switch (kept by crowsnest; the file stays the source of what
// the task says) and Run now. Files that could not be read are listed under it. Reached from the
// Chat menu and the Jobs view as a Sheet (ScheduleSheet); a table from md, a list on a phone.
import { CalendarClock, Play, RefreshCw } from "lucide-react";
import { relativeTime } from "../../lib/format";
import { useSchedule, useScheduleAction } from "../../lib/queries";
import { nextDueText, type ScheduleTask } from "../../lib/schedule";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Chip } from "../ui/Chip";
import { EmptyState } from "../ui/EmptyState";
import { Sheet } from "../ui/Sheet";
import { SkeletonRows } from "../ui/Skeleton";
import { useToast } from "../ui/toast-context";

export function SchedulePanel() {
  const schedule = useSchedule();
  if (schedule.isPending) return <SkeletonRows rows={4} label="Loading the schedule" />;
  if (schedule.isError) {
    return (
      <Banner tone="danger" role="alert" action={<Button compact icon={RefreshCw} onClick={() => void schedule.refetch()}>Retry</Button>}>
        Could not load the schedule: {schedule.error.message}
      </Banner>
    );
  }
  const { tasks, problems, dirs } = schedule.data;
  return (
    <div className="flex min-h-0 flex-col">
      {tasks.length === 0 ? (
        <EmptyState icon={CalendarClock} title="No scheduled tasks">
          Put a markdown file with <code className="font-mono">every:</code> frontmatter in {dirs.map((dir, i) => (
            <span key={dir}>
              {i > 0 && " or "}
              <code className="break-all font-mono">{dir}</code>
            </span>
          ))}
          . The README's "Scheduled work" section has the format and three examples.
        </EmptyState>
      ) : (
        <>
          <ul className="divide-y divide-border md:hidden" aria-label="Scheduled tasks">
            {tasks.map((task) => (
              <li key={task.name} className="flex flex-col gap-1 px-3 py-2">
                <TaskHeading task={task} />
                <div className="text-xs text-muted">
                  {task.schedule} · last {task.last_run_at ? relativeTime(task.last_run_at) : "never"} · next {nextDueText(task, relativeTime)}
                </div>
                <Outcome task={task} />
                <div className="flex items-center justify-between gap-2">
                  <EnabledSwitch task={task} />
                  <RunNow task={task} />
                </div>
              </li>
            ))}
          </ul>
          <table className="hidden w-full table-fixed border-collapse text-xs md:table">
            <caption className="sr-only">Scheduled tasks</caption>
            <thead className="sticky top-0 z-10 bg-surface-raised text-left text-2xs uppercase tracking-wide text-muted">
              <tr className="border-b border-border">
                <th scope="col" className="px-2 py-1 font-semibold">Task</th>
                <th scope="col" className="w-32 px-2 py-1 font-semibold">Cadence</th>
                <th scope="col" className="w-20 px-2 py-1 font-semibold">Last run</th>
                <th scope="col" className="w-24 px-2 py-1 font-semibold">Next</th>
                <th scope="col" className="w-16 px-2 py-1 font-semibold">On</th>
                <th scope="col" className="w-24 px-2 py-1 font-semibold"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {tasks.map((task) => (
                <tr key={task.name} className="border-b border-border align-top hover:bg-hover">
                  <td className="px-2 py-1">
                    <TaskHeading task={task} />
                    <Outcome task={task} />
                  </td>
                  <td className="px-2 py-1 text-body">{task.schedule}</td>
                  <td className="whitespace-nowrap px-2 py-1 text-muted">
                    {task.last_run_at ? <time dateTime={task.last_run_at} title={new Date(task.last_run_at).toLocaleString()}>{relativeTime(task.last_run_at)}</time> : "never"}
                  </td>
                  <td className="px-2 py-1 text-muted">{nextDueText(task, relativeTime)}</td>
                  <td className="px-2 py-1"><EnabledSwitch task={task} /></td>
                  <td className="px-2 py-1 text-right"><RunNow task={task} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
      {problems.length > 0 && (
        <div className="border-t border-border px-3 py-2">
          <h3 className="text-xs font-semibold text-heading">Files skipped</h3>
          <ul className="mt-1 flex flex-col gap-1 text-xs">
            {problems.map((problem) => (
              <li key={problem.path}>
                <code className="break-all font-mono text-body">{problem.path}</code>: <span className="text-danger">{problem.error}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

function TaskHeading({ task }: { task: ScheduleTask }) {
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
      <span className="font-mono text-xs font-semibold text-heading">{task.name}</span>
      {task.as === "job" && <Chip tone="accent">job</Chip>}
      {task.origin === "user" && <Chip>user</Chip>}
      <span className="min-w-0 truncate text-xs text-body" title={task.path}>{task.title}</span>
    </div>
  );
}

function Outcome({ task }: { task: ScheduleTask }) {
  if (!task.last_outcome) return null;
  return (
    <div className="text-2xs text-muted">
      {task.last_outcome}
      {task.job_id && task.job_state && ` · ${task.job_id} is ${task.job_state}`}
    </div>
  );
}

function EnabledSwitch({ task }: { task: ScheduleTask }) {
  const action = useScheduleAction();
  const toast = useToast();
  const toggle = () =>
    action.mutateAsync({ name: task.name, enabled: !task.enabled }).catch((error: Error) =>
      toast.show({ tone: "danger", title: "Could not switch it", description: error.message }),
    );
  return (
    <button
      type="button"
      role="switch"
      aria-checked={task.enabled}
      aria-label={`${task.name} enabled`}
      disabled={action.isPending}
      onClick={() => void toggle()}
      className="inline-flex min-h-11 items-center md:min-h-7"
    >
      <span className={`relative inline-block h-5 w-9 rounded-full transition-colors ${task.enabled ? "bg-accent" : "bg-border"}`}>
        <span className={`absolute top-0.5 h-4 w-4 rounded-full bg-surface shadow transition-all ${task.enabled ? "left-4.5" : "left-0.5"}`} />
      </span>
    </button>
  );
}

function RunNow({ task }: { task: ScheduleTask }) {
  const action = useScheduleAction();
  const toast = useToast();
  const run = () =>
    action
      .mutateAsync({ name: task.name, run: true })
      .then(() =>
        toast.show({
          tone: "success",
          title: task.as === "job" ? `${task.name}: job started` : `${task.name} goes with the next poke`,
        }),
      )
      .catch((error: Error) => toast.show({ tone: "danger", title: "Could not run it", description: error.message }));
  return (
    <Button compact variant="ghost" icon={Play} onClick={() => void run()} disabled={action.isPending || task.run_now} aria-label={`Run ${task.name} now`}>
      Run now
    </Button>
  );
}

/** The Schedule in a Sheet: full screen on a phone, a modal from md. */
export function ScheduleSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Sheet open={open} onClose={onClose} title="Schedule">
      {open && <SchedulePanel />}
    </Sheet>
  );
}
