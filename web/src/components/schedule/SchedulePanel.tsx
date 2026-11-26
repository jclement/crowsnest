// The Schedule (DESIGN.md 7.8, 21.4 and 12): every scheduled task with its cadence, who it is for
// (the orchestrator, a project's lead, or a job), last run, next time and last outcome, its runs
// (newest first, with links to the jobs they made), an on/off switch and Run now. Schedules made
// in the app are edited and deleted here (ScheduleForm); task files are shown "from file" and are
// read-only (their switch is still the app's), and an app schedule a file of the same name hides
// is flagged "shadowed" (it never runs). A project filter narrows the list (GET
// /schedule?project=id). Files that could not be read are listed under it. Reached from the Chat
// menu, the Jobs view, the project page and the palette as a Sheet (ScheduleSheet); a table from
// md, a list on a phone.
import { useNavigate } from "@tanstack/react-router";
import { ArrowLeft, CalendarClock, ChevronDown, ChevronRight, Pencil, Play, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { relativeTime } from "../../lib/format";
import type { ProjectView } from "../../lib/projects";
import { useProjects, useSchedule, useScheduleAction, useScheduleEdit } from "../../lib/queries";
import { nextDueText, targetOf, type ScheduleInput, type ScheduleTask } from "../../lib/schedule";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Chip } from "../ui/Chip";
import { EmptyState } from "../ui/EmptyState";
import { Sheet } from "../ui/Sheet";
import { SkeletonRows } from "../ui/Skeleton";
import { Switch } from "../ui/Switch";
import { useToast } from "../ui/toast-context";
import { ScheduleForm } from "./ScheduleForm";

/** "" every task, "0" the orchestrator's and unfiled ones, else a project id. */
type Filter = string;

interface SchedulePanelProps {
  /** Start filtered to this project (the project page). */
  projectId?: number;
  /** Opens a job a run made; without it the job id is plain text. */
  onOpenJob?: (id: string) => void;
}

type Mode = { kind: "list" } | { kind: "new" } | { kind: "edit"; task: ScheduleTask };

