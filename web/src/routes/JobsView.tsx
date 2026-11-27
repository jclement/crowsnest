// The Jobs view (DESIGN.md 12), the default route: the text box at the top, the board below it
// (needs-you and stuck first, then running, then finished today; older finished jobs behind a
// toggle), and the selected job: beside the board from md, a full-screen sheet on a phone. The
// selection is in the URL (/jobs?job=j_12) so a link or a reload lands on it. The Journal (pokes,
// sends, answers, restarts, alerts), the Schedule (scheduled tasks) and Spend (today's and this
// week's estimated cost, with the breakdown sheet) open from the row above the board.
//
// Projects (DESIGN.md 21.1): a switcher above the box picks All, one project or Unfiled, from
// the URL (?project=<slug>) or as this browser last left it; the board then shows that
// project's jobs, the new-job box gives jobs to it ("to the lead" or "as a job"), and the
// Schedule and Spend sheets open filtered to it. With every project shown, cards carry a chip
// naming their project.
import { useNavigate, useSearch } from "@tanstack/react-router";
import { CalendarClock, ListTodo, NotebookText, RefreshCw } from "lucide-react";
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
import { Button } from "../components/ui/Button";
import { EmptyState } from "../components/ui/EmptyState";
import { Sheet } from "../components/ui/Sheet";
import { SkeletonRows } from "../components/ui/Skeleton";
import { boardSections, sectionLabels, type BoardSection, type Job } from "../lib/jobs";
import { DESKTOP_QUERY, useMediaQuery } from "../lib/media";
import { filterJobs, filterProjectId, filterToParam, projectsById, type ProjectFilter, type ProjectView } from "../lib/projects";
import { findProject, useJobs, useProjects } from "../lib/queries";

export function JobsView() {
  const { job: selected } = useSearch({ strict: false }) as { job?: string };
  const navigate = useNavigate();
  const desktop = useMediaQuery(DESKTOP_QUERY);
  const projects = useProjects();
  const [filter, setFilter] = useProjectFilter(projects.data);
  const project = filter.kind === "project" ? findProject(projects.data?.projects, filter.slug) : undefined;
  const projectParam = filterToParam(filter) || undefined;
  const select = (id: string | undefined) => void navigate({ to: "/jobs", search: { job: id, project: projectParam } });
  const [journalOpen, setJournalOpen] = useState(false);
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [spendOpen, setSpendOpen] = useState(false);
  const scheduleProject = filterProjectId(filter, projects.data?.projects);

  return (
    <div className="flex h-full min-h-0 flex-col md:flex-row">
      <div className="flex min-h-0 flex-col border-border md:w-96 md:shrink-0 md:border-r lg:w-[28rem]">
        {projects.data && projects.data.projects.length > 0 && <ProjectSwitcher list={projects.data} value={filter} onChange={setFilter} />}
        <NewJobForm onCreated={(job) => select(job.id)} project={project} />
        <div className="flex flex-wrap items-center justify-end gap-x-0.5 border-b border-border px-1 py-0.5">
          <span className="mr-auto">
            <SpendButton onOpen={() => setSpendOpen(true)} />
          </span>
          <Button compact variant="ghost" icon={CalendarClock} onClick={() => setScheduleOpen(true)}>
            Schedule
          </Button>
          <Button compact variant="ghost" icon={NotebookText} onClick={() => setJournalOpen(true)}>
            Journal
          </Button>
        </div>
        <JournalSheet open={journalOpen} onClose={() => setJournalOpen(false)} />
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
          ? `Say what needs doing above: give it to ${name}'s lead, or as a job in its directory.`
          : "Say what needs doing above. Give it a directory to start an agent there, or leave it empty and the orchestrator takes it."}
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
  if (jobs.length === 0) return null;
  return (
    <section aria-label={sectionLabels[section]}>
      <h2 className="sticky top-0 z-10 border-b border-border bg-surface-raised px-3 py-1 text-2xs font-semibold uppercase tracking-wide text-muted">
        {sectionLabels[section]} <span className="font-normal">({jobs.length})</span>
      </h2>
      <ul className="divide-y divide-border">
        {jobs.map((job) => (
          <JobCard key={job.id} job={job} selected={selected === job.id} onSelect={onSelect} projectName={nameOf(job)} />
        ))}
      </ul>
    </section>
  );
}
