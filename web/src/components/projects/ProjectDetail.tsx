// ProjectDetail: one project in full (DESIGN.md 21): what and where it is, its lead
// (LeadControls), its jobs, its schedules (GET /schedule?project=id, with Run now and the Schedule
// sheet filtered to it), its own instruction layer (edited in place) and what its lead was
// started with. Opening it tells the server once (?open=1), which starts a lead set to start
// "when I open the project".
import { useNavigate } from "@tanstack/react-router";
import { CalendarClock, ListTodo, Pencil, Play, Plus, RefreshCw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { endpoints, queryKeys } from "../../lib/api";
import { leadSubject } from "../../lib/instructions";
import { boardSections, type Job } from "../../lib/jobs";
import { relativeTime } from "../../lib/format";
import { leadRunning, type ProjectView } from "../../lib/projects";
import { useJobs, useProject, useSchedule, useScheduleAction } from "../../lib/queries";
import { nextDueText, targetOf } from "../../lib/schedule";
import { AgentInstructionsCard } from "../instructions/AgentInstructionsCard";
import { LayerEditor } from "../instructions/LayerEditor";
import { JobCard } from "../jobs/JobCard";
import { ScheduleSheet } from "../schedule/SchedulePanel";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Chip } from "../ui/Chip";
import { SkeletonRows } from "../ui/Skeleton";
import { useToast } from "../ui/toast-context";
import { LeadControls } from "./LeadControls";
import { ArchiveButton, EditProjectSheet } from "./ProjectSheets";

export function ProjectDetail({ slug }: { slug: string }) {
  const query = useProject(slug);
  useOpenProject(slug);
  if (query.isPending) return <SkeletonRows rows={8} label="Loading the project" />;
  if (query.isError) {
    return (
      <Banner tone="danger" role="alert" action={<Button compact icon={RefreshCw} onClick={() => void query.refetch()}>Retry</Button>}>
        Could not load {slug}: {query.error.message}
      </Banner>
    );
  }
  return <Detail project={query.data} />;
}

/** Tells the server the project was opened, once per mount (lead_start=open starts the lead). */
function useOpenProject(slug: string) {
  const queryClient = useQueryClient();
  const sent = useRef(false);
  useEffect(() => {
    if (sent.current) return;
    sent.current = true;
    endpoints
      .project(slug, true)
      .then((project) => queryClient.setQueryData(queryKeys.project(slug), project))
      .catch(() => {}); // the plain fetch shows any error
  }, [slug, queryClient]);
}

function Detail({ project }: { project: ProjectView }) {
  const [editing, setEditing] = useState(false);
  return (
    <article aria-label={project.name} className="flex max-w-4xl flex-col gap-3 p-3 md:p-4">
      <header className="flex flex-col gap-1.5">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="min-w-0 break-words text-base font-semibold text-heading">{project.name}</h2>
          <Chip mono>{project.slug}</Chip>
          {project.archived && <Chip tone="warning">archived</Chip>}
          <span className="ml-auto flex flex-wrap gap-1">
            <Button compact variant="ghost" icon={Pencil} onClick={() => setEditing(true)}>
              Edit
            </Button>
            <ArchiveButton project={project} />
          </span>
        </div>
        <p className="break-all font-mono text-2xs text-muted">
          {project.dir}
          {project.root_dir && project.root_dir !== project.dir && ` · repo ${project.root_dir}`}
        </p>
        {project.description && <p className="text-sm text-body">{project.description}</p>}
        <Defaults project={project} />
      </header>
      <LeadControls project={project} />
      <ProjectJobs project={project} />
      <ProjectSchedules project={project} />
      <Card title="Project instructions" aria-label="Project instructions">
        <LayerEditor scope="project" apiKey={String(project.id)} hint="The lead and every job agent of this project get this, after the global and role layers." />
      </Card>
      <AgentInstructionsCard subject={leadSubject(project.slug)} restart={{ kind: "lead", slug: project.slug }} who={`${project.name}'s lead`} canRestart={leadRunning(project.lead)} />
      <EditProjectSheet project={project} open={editing} onClose={() => setEditing(false)} />
    </article>
  );
}