export function SchedulePanel({ projectId, onOpenJob }: SchedulePanelProps) {
  const [filter, setFilter] = useState<Filter>(projectId === undefined ? "" : String(projectId));
  const [mode, setMode] = useState<Mode>({ kind: "list" });
  const schedule = useSchedule(true, filter === "" ? undefined : Number(filter));
  const projects = useProjects();
  const projectList = projects.data?.projects ?? [];

  if (mode.kind !== "list") {
    return <FormMode mode={mode} projects={projectList} defaultProjectId={filter && filter !== "0" ? Number(filter) : undefined} onDone={() => setMode({ kind: "list" })} />;
  }
  if (schedule.isPending) return <SkeletonRows rows={4} label="Loading the schedule" />;
  if (schedule.isError) {
    return (
      <Banner tone="danger" role="alert" action={<Button compact icon={RefreshCw} onClick={() => void schedule.refetch()}>Retry</Button>}>
        Could not load the schedule: {schedule.error.message}
      </Banner>
    );
  }
  const { tasks, problems, dirs } = schedule.data;
  const names = new Map(projectList.map((project) => [project.id, project.name]));
  return (
    <div className="flex min-h-0 flex-col">
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-1.5">
        {projectList.length > 0 && (
          <label className="flex items-center gap-1.5 text-xs text-muted">
            Project
            <select
              aria-label="Project filter"
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
              className="h-11 rounded border border-border-strong bg-surface px-2 text-xs text-heading md:h-7"
            >
              <option value="">All</option>
              <option value="0">Orchestrator and unfiled</option>
              {projectList.map((project) => (
                <option key={project.id} value={project.id}>
                  {project.name}
                </option>
              ))}
            </select>
          </label>
        )}
        <Button compact variant="primary" icon={Plus} onClick={() => setMode({ kind: "new" })} className="ml-auto">
          New schedule
        </Button>
      </div>
      {tasks.length === 0 ? (
        <EmptyState icon={CalendarClock} title="No scheduled tasks">
          Make one with New schedule, or put a markdown file with <code className="font-mono">every:</code> frontmatter in{" "}
          {dirs.map((dir, i) => (
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
                <TaskHeading task={task} projectName={names.get(task.project_id ?? 0)} />
                <div className="text-xs text-muted">
                  {task.schedule} · last {task.last_run_at ? relativeTime(task.last_run_at) : "never"} · next {nextDueText(task, relativeTime)}
                </div>
                <Outcome task={task} />
                <div className="flex flex-wrap items-center justify-between gap-1">
                  <EnabledSwitch task={task} />
                  <span className="flex flex-wrap items-center justify-end gap-1">
                    <TaskEdits task={task} onEdit={() => setMode({ kind: "edit", task })} />
                    <RunNow task={task} />
                  </span>
                </div>
                <Runs task={task} onOpenJob={onOpenJob} />
              </li>
            ))}
          </ul>
          <table className="hidden w-full table-fixed border-collapse text-xs md:table">
            <caption className="sr-only">Scheduled tasks</caption>
            <thead className="sticky top-0 z-10 bg-surface-raised text-left text-2xs uppercase tracking-wide text-muted">
              <tr className="border-b border-border">
                <th scope="col" className="px-2 py-1 font-semibold">Task</th>
                <th scope="col" className="w-28 px-2 py-1 font-semibold">Cadence</th>
                <th scope="col" className="w-20 px-2 py-1 font-semibold">Last run</th>
                <th scope="col" className="w-24 px-2 py-1 font-semibold">Next</th>
                <th scope="col" className="w-14 px-2 py-1 font-semibold">On</th>
                <th scope="col" className="w-40 px-2 py-1 font-semibold"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {tasks.map((task) => (
                <tr key={task.name} className="border-b border-border align-top hover:bg-hover">
                  <td className="px-2 py-1">
                    <TaskHeading task={task} projectName={names.get(task.project_id ?? 0)} />
                    <Outcome task={task} />
                    <Runs task={task} onOpenJob={onOpenJob} />
                  </td>
                  <td className="px-2 py-1 text-body">{task.schedule}</td>
                  <td className="whitespace-nowrap px-2 py-1 text-muted">
                    {task.last_run_at ? <time dateTime={task.last_run_at} title={new Date(task.last_run_at).toLocaleString()}>{relativeTime(task.last_run_at)}</time> : "never"}
                  </td>
                  <td className="px-2 py-1 text-muted">{nextDueText(task, relativeTime)}</td>
                  <td className="px-2 py-1"><EnabledSwitch task={task} /></td>
                  <td className="px-2 py-1 text-right">
                    <span className="flex flex-wrap justify-end gap-0.5">
                      <TaskEdits task={task} onEdit={() => setMode({ kind: "edit", task })} />
                      <RunNow task={task} />
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
      {problems.length > 0 && (
        <div className="border-t border-border px-3 py-2">
          <h3 className="text-xs font-semibold text-heading">Skipped</h3>
          <ul className="mt-1 flex flex-col gap-1 text-xs">
            {problems.map((problem) => (
              <li key={problem.path || problem.name}>
                <code className="break-all font-mono text-body">{problem.path || problem.name}</code>: <span className="text-danger">{problem.error}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

function FormMode({ mode, projects, defaultProjectId, onDone }: { mode: Exclude<Mode, { kind: "list" }>; projects: ProjectView[]; defaultProjectId?: number; onDone: () => void }) {
  const edit = useScheduleEdit();
  const toast = useToast();
  const task = mode.kind === "edit" ? mode.task : undefined;
  const submit = (input: ScheduleInput, enabled: boolean) =>
    edit.mutate(task ? { kind: "update", input, enabled } : { kind: "create", input, enabled }, {
      onSuccess: () => {
        toast.show({ tone: "success", title: task ? `${input.name} saved` : `Schedule ${input.name} made` });
        onDone();
      },
    });
  return (
    <div className="flex flex-col">
      <div className="border-b border-border px-1 py-0.5">
        <Button compact variant="ghost" icon={ArrowLeft} onClick={onDone}>
          All schedules
        </Button>
      </div>
      <ScheduleForm task={task} projects={projects} defaultProjectId={defaultProjectId} pending={edit.isPending} error={edit.error?.message} onSubmit={submit} onCancel={onDone} />
    </div>
  );
}

function targetText(task: ScheduleTask, projectName: string | undefined): string {
  const target = targetOf(task);
  const project = projectName ?? task.project;
  if (target === "orchestrator") return "orchestrator";
  if (target === "lead") return `${project ?? "a project"} lead`;
  return project ? `job · ${project}` : "job";
}

function TaskHeading({ task, projectName }: { task: ScheduleTask; projectName?: string }) {
  const fromFile = task.origin !== "app";
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
      <span className="font-mono text-xs font-semibold text-heading">{task.name}</span>
      <Chip tone={task.as === "job" ? "accent" : "neutral"}>{targetText(task, projectName)}</Chip>
      {fromFile && (
        <Chip title={`Read-only here: ${task.path}`}>{task.origin === "user" ? "user file" : "from file"}</Chip>
      )}
      {task.shadowed && (
        <Chip tone="warning" title="A task file of the same name hides this schedule; it never runs while the file is there">
          shadowed
        </Chip>
      )}
      <span className="min-w-0 truncate text-xs text-body" title={task.path}>
        {task.title}
      </span>
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

/** The task's latest runs, folded away behind their count. */
function Runs({ task, onOpenJob }: { task: ScheduleTask; onOpenJob?: (id: string) => void }) {
  const [open, setOpen] = useState(false);
  const runs = task.runs ?? [];
  if (runs.length === 0) return null;
  return (
    <div className="text-2xs">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="inline-flex min-h-11 items-center gap-1 rounded text-muted hover:text-heading md:min-h-6"
      >
        {open ? <ChevronDown className="h-3 w-3" aria-hidden /> : <ChevronRight className="h-3 w-3" aria-hidden />}
        {runs.length === 1 ? "1 run" : `${runs.length} runs`}
      </button>
      {open && (
        <ol aria-label={`Runs of ${task.name}`} className="ml-4 flex flex-col gap-0.5 border-l border-border pl-2">
          {runs.map((run) => (
            <li key={`${run.at}:${run.outcome}`} className="flex flex-wrap gap-x-2 text-muted">
              <time dateTime={run.at} title={new Date(run.at).toLocaleString()}>
                {relativeTime(run.at)}
              </time>
              <span className="text-body">{run.outcome}</span>
              {run.job_id &&
                (onOpenJob ? (
                  <button type="button" onClick={() => onOpenJob(run.job_id ?? "")} className="font-mono text-accent hover:underline">
                    {run.job_id}
                  </button>
                ) : (
                  <span className="font-mono">{run.job_id}</span>
                ))}
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}

function EnabledSwitch({ task }: { task: ScheduleTask }) {
  const action = useScheduleAction();
  const toast = useToast();
  const toggle = (next: boolean) =>
    action.mutateAsync({ name: task.name, enabled: next }).catch((error: Error) => toast.show({ tone: "danger", title: "Could not switch it", description: error.message }));
  return <Switch checked={task.enabled} onChange={(next) => void toggle(next)} label={`${task.name} enabled`} disabled={action.isPending} />;
}

/** Edit and Delete, for schedules made in the app only. */
function TaskEdits({ task, onEdit }: { task: ScheduleTask; onEdit: () => void }) {
  const [confirming, setConfirming] = useState(false);
  const edit = useScheduleEdit();
  const toast = useToast();
  if (task.origin !== "app" || task.editable === false) return null;
  if (confirming) {
    return (
      <span className="flex flex-wrap items-center gap-1 text-xs text-body">
        Delete {task.name}?
        <Button
          compact
          variant="danger"
          disabled={edit.isPending}
          onClick={() =>
            edit.mutate(
              { kind: "delete", name: task.name },
              {
                onSuccess: () => toast.show({ tone: "success", title: `${task.name} deleted` }),
                onError: (error) => toast.show({ tone: "danger", title: "Not deleted", description: error.message }),
              },
            )
          }
        >
          Delete
        </Button>
        <Button compact variant="ghost" onClick={() => setConfirming(false)}>
          Keep
        </Button>
      </span>
    );
  }
  return (
    <>
      <Button compact variant="ghost" icon={Pencil} onClick={onEdit} aria-label={`Edit ${task.name}`}>
        Edit
      </Button>
      <Button compact variant="ghost" icon={Trash2} onClick={() => setConfirming(true)} aria-label={`Delete ${task.name}`}>
        Delete
      </Button>
    </>
  );
}

function RunNow({ task }: { task: ScheduleTask }) {
  const action = useScheduleAction();
  const toast = useToast();
  const target = targetOf(task);
  const run = () =>
    action
      .mutateAsync({ name: task.name, run: true })
      .then(() =>
        toast.show({
          tone: "success",
          title: task.as === "job" ? `${task.name}: job started` : target === "lead" ? `${task.name} goes with the lead's next poke` : `${task.name} goes with the next poke`,
        }),
      )
      .catch((error: Error) => toast.show({ tone: "danger", title: "Could not run it", description: error.message }));
  return (
    <Button compact variant="ghost" icon={Play} onClick={() => void run()} disabled={action.isPending || task.run_now || task.shadowed} aria-label={`Run ${task.name} now`}>
      Run now
    </Button>
  );
}

/** The Schedule in a Sheet: full screen on a phone, a modal from md. */
export function ScheduleSheet({ open, onClose, projectId }: { open: boolean; onClose: () => void; projectId?: number }) {
  const navigate = useNavigate();
  const openJob = (id: string) => {
    onClose();
    void navigate({ to: "/", search: { job: id } });
  };
  return (
    <Sheet open={open} onClose={onClose} title="Schedule">
      {open && <SchedulePanel projectId={projectId} onOpenJob={openJob} />}
    </Sheet>
  );
}
