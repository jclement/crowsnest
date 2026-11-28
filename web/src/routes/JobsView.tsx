// The Jobs view (DESIGN.md 12): a list. The board (needs-you and stuck first, then running, then
// finished today; older finished jobs behind a toggle), and the selected job: beside the board from md, a full-screen sheet on a phone. The
// selection is in the URL (/jobs?job=j_12) so a link or a reload lands on it. The Journal (pokes,
// sends, answers, restarts, alerts), the Schedule (scheduled tasks) and Spend (today's and this
// week's estimated cost, with the breakdown sheet) open from the row above the board.
//
// Jobs are given on Now (its job box), from the palette or by voice; this view has no composer of
// its own (R8: one place to give a job). Its "New job" button, and ?new=1, open the same form,
// NewJobForm, in a sheet over the board.
//
// Projects (DESIGN.md 21.1): a switcher above the board picks All, one project or Unfiled, from
// the URL (?project=<slug>) or as this browser last left it; the board then shows that
// project's jobs, New job gives jobs to it ("to the lead" or "as a job"), and the Schedule and
// Spend sheets open filtered to it. With every project shown, cards carry a chip naming their
// project.
import { useDiffProgress } from "../lib/changes-queries";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { CalendarClock, FolderGit2, Gauge, ListTodo, NotebookText, Plus, RefreshCw } from "lucide-react";
import { SweepSheet } from "../components/control/SweepSheet";
import { queueText } from "../lib/landing";
import { useState } from "react";
import { JobCard } from "../components/jobs/JobCard";
import { JobDetail } from "../components/jobs/JobDetail";
import { NewJobForm } from "../components/jobs/NewJobForm";
import { ProjectSwitcher } from "../components/projects/ProjectSwitcher";
import { useProjectFilter } from "../components/projects/useProjectFilter";
import { JournalSheet } from "../components/journal/JournalPanel";
import { ScheduleSheet } from "../components/schedule/SchedulePanel";
import { SpendButton, SpendSheet } from "../components/spend/SpendSheet";
import { Banner } from "../components/ui/Banner";
import { Button, IconButton } from "../components/ui/Button";
import { EmptyState } from "../components/ui/EmptyState";
import { Sheet } from "../components/ui/Sheet";
import { SkeletonRows } from "../components/ui/Skeleton";
import { landable } from "../lib/landing";
import { boardSections, sectionLabels, type BoardSection, type Job } from "../lib/jobs";
import { DESKTOP_QUERY, useMediaQuery } from "../lib/media";
import { filterJobs, filterProjectId, filterToParam, projectsById, type ProjectFilter, type ProjectView } from "../lib/projects";
import { findProject, useJobs, useProjects, useQueue, useSweep } from "../lib/queries";
import { HelpButton } from "../components/help/HelpButton";

export function JobsView() {
  const { job: selected, new: formParam } = useSearch({ strict: false }) as { job?: string; new?: true };
  const navigate = useNavigate();
  const desktop = useMediaQuery(DESKTOP_QUERY);
  const projects = useProjects();
  const [filter, setFilter] = useProjectFilter(projects.data);
  const project = filter.kind === "project" ? findProject(projects.data?.projects, filter.slug) : undefined;
  const projectParam = filterToParam(filter) || undefined;
  const select = (id: string | undefined) => void navigate({ to: "/jobs", search: { job: id, project: projectParam } });
  const setFormOpen = (open: boolean) => void navigate({ to: "/jobs", search: { job: selected, project: projectParam, new: open ? true : undefined } });
  const [journalOpen, setJournalOpen] = useState(false);
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [spendOpen, setSpendOpen] = useState(false);
  const [sweepOpen, setSweepOpen] = useState(false);
  const queue = useQueue();
  const sweep = useSweep();
  const lingering = landable(sweep.data).length;
  const scheduleProject = filterProjectId(filter, projects.data?.projects);

  return (
    <div className="flex h-full min-h-0 flex-col md:flex-row">
      <div className="flex min-h-0 flex-col border-border md:w-96 md:shrink-0 md:border-r lg:w-[28rem]">
        {projects.data && projects.data.projects.length > 0 && <ProjectSwitcher list={projects.data} value={filter} onChange={setFilter} />}
        <Sheet open={formParam === true} onClose={() => setFormOpen(false)} title={project ? `New job for ${project.name}` : "New job"}>
          <NewJobForm onCreated={(job) => select(job.id)} project={project} autoFocus />
        </Sheet>
        <div className="flex flex-wrap items-center justify-end gap-x-0.5 border-b border-border px-1 py-0.5">
          <Button compact variant="primary" icon={Plus} onClick={() => setFormOpen(true)} disabled={project?.archived}>
            New job
          </Button>
          <span className="mr-auto">
            <SpendButton onOpen={() => setSpendOpen(true)} />
          </span>
          {/* Icons with their names (R10 F-F13): with New job and spend on the first row and the counts on the second, the toolbar is
              two rows on a phone, not three. */}
          <IconButton icon={CalendarClock} label="Schedule" onClick={() => setScheduleOpen(true)} />
          <IconButton icon={NotebookText} label="Journal" onClick={() => setJournalOpen(true)} />
          <HelpButton topic="concepts/jobs#states" what="job states" inline={false} />
          {queue.data && (
            <Button compact variant="ghost" icon={Gauge} onClick={() => void navigate({ to: "/settings", hash: "limits" })} title="Running-job limit, load hold and heavy slots: Settings → Limits">
              <span className={queue.data.paused || queue.data.load_hold ? "text-warning" : undefined}>{queueText(queue.data)}</span>
            </Button>
          )}
          {lingering > 0 && (
            <Button compact variant="ghost" icon={FolderGit2} onClick={() => setSweepOpen(true)} title="Finished jobs whose worktrees are still around">
              {lingering} to land
            </Button>
          )}
        </div>
        <JournalSheet open={journalOpen} onClose={() => setJournalOpen(false)} />
        <SweepSheet
          open={sweepOpen}
          onClose={() => setSweepOpen(false)}
          onOpenJob={(id) => {
            setSweepOpen(false);
            select(id);
          }}
        />
        <ScheduleSheet open={scheduleOpen} onClose={() => setScheduleOpen(false)} projectId={scheduleProject ?? undefined} />
        <SpendSheet open={spendOpen} onClose={() => setSpendOpen(false)} projectId={scheduleProject ?? undefined} />
        <div className="min-h-0 flex-1 overflow-y-auto">
          <Board selected={selected} onSelect={select} filter={filter} projects={projects.data?.projects} />
        </div>
      </div>
      {desktop ? (
        <section aria-label="Selected job" className="min-h-0 min-w-0 flex-1 overflow-y-auto">
          {selected ? (
            <JobDetail key={selected} id={selected} />
          ) : (
            <EmptyState icon={ListTodo} title="Pick a job">
              Its timeline, result, questions and conversation open here.
            </EmptyState>
          )}
        </section>
      ) : (
        <Sheet open={!!selected} onClose={() => select(undefined)} title={<span className="truncate">Job {selected}</span>}>
          {selected && <JobDetail key={selected} id={selected} />}
        </Sheet>
      )}
    </div>
  );
}

