// The Projects view (DESIGN.md 21): /projects lists the projects, each with its directory, its
// lead's state (and Start or Stop right there), and how many of its jobs need you or are running;
// archived ones behind a toggle. /projects/<slug> opens one (ProjectDetail): beside the list from
// md, on its own page on a phone. "New project" opens a sheet with the directory picker.
import { Link, useNavigate } from "@tanstack/react-router";
import { ArrowLeft, FolderKanban, Play, Plus, RefreshCw, Square } from "lucide-react";
import { useState } from "react";
import { ProjectDetail } from "../components/projects/ProjectDetail";
import { NewProjectSheet } from "../components/projects/ProjectSheets";
import { Banner } from "../components/ui/Banner";
import { Button } from "../components/ui/Button";
import { Chip } from "../components/ui/Chip";
import { EmptyState } from "../components/ui/EmptyState";
import { SkeletonRows } from "../components/ui/Skeleton";
import { useToast } from "../components/ui/toast-context";
import { cn } from "../lib/cn";
import { middlePath } from "../lib/browse";
import { DESKTOP_QUERY, useMediaQuery } from "../lib/media";
import { leadChip, leadRunning, type ProjectView } from "../lib/projects";
import { useLeadAction, useProjects } from "../lib/queries";
import { HelpButton } from "../components/help/HelpButton";
import { Hint } from "../components/help/Hint";

export function ProjectsView({ slug, create }: { slug?: string; create?: "1" | "browse" }) {
  const desktop = useMediaQuery(DESKTOP_QUERY);
  const [creating, setCreating] = useState(!!create);
  const navigate = useNavigate();
  // A link that asks for New project (?new=…) opens it, also when this view is already showing.
  const [askedFor, setAskedFor] = useState(create);
  if (askedFor !== create) {
    setAskedFor(create);
    if (create) setCreating(true);
  }
  const closeNew = () => {
    setCreating(false);
    if (create) void navigate({ to: "/projects", search: {}, replace: true });
  };
  const sheet = (
    <NewProjectSheet
      open={creating}
      browse={create === "browse"}
      onClose={closeNew}
      onCreated={(project) => void navigate({ to: "/projects/$slug", params: { slug: project.slug } })}
    />
  );

  if (!desktop && slug) {
    return (
      <div className="flex min-h-full flex-col">
        <div className="flex min-h-11 items-center border-b border-border bg-surface-raised px-1">
          <Link to="/projects" className="inline-flex min-h-11 items-center gap-1 rounded px-2 text-sm text-body hover:bg-hover">
            <ArrowLeft className="h-4 w-4" aria-hidden /> Projects
          </Link>
        </div>
        <ProjectDetail key={slug} slug={slug} />
        {sheet}
      </div>
    );
  }
  return (
    <div className="flex h-full min-h-0 flex-col md:flex-row">
      <div className="flex min-h-0 flex-col border-border md:w-96 md:shrink-0 md:border-r lg:w-[28rem]">
        <ProjectList selected={slug} onNew={() => setCreating(true)} />
      </div>
      {desktop && (
        <section aria-label="Selected project" className="min-h-0 min-w-0 flex-1 overflow-y-auto">
          {slug ? (
            <ProjectDetail key={slug} slug={slug} />
          ) : (
            <EmptyState icon={FolderKanban} title="Pick a project">
              Its lead, jobs, schedules and instructions open here.
            </EmptyState>
          )}
        </section>
      )}
      {sheet}
    </div>
  );
}