/** The defaults its jobs get, as chips. */
function Defaults({ project }: { project: ProjectView }) {
  const chips: string[] = [];
  if (project.default_role) chips.push(`role ${project.default_role}`);
  if (project.default_engine) chips.push(project.default_engine);
  if (project.default_model) chips.push(project.default_model);
  if (project.default_isolate !== null) chips.push(project.default_isolate ? "own worktree" : "no worktree");
  if (project.permission_mode) chips.push(project.permission_mode);
  chips.push(`lead starts: ${project.lead_start}`);
  return (
    <div className="flex flex-wrap gap-1" aria-label="Defaults">
      {chips.map((chip) => (
        <Chip key={chip}>{chip}</Chip>
      ))}
    </div>
  );
}

/** The project's live jobs and the ones finished today; the board has the rest. */
function ProjectJobs({ project }: { project: ProjectView }) {
  const jobs = useJobs();
  const navigate = useNavigate();
  const mine = (jobs.data ?? []).filter((job) => (job.project_id ?? 0) === project.id);
  const sections = boardSections(mine);
  const shown: Job[] = [...sections.attention, ...sections.running, ...sections.today];
  const open = (id: string) => void navigate({ to: "/jobs", search: { job: id, project: project.slug } });
  return (
    <Card
      title={
        <>
          Jobs
          <span className="font-normal text-muted">
            {project.counts.needs_you} need you · {project.counts.running} running · {project.counts.total} in all
          </span>
        </>
      }
      aria-label="Project jobs"
      bodyClassName="p-0"
      actions={
        <>
          <Button compact variant="ghost" icon={ListTodo} onClick={() => void navigate({ to: "/jobs", search: { project: project.slug } })}>
            Board
          </Button>
          <Button compact variant="ghost" icon={Plus} onClick={() => void navigate({ to: "/jobs", search: { project: project.slug } })} disabled={project.archived}>
            New job
          </Button>
        </>
      }
    >
      {jobs.isPending ? (
        <SkeletonRows rows={2} label="Loading jobs" />
      ) : shown.length === 0 ? (
        <p className="px-3 py-2 text-xs text-muted">{mine.length === 0 ? "No jobs in this project yet." : "Nothing live or finished today; the board has the older ones."}</p>
      ) : (
        <ul className="divide-y divide-border">
          {shown.map((job) => (
            <JobCard key={job.id} job={job} onSelect={open} />
          ))}
        </ul>
      )}
    </Card>
  );
}

function ProjectSchedules({ project }: { project: ProjectView }) {
  const schedule = useSchedule(true, project.id);
  const [open, setOpen] = useState(false);
  const action = useScheduleAction();
  const toast = useToast();
  const run = (name: string, asJob: boolean) =>
    action
      .mutateAsync({ name, run: true })
      .then(() => toast.show({ tone: "success", title: asJob ? `${name}: job started` : `${name} goes with the lead's next poke` }))
      .catch((error: Error) => toast.show({ tone: "danger", title: "Could not run it", description: error.message }));
  const tasks = schedule.data?.tasks ?? [];
  return (
    <Card
      title="Schedules"
      aria-label="Project schedules"
      bodyClassName="p-0"
      actions={
        <Button compact variant="ghost" icon={CalendarClock} onClick={() => setOpen(true)}>
          Manage
        </Button>
      }
    >
      {schedule.isPending ? (
        <SkeletonRows rows={2} label="Loading schedules" />
      ) : tasks.length === 0 ? (
        <p className="px-3 py-2 text-xs text-muted">No schedules for this project. Manage → New schedule makes one for its lead or as a job.</p>
      ) : (
        <ul className="divide-y divide-border">
          {tasks.map((task) => (
            <li key={task.name} className="flex flex-wrap items-center gap-x-2 gap-y-0.5 px-3 py-1.5 text-xs">
              <span className="font-mono font-semibold text-heading">{task.name}</span>
              <Chip tone={targetOf(task) === "job" ? "accent" : "neutral"}>{targetOf(task) === "job" ? "job" : "to the lead"}</Chip>
              {!task.enabled && <Chip>off</Chip>}
              <span className="min-w-0 flex-1 truncate text-body">{task.schedule}</span>
              <span className="text-2xs text-muted">
                last {task.last_run_at ? relativeTime(task.last_run_at) : "never"} · next {nextDueText(task, relativeTime)}
              </span>
              <Button compact variant="ghost" icon={Play} onClick={() => void run(task.name, task.as === "job")} disabled={action.isPending || task.run_now} aria-label={`Run ${task.name} now`}>
                Run now
              </Button>
            </li>
          ))}
        </ul>
      )}
      <ScheduleSheet open={open} onClose={() => setOpen(false)} projectId={project.id} />
    </Card>
  );
}