interface BoardProps {
  selected?: string;
  onSelect: (id: string) => void;
  filter?: ProjectFilter;
  projects?: ProjectView[];
}

export function Board({ selected, onSelect, filter = { kind: "all" }, projects }: BoardProps) {
  const jobs = useJobs();
  const [showOlder, setShowOlder] = useState(false);
  if (jobs.isPending) return <SkeletonRows rows={5} label="Loading jobs" />;
  if (jobs.isError) {
    return (
      <Banner tone="danger" role="alert" action={<Button compact icon={RefreshCw} onClick={() => void jobs.refetch()}>Retry</Button>}>
        Could not load jobs: {jobs.error.message}
      </Banner>
    );
  }
  const shownJobs = filterJobs(jobs.data, filter, projects);
  if (shownJobs.length === 0) {
    const name = filter.kind === "project" ? (projects?.find((project) => project.slug === filter.slug)?.name ?? filter.slug) : null;
    return (
      <EmptyState icon={ListTodo} title={name ? `No jobs in ${name} yet` : filter.kind === "unfiled" ? "No unfiled jobs" : "No jobs yet"}>
        {name
          ? `New job gives one to ${name}'s lead, or as a job in its directory.`
          : "Give one from Now's job box or with New job: with a directory an agent starts there; without one the orchestrator takes it."}
      </EmptyState>
    );
  }
  const sections = boardSections(shownJobs);
  const names = filter.kind === "all" ? projectsById(projects) : undefined;
  const nameOf = (job: Job) => (job.project_id ? names?.get(job.project_id)?.name : undefined);
  const shown: BoardSection[] = ["attention", "running", "today"];
  return (
    <div>
      {shown.map((section) => (
        <Section key={section} section={section} jobs={sections[section]} selected={selected} onSelect={onSelect} nameOf={nameOf} />
      ))}
      {sections.older.length > 0 && (
        <div className="border-t border-border px-3 py-2">
          <Button compact variant="ghost" onClick={() => setShowOlder(!showOlder)} aria-expanded={showOlder}>
            {showOlder ? "Hide older" : `Show ${sections.older.length} older`}
          </Button>
        </div>
      )}
      {showOlder && <Section section="older" jobs={sections.older} selected={selected} onSelect={onSelect} nameOf={nameOf} />}
    </div>
  );
}

function Section({
  section,
  jobs,
  selected,
  onSelect,
  nameOf,
}: {
  section: BoardSection;
  jobs: Job[];
  selected?: string;
  onSelect: (id: string) => void;
  nameOf: (job: Job) => string | undefined;
}) {
  const progress = useDiffProgress();
  if (jobs.length === 0) return null;
  return (
    <section aria-label={sectionLabels[section]}>
      <h2 className="sticky top-0 z-10 border-b border-border bg-surface-raised px-3 py-1 text-2xs font-semibold uppercase tracking-wide text-muted">
        {sectionLabels[section]} <span className="font-normal">({jobs.length} {jobs.length === 1 ? "job" : "jobs"})</span>
      </h2>
      <ul className="divide-y divide-border">
        {jobs.map((job) => (
          <JobCard key={job.id} job={job} selected={selected === job.id} onSelect={onSelect} projectName={nameOf(job)} progress={progress.data?.[job.id]} />
        ))}
      </ul>
    </section>
  );
}