function ProjectList({ selected, onNew }: { selected?: string; onNew: () => void }) {
  const [archived, setArchived] = useState(false);
  const projects = useProjects(archived);
  return (
    <>
      <header className="flex min-h-11 shrink-0 items-center gap-2 border-b border-border bg-surface-raised px-3">
        <h1 className="text-sm font-semibold text-heading">Projects</h1>
        <HelpButton topic="concepts/projects-and-leads" what="projects, leads and the orchestrator" />
        <label className="ml-auto inline-flex min-h-11 items-center gap-1.5 text-xs text-muted md:min-h-7">
          <input type="checkbox" checked={archived} onChange={(event) => setArchived(event.target.checked)} className="h-4 w-4 accent-[var(--color-accent)]" />
          Show archived
        </label>
        <Button compact variant="primary" icon={Plus} onClick={onNew}>
          New project
        </Button>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="px-3 pt-2 empty:hidden">
          <Hint id="projects" topic="concepts/projects-and-leads">
            A project is a repository with its own standing lead: give the project a job and its lead plans it and hands it to agents in worktrees.
          </Hint>
        </div>
        {projects.isPending ? (
          <SkeletonRows rows={4} label="Loading projects" />
        ) : projects.isError ? (
          <Banner tone="danger" role="alert" action={<Button compact icon={RefreshCw} onClick={() => void projects.refetch()}>Retry</Button>}>
            Could not load projects: {projects.error.message}
          </Banner>
        ) : projects.data.projects.length === 0 ? (
          <EmptyState icon={FolderKanban} title="No projects yet" action={<Button variant="primary" icon={Plus} onClick={onNew}>New project</Button>}>
            A project is a directory with its own lead (a standing Claude), defaults for its jobs and its own instructions.
          </EmptyState>
        ) : (
          <>
            <ul aria-label="Projects" className="divide-y divide-border">
              {projects.data.projects.map((project) => (
                <ProjectRow key={project.id} project={project} selected={selected === project.slug} />
              ))}
            </ul>
            <p className="border-t border-border px-3 py-2 text-2xs text-muted">
              Unfiled: {projects.data.unfiled.needs_you} need you · {projects.data.unfiled.running} running · {projects.data.unfiled.total} in all
            </p>
          </>
        )}
      </div>
    </>
  );
}

function ProjectRow({ project, selected }: { project: ProjectView; selected: boolean }) {
  const chip = leadChip(project.lead);
  const running = leadRunning(project.lead);
  const action = useLeadAction(project.slug);
  const toast = useToast();
  const toggle = () =>
    action.mutate(running ? "stop" : "start", {
      onSuccess: () => toast.show({ tone: "success", title: running ? `${project.name}: lead stopped` : `${project.name}: lead started` }),
      onError: (error) => toast.show({ tone: "danger", title: "The lead did not change", description: error.message }),
    });
  return (
    <li className={cn("flex items-stretch", selected && "bg-selected")}>
      <Link
        to="/projects/$slug"
        params={{ slug: project.slug }}
        aria-current={selected || undefined}
        className="flex min-h-14 min-w-0 flex-1 flex-col gap-1 px-3 py-2.5 hover:bg-hover"
      >
        <span className="flex items-start gap-2">
          <span className="min-w-0 flex-1 truncate text-sm font-medium text-heading">{project.name}</span>
          {project.archived && <Chip>archived</Chip>}
          <Chip tone={chip.tone}>{chip.label}</Chip>
        </span>
        <span className="truncate font-mono text-2xs text-muted" title={project.dir}>
          {middlePath(project.dir)}
        </span>
        <span className="flex flex-wrap gap-1 text-2xs text-muted">
          {project.counts.needs_you > 0 && <Chip tone="danger">{project.counts.needs_you} need you</Chip>}
          {project.counts.running > 0 && <Chip tone="accent">{project.counts.running} running</Chip>}
          <span>
            {project.counts.total} job{project.counts.total === 1 ? "" : "s"}
          </span>
        </span>
      </Link>
      {!project.archived && (
        <div className="flex shrink-0 items-center pr-2">
          <Button
            compact
            variant="ghost"
            icon={running ? Square : Play}
            onClick={toggle}
            disabled={action.isPending}
            aria-label={`${running ? "Stop" : "Start"} ${project.name} lead`}
          >
            {running ? "Stop" : "Start"}
          </Button>
        </div>
      )}
    </li>
  );
}
