// The Jobs view (DESIGN.md 12), the default route: the text box at the top, the board below it
// (needs-you and stuck first, then running, then finished today; older finished jobs behind a
// toggle), and the selected job: beside the board from md, a full-screen sheet on a phone. The
// selection is in the URL (/?job=j_12) so a link or a reload lands on it.
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ListTodo, RefreshCw } from "lucide-react";
import { useState } from "react";
import { JobCard } from "../components/jobs/JobCard";
import { JobDetail } from "../components/jobs/JobDetail";
import { NewJobForm } from "../components/jobs/NewJobForm";
import { Banner } from "../components/ui/Banner";
import { Button } from "../components/ui/Button";
import { EmptyState } from "../components/ui/EmptyState";
import { Sheet } from "../components/ui/Sheet";
import { SkeletonRows } from "../components/ui/Skeleton";
import { boardSections, sectionLabels, type BoardSection, type Job } from "../lib/jobs";
import { DESKTOP_QUERY, useMediaQuery } from "../lib/media";
import { useJobs } from "../lib/queries";

export function JobsView() {
  const { job: selected } = useSearch({ strict: false }) as { job?: string };
  const navigate = useNavigate();
  const desktop = useMediaQuery(DESKTOP_QUERY);
  const select = (id: string | undefined) => void navigate({ to: "/", search: { job: id } });

  return (
    <div className="flex h-full min-h-0 flex-col md:flex-row">
      <div className="flex min-h-0 flex-col border-border md:w-96 md:shrink-0 md:border-r lg:w-[28rem]">
        <NewJobForm onCreated={(job) => select(job.id)} />
        <div className="min-h-0 flex-1 overflow-y-auto">
          <Board selected={selected} onSelect={select} />
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

export function Board({ selected, onSelect }: { selected?: string; onSelect: (id: string) => void }) {
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
  if (jobs.data.length === 0) {
    return (
      <EmptyState icon={ListTodo} title="No jobs yet">
        Say what needs doing above. Give it a directory to start an agent there, or leave it empty and the orchestrator takes it.
      </EmptyState>
    );
  }
  const sections = boardSections(jobs.data);
  const shown: BoardSection[] = ["attention", "running", "today"];
  return (
    <div>
      {shown.map((section) => (
        <Section key={section} section={section} jobs={sections[section]} selected={selected} onSelect={onSelect} />
      ))}
      {sections.older.length > 0 && (
        <div className="border-t border-border px-3 py-2">
          <Button compact variant="ghost" onClick={() => setShowOlder(!showOlder)} aria-expanded={showOlder}>
            {showOlder ? "Hide older" : `Show ${sections.older.length} older`}
          </Button>
        </div>
      )}
      {showOlder && <Section section="older" jobs={sections.older} selected={selected} onSelect={onSelect} />}
    </div>
  );
}

function Section({ section, jobs, selected, onSelect }: { section: BoardSection; jobs: Job[]; selected?: string; onSelect: (id: string) => void }) {
  if (jobs.length === 0) return null;
  return (
    <section aria-label={sectionLabels[section]}>
      <h2 className="sticky top-0 z-10 border-b border-border bg-surface-raised px-3 py-1 text-2xs font-semibold uppercase tracking-wide text-muted">
        {sectionLabels[section]} <span className="font-normal">({jobs.length})</span>
      </h2>
      <ul className="divide-y divide-border">
        {jobs.map((job) => (
          <JobCard key={job.id} job={job} selected={selected === job.id} onSelect={onSelect} />
        ))}
      </ul>
    </section>
  );
}
