// ProjectSwitcher: the Jobs view's project choice (DESIGN.md 21.1): All, each project, Unfiled,
// each with how many of its jobs need you and how many run. One row of buttons that scrolls
// sideways inside itself on a phone (the page never does). The choice is in the URL
// (?project=<slug>) and remembered per browser (lib/projects.ts), through useProjectFilter.
import { cn } from "../../lib/cn";
import { filterToParam, type ProjectCounts, type ProjectFilter, type ProjectList } from "../../lib/projects";

function sum(counts: ProjectCounts[]): ProjectCounts {
  return counts.reduce((total, next) => ({ needs_you: total.needs_you + next.needs_you, running: total.running + next.running, total: total.total + next.total }), {
    needs_you: 0,
    running: 0,
    total: 0,
  });
}

export function ProjectSwitcher({ list, value, onChange }: { list: ProjectList; value: ProjectFilter; onChange: (filter: ProjectFilter) => void }) {
  const all = sum([...list.projects.map((project) => project.counts), list.unfiled]);
  const options: { filter: ProjectFilter; label: string; counts: ProjectCounts }[] = [
    { filter: { kind: "all" }, label: "All", counts: all },
    ...list.projects.map((project) => ({ filter: { kind: "project", slug: project.slug } as ProjectFilter, label: project.name, counts: project.counts })),
    { filter: { kind: "unfiled" }, label: "Unfiled", counts: list.unfiled },
  ];
  const current = filterToParam(value);
  return (
    <div role="radiogroup" aria-label="Project" className="relative flex gap-1 overflow-x-auto border-b border-border px-2 py-1 [scrollbar-width:thin]">
      {options.map((option) => {
        const selected = filterToParam(option.filter) === current;
        return (
          <button
            key={filterToParam(option.filter) || "all"}
            type="button"
            role="radio"
            aria-checked={selected}
            onClick={() => onChange(option.filter)}
            className={cn(
              "inline-flex min-h-11 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full border px-3 text-xs md:min-h-7",
              selected ? "border-accent bg-accent/10 font-medium text-heading" : "border-border text-body hover:bg-hover",
            )}
          >
            {option.label}
            {option.counts.needs_you > 0 && (
              <span className="inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-status-blocked px-1 text-[10px] font-semibold text-white tabular-nums dark:text-surface" title="Need you">
                {option.counts.needs_you}
                <span className="sr-only"> need you</span>
              </span>
            )}
            {option.counts.running > 0 && (
              <span className="text-2xs text-accent tabular-nums" title="Running">
                {option.counts.running}
                <span className="sr-only"> running</span>
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}
