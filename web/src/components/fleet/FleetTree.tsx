// FleetTree: the fleet as collapsible workspace groups, tabs and panes inside, with a filter box.
// Whatever needs Jeff sorts first at every level (lib/fleet.ts sortFleet). Selecting an agent
// calls onSelect with its pane id; the parent decides whether that opens a side panel or a sheet.
import { ChevronRight, Search, SearchX, Terminal as TerminalIcon, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { cn } from "../../lib/cn";
import { cleanTitle, cwdTail, filterFleet, needsYou, needsYouCount, sortFleet } from "../../lib/fleet";
import type { Fleet, Pane, PaneAgent, Tab, Workspace } from "../../lib/types";
import { Chip, StatusDot } from "../ui/Chip";
import { EmptyState } from "../ui/EmptyState";

interface FleetTreeProps {
  fleet: Fleet;
  selected?: string;
  onSelect: (paneId: string) => void;
}

export function FleetTree({ fleet, selected, onSelect }: FleetTreeProps) {
  const [query, setQuery] = useState("");
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
  const workspaces = useMemo(() => filterFleet(sortFleet(fleet.workspaces), query), [fleet, query]);
  const agents = fleet.agents;
  const urgent = needsYouCount(fleet);
  const filtering = query.trim() !== "";

  const toggle = (workspaceId: string) =>
    setCollapsed((current) => {
      const next = new Set(current);
      if (next.has(workspaceId)) next.delete(workspaceId);
      else next.add(workspaceId);
      return next;
    });

  return (
    <div className="flex min-h-0 flex-col">
      <div className="sticky top-0 z-10 border-b border-border bg-surface/95 px-3 pb-2 pt-3 backdrop-blur">
        <FilterBox value={query} onChange={setQuery} />
        <p className="mt-1.5 flex flex-wrap items-center gap-x-2 text-2xs text-muted">
          <span>
            {agents.length} {agents.length === 1 ? "agent" : "agents"} in {fleet.workspaces.length}{" "}
            {fleet.workspaces.length === 1 ? "workspace" : "workspaces"}
          </span>
          {urgent > 0 && <span className="font-medium text-danger">{urgent} need you</span>}
          <span className="ml-auto font-mono">herdr {fleet.herdr_version}</span>
        </p>
      </div>
      {workspaces.length === 0 ? (
        <EmptyState icon={SearchX} title="Nothing matches" className="h-auto py-10">
          No workspace, tab or agent matches “{query.trim()}”.
        </EmptyState>
      ) : (
        <ul aria-label="Workspaces" className="flex flex-col pb-4">
          {workspaces.map((workspace) => (
            <WorkspaceGroup
              key={workspace.id}
              workspace={workspace}
              open={filtering || !collapsed.has(workspace.id)}
              onToggle={() => toggle(workspace.id)}
              selected={selected}
              onSelect={onSelect}
              hideShells={filtering}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

// ---- Filter ------------------------------------------------------------------

function FilterBox({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const inputRef = useRef<HTMLInputElement>(null);

  // "/" focuses the filter from anywhere on the page, as in Grafana and GitHub.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (event.key !== "/" || target?.closest("input, textarea, [contenteditable]")) return;
      event.preventDefault();
      inputRef.current?.focus();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  return (
    <div className="relative">
      <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" aria-hidden />
      <input
        ref={inputRef}
        type="search"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => event.key === "Escape" && onChange("")}
        placeholder="Filter agents, workspaces, paths…"
        aria-label="Filter the fleet"
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        className="h-11 w-full rounded border border-border-strong bg-surface pl-8 pr-10 text-sm text-heading placeholder:text-muted md:h-8 md:text-xs [&::-webkit-search-cancel-button]:hidden"
      />
      {value ? (
        <button
          type="button"
          onClick={() => onChange("")}
          aria-label="Clear filter"
          className="absolute right-0 top-0 inline-flex h-11 w-11 items-center justify-center text-muted hover:text-heading md:h-8 md:w-8"
        >
          <X className="h-4 w-4" aria-hidden />
        </button>
      ) : (
        <kbd className="pointer-events-none absolute right-2 top-1/2 hidden -translate-y-1/2 rounded border border-border px-1 font-mono text-2xs text-muted md:block">
          /
        </kbd>
      )}
    </div>
  );
}

// ---- Groups ------------------------------------------------------------------

interface GroupProps {
  selected?: string;
  onSelect: (paneId: string) => void;
  hideShells: boolean;
}

function WorkspaceGroup({
  workspace,
  open,
  onToggle,
  ...rest
}: GroupProps & { workspace: Workspace; open: boolean; onToggle: () => void }) {
  const agents = workspace.tabs.flatMap((tab) => tab.panes.flatMap((pane) => (pane.agent ? [pane.agent] : [])));
  const urgent = agents.filter(needsYou).length;
  return (
    <li className="border-b border-border">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={open}
        className="flex min-h-11 w-full items-center gap-2 bg-surface-raised px-2 text-left hover:bg-hover md:min-h-9"
      >
        <ChevronRight className={cn("h-4 w-4 shrink-0 text-muted transition-transform", open && "rotate-90")} aria-hidden />
        <span className="inline-flex h-5 min-w-5 shrink-0 items-center justify-center rounded bg-surface-sunken px-1 font-mono text-2xs text-muted ring-1 ring-border">
          {workspace.number}
        </span>
        <span className="min-w-0 flex-1 truncate text-sm font-semibold text-heading md:text-xs">{workspace.label}</span>
        {urgent > 0 && <Chip tone="danger">{urgent} need you</Chip>}
        <span className="shrink-0 text-2xs tabular-nums text-muted">
          {agents.length} {agents.length === 1 ? "agent" : "agents"}
        </span>
      </button>
      {open && (
        <ul aria-label={`Tabs in ${workspace.label}`}>
          {workspace.tabs.map((tab) => (
            <TabGroup key={tab.id} tab={tab} {...rest} />
          ))}
        </ul>
      )}
    </li>
  );
}

function TabGroup({ tab, selected, onSelect, hideShells }: GroupProps & { tab: Tab }) {
  const panes = hideShells ? tab.panes.filter((pane) => pane.agent) : tab.panes;
  if (panes.length === 0) return null;
  return (
    <li>
      <div className="flex h-6 items-center gap-1.5 pl-8 pr-3 text-2xs uppercase tracking-wide text-muted">
        <span className="font-mono">t{tab.number}</span>
        <span className="truncate font-medium normal-case tracking-normal">{tab.label}</span>
      </div>
      <ul aria-label={`Panes in ${tab.label}`}>
        {panes.map((pane) =>
          pane.agent ? (
            <li key={pane.id}>
              <AgentRow agent={pane.agent} cwd={pane.cwd} selected={selected === pane.id} onSelect={() => onSelect(pane.id)} />
            </li>
          ) : (
            <li key={pane.id}>
              <ShellRow pane={pane} />
            </li>
          ),
        )}
      </ul>
    </li>
  );
}

// ---- Rows --------------------------------------------------------------------

/** One agent: state dot, name, kind; terminal title and cwd tail on the second line. */
export function AgentRow({
  agent,
  cwd,
  selected,
  onSelect,
}: {
  agent: PaneAgent;
  cwd: string;
  selected: boolean;
  onSelect: () => void;
}) {
  const urgent = needsYou(agent);
  const title = cleanTitle(agent.title);
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={selected ? "true" : undefined}
      data-needs-you={urgent || undefined}
      className={cn(
        "group relative flex min-h-13 w-full flex-col justify-center gap-0.5 py-1.5 pl-8 pr-3 text-left hover:bg-hover md:min-h-11",
        selected && "bg-selected hover:bg-selected",
      )}
    >
      <span
        aria-hidden
        className={cn(
          "absolute inset-y-1 left-0 w-0.5 rounded-r",
          selected ? "bg-accent" : urgent ? "bg-status-blocked" : "bg-transparent",
        )}
      />
      <span className="flex min-w-0 items-center gap-2">
        <StatusDot status={agent.status} />
        <span className="min-w-0 truncate text-sm font-medium text-heading md:text-xs">{agent.name}</span>
        <Chip mono>{agent.kind}</Chip>
        {urgent && <Chip tone="danger">needs you</Chip>}
      </span>
      <span className="flex min-w-0 items-center gap-2 pl-[18px] text-2xs text-muted">
        <span className="min-w-0 flex-1 truncate" title={agent.title}>
          {title || <span className="italic">no title</span>}
        </span>
        <span className="max-w-[45%] shrink-0 truncate font-mono" title={cwd}>
          {cwdTail(cwd)}
        </span>
      </span>
    </button>
  );
}

function ShellRow({ pane }: { pane: Pane }) {
  return (
    <div className="flex h-7 min-w-0 items-center gap-2 pl-8 pr-3 text-2xs text-muted">
      <TerminalIcon className="h-3 w-3 shrink-0 opacity-70" aria-hidden />
      <span>shell</span>
      <span className="ml-auto max-w-[60%] truncate font-mono" title={pane.cwd}>
        {cwdTail(pane.cwd)}
      </span>
    </div>
  );
}
