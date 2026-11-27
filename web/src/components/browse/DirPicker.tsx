// DirPicker: the folder picker (DESIGN.md 4.23, 12). A sheet (full screen on a phone, a modal from
// md) over the server's read-only browser: a breadcrumb, a box that filters the listing or takes a
// pasted path (/… or ~/…), places to start from (home, ~/Developer, ~/Work, projects, recent
// choices), repositories badged and floated to the top, and the facts about the highlighted folder
// with what to watch out for, beside a "Select this folder" button that never scrolls away.
//
// Keys, wherever focus is in the picker: ↑/↓ move the highlight, Enter (or →) opens it, Backspace
// in an empty box (or ←) goes up, Cmd/Ctrl+Enter selects. A click highlights, a double click or
// the row's chevron opens. Selecting picks the highlighted folder, or the open one when nothing is
// highlighted. Navigation fetches first and moves only on success, so a refused path leaves the
// listing where it was and says why.
import { useQueryClient } from "@tanstack/react-query";
import { ArrowUp, ChevronRight, File, Folder, FolderGit2, FolderSymlink, Home, Lock } from "lucide-react";
import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { endpoints, queryKeys } from "../../lib/api";
import {
  canEnter,
  factsSummary,
  filterEntries,
  folderWarnings,
  hostNote,
  isPathInput,
  recentFolders,
  rememberFolder,
  tildePath,
  type BrowseEntry,
  type BrowseOptions,
  type FolderFacts,
  type Listing,
  type Place,
} from "../../lib/browse";
import { cn } from "../../lib/cn";
import { relativeTime } from "../../lib/format";
import { useBrowse, useBrowsePlaces, useFolderFacts } from "../../lib/queries";
import { Button } from "../ui/Button";
import { Sheet } from "../ui/Sheet";

export interface DirPickerProps {
  open: boolean;
  onClose: () => void;
  /** Called with the chosen folder's absolute path and, when loaded, its facts. */
  onSelect: (path: string, facts?: FolderFacts) => void;
  /** Where to start (a typed path, the current value); home when empty or refused. */
  initialPath?: string;
  title?: string;
  /** What the folder is for: a project's warnings differ from a job's. */
  purpose?: "project" | "job";
}

/** The picker in its sheet; the body mounts only while open, so each opening starts fresh. */
export function DirPicker({ open, onClose, title = "Choose a folder", ...rest }: DirPickerProps) {
  return (
    <Sheet open={open} onClose={onClose} title={title} size="lg">
      {open && <PickerBody onClose={onClose} {...rest} />}
    </Sheet>
  );
}

/** How long the highlight must rest before its facts are fetched (arrowing through a list). */
const FACTS_DELAY_MS = 150;

function useDebounced<T>(value: T, delay: number): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return settled;
}

function PickerBody({ onClose, onSelect, initialPath, purpose = "project" }: Omit<DirPickerProps, "open" | "title">) {
  const ids = useId();
  const queryClient = useQueryClient();
  const [path, setPath] = useState("");
  const [filter, setFilter] = useState("");
  const [highlight, setHighlight] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [options, setOptions] = useState<BrowseOptions>({ files: false, hidden: false });
  const filterRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const places = useBrowsePlaces();
  const listing = useBrowse(path, options);
  const home = places.data?.home;

  /** Fetches path and moves there on success; on a refusal keeps the listing and says why. */
  const go = useCallback(
    async (target: string, select?: string | null): Promise<boolean> => {
      try {
        const next = await queryClient.fetchQuery({ queryKey: queryKeys.browse(target, options), queryFn: () => endpoints.browse(target, options), staleTime: 5_000 });
        setPath(next.path);
        setFilter("");
        setHighlight(select ?? null);
        setError(null);
        return true;
      } catch (failure) {
        setError(failure instanceof Error ? failure.message : String(failure));
        return false;
      }
    },
    [queryClient, options],
  );

  // Start at the given path, else home.
  useEffect(() => {
    const start = initialPath?.trim();
    void (async () => {
      if (start && (await go(start))) return;
      await go("~");
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once, at opening
  }, []);

  const data = listing.data;
  const shown = useMemo(() => filterEntries(data?.entries ?? [], filter), [data, filter]);
  const highlighted = shown.find((entry) => entry.path === highlight) ?? null;
  const target = highlighted ? (canEnter(highlighted) ? highlighted.path : "") : (data?.path ?? "");
  const factsPath = useDebounced(target, FACTS_DELAY_MS);
  const facts = useFolderFacts(factsPath);
  const currentFacts = facts.data && facts.data.path === target ? facts.data : undefined;

  // Keep the highlighted row in view as the keys move it.
  useEffect(() => {
    if (!highlight) return;
    const row = listRef.current?.querySelector<HTMLElement>(`[data-path="${CSS.escape(highlight)}"]`);
    row?.scrollIntoView?.({ block: "nearest" });
  }, [highlight]);

  const up = () => {
    if (data?.parent) void go(data.parent, data.path);
  };
  const enter = (entry: BrowseEntry | null) => {
    if (entry && canEnter(entry)) void go(entry.path);
  };
  const select = () => {
    if (!target) return;
    rememberFolder(target);
    onSelect(target, currentFacts);
    onClose();
  };
  const move = (step: number) => {
    const choices = shown.filter((entry) => entry.dir);
    if (choices.length === 0) return;
    const index = choices.findIndex((entry) => entry.path === highlight);
    const next = index < 0 ? (step > 0 ? 0 : choices.length - 1) : Math.min(choices.length - 1, Math.max(0, index + step));
    setHighlight(choices[next]?.path ?? null);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const inBox = event.target === filterRef.current;
    const boxEmpty = filter === "";
    if (event.target instanceof HTMLInputElement && !inBox) return; // the checkboxes
    switch (event.key) {
      case "ArrowDown":
      case "ArrowUp":
        event.preventDefault();
        move(event.key === "ArrowDown" ? 1 : -1);
        return;
      case "Enter":
        event.preventDefault();
        if (event.metaKey || event.ctrlKey) select();
        else if (inBox && isPathInput(filter)) void go(filter.trim());
        else enter(highlighted);
        return;
      case "ArrowRight":
        if (!inBox || boxEmpty) {
          event.preventDefault();
          enter(highlighted);
        }
        return;
      case "ArrowLeft":
      case "Backspace":
        if (!inBox || boxEmpty) {
          event.preventDefault();
          up();
        }
        return;
    }
  };

  const recent = recentFolders();
  return (
    <div className="flex h-full flex-col md:h-[72vh]" onKeyDown={onKeyDown}>
      <div className="flex shrink-0 flex-col gap-2 border-b border-border p-2">
        <Breadcrumb listing={data} onGo={(crumb) => void go(crumb)} onUp={up} />
        <div className="flex items-center gap-2">
          <label htmlFor={`${ids}-filter`} className="sr-only">
            Filter folders, or paste a path
          </label>
          <input
            ref={filterRef}
            id={`${ids}-filter`}
            value={filter}
            onChange={(event) => {
              setFilter(event.target.value);
              setError(null);
            }}
            autoFocus
            autoComplete="off"
            spellCheck={false}
            placeholder="Filter, or paste a path (/… or ~/…)"
            aria-controls={`${ids}-list`}
            className="min-h-11 w-full min-w-0 flex-1 rounded border border-border-strong bg-surface px-2 font-mono text-sm text-heading placeholder:font-sans placeholder:text-muted focus-visible:outline-2 focus-visible:outline-accent md:min-h-8"
          />
          <Toggle label="Hidden" checked={!!options.hidden} onChange={(hidden) => setOptions({ ...options, hidden })} />
          <Toggle label="Files" checked={!!options.files} onChange={(files) => setOptions({ ...options, files })} />
        </div>
        <PlaceChips places={places.data?.favourites ?? []} recent={recent} home={home} onGo={(place) => void go(place)} />
        {error && (
          <p role="alert" className="text-xs text-danger">
            {error}
          </p>
        )}
      </div>
      <div className="flex min-h-0 flex-1">
        <PlaceList places={places.data?.favourites ?? []} recent={recent} home={home} current={data?.path} onGo={(place) => void go(place)} />
        <div className="min-h-0 min-w-0 flex-1 overflow-y-auto">
          {isPathInput(filter) && (
            <button
              type="button"
              onClick={() => void go(filter.trim())}
              className="flex min-h-11 w-full items-center gap-2 border-b border-border px-3 text-left text-xs text-accent hover:bg-hover md:min-h-8"
            >
              <ChevronRight className="h-4 w-4" aria-hidden /> Go to <span className="font-mono">{filter.trim()}</span> (Enter)
            </button>
          )}
          <ul id={`${ids}-list`} ref={listRef} aria-label={data ? `Folders in ${data.label}` : "Folders"} className="divide-y divide-border">
            {shown.map((entry) => (
              <EntryRow
                key={entry.path}
                entry={entry}
                highlighted={entry.path === highlight}
                onHighlight={() => setHighlight(entry.path)}
                onOpen={() => enter(entry)}
              />
            ))}
          </ul>
          <ListFooter listing={data} shown={shown.length} filtered={filter !== "" && !isPathInput(filter)} loading={listing.isPending && path !== ""} />
        </div>
      </div>
      <FactsBar target={target} home={home} facts={currentFacts} loading={facts.isFetching} purpose={purpose} onCancel={onClose} onSelect={select} />
    </div>
  );
}

// ---- Pieces ---------------------------------------------------------------------------------

function Toggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (value: boolean) => void }) {
  return (
    <label className="inline-flex min-h-11 shrink-0 cursor-pointer items-center gap-1 text-2xs text-muted md:min-h-8">
      <input type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} className="h-4 w-4 accent-accent" />
      {label}
    </label>
  );
}

function Breadcrumb({ listing, onGo, onUp }: { listing?: Listing; onGo: (path: string) => void; onUp: () => void }) {
  if (!listing) return <div className="min-h-8 text-xs text-muted">Loading…</div>;
  // From the first browsable step (the root and below); the steps above it are not links.
  const first = Math.max(0, listing.crumbs.findIndex((crumb) => crumb.browsable));
  const crumbs = listing.crumbs.slice(first);
  return (
    <nav aria-label="Folder path" className="flex min-w-0 items-center gap-1">
      <button
        type="button"
        onClick={onUp}
        disabled={!listing.parent}
        aria-label="Up one folder"
        title="Up one folder (Backspace)"
        className="inline-flex min-h-11 min-w-11 shrink-0 items-center justify-center rounded text-muted hover:bg-hover hover:text-heading disabled:opacity-40 md:min-h-7 md:min-w-7"
      >
        <ArrowUp className="h-4 w-4" aria-hidden />
      </button>
      <ol className="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto whitespace-nowrap font-mono text-xs">
        {crumbs.map((crumb, index) => {
          const last = index === crumbs.length - 1;
          return (
            <li key={crumb.path} className="flex items-center gap-0.5">
              {index > 0 && <span className="text-muted">/</span>}
              {last ? (
                <span aria-current="page" className="px-1 font-semibold text-heading">
                  {crumb.name}
                </span>
              ) : (
                <button type="button" onClick={() => onGo(crumb.path)} className="min-h-11 rounded px-1 text-accent hover:bg-hover md:min-h-7">
                  {crumb.name}
                </button>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

interface PlacesProps {
  places: Place[];
  recent: string[];
  home?: string;
  onGo: (path: string) => void;
}

const placeGroups: Array<{ title: string; kinds: Place["kind"][] }> = [
  { title: "Places", kinds: ["home", "favourite", "root"] },
  { title: "Projects", kinds: ["project"] },
  { title: "Project folders", kinds: ["project-parent"] },
  { title: "Remembered", kinds: ["remembered", "recent"] },
];

/** The places grouped, plus this browser's recent choices, each path once. */
function groupPlaces(places: Place[], recent: string[], home?: string): Array<{ title: string; items: Place[] }> {
  const seen = new Set<string>();
  const take = (items: Place[]) => items.filter((place) => (seen.has(place.path) ? false : (seen.add(place.path), true)));
  const [first, ...rest] = placeGroups.map(({ title, kinds }) => ({ title, items: take(places.filter((place) => kinds.includes(place.kind))) }));
  const recentPlaces = take(recent.map((path) => ({ path, label: tildePath(path, home), kind: "recent" as const })));
  return [...(first ? [first] : []), { title: "Recent", items: recentPlaces }, ...rest].filter((group) => group.items.length > 0);
}

function placeLabel(place: Place, home?: string): string {
  return place.kind === "project" ? place.label : tildePath(place.path, home);
}

/** The places as a sidebar from md. */
function PlaceList({ places, recent, home, current, onGo }: PlacesProps & { current?: string }) {
  const groups = groupPlaces(places, recent, home);
  return (
    <nav aria-label="Places" className="hidden w-52 shrink-0 overflow-y-auto border-r border-border bg-surface-raised py-1 md:block">
      {groups.map((group) => (
        <div key={group.title} className="py-1">
          <h3 className="px-3 py-0.5 text-2xs font-semibold uppercase tracking-wide text-muted">{group.title}</h3>
          <ul>
            {group.items.map((place) => (
              <li key={place.path}>
                <button
                  type="button"
                  onClick={() => onGo(place.path)}
                  title={place.path}
                  aria-current={place.path === current ? "location" : undefined}
                  className={cn(
                    "flex min-h-7 w-full items-center gap-1.5 px-3 text-left text-xs text-body hover:bg-hover",
                    place.path === current && "bg-selected text-heading",
                  )}
                >
                  <PlaceIcon place={place} />
                  <span className="truncate">{placeLabel(place, home)}</span>
                </button>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}

/** The places as a scrolling row of chips below md. */
function PlaceChips({ places, recent, home, onGo }: PlacesProps) {
  const items = groupPlaces(places, recent, home).flatMap((group) => group.items);
  if (items.length === 0) return null;
  return (
    <nav aria-label="Places" className="-mx-2 flex gap-1 overflow-x-auto px-2 md:hidden">
      {items.map((place) => (
        <button
          key={place.path}
          type="button"
          onClick={() => onGo(place.path)}
          title={place.path}
          className="inline-flex min-h-11 shrink-0 items-center gap-1 rounded-full border border-border bg-surface-raised px-3 text-xs text-body hover:bg-hover"
        >
          <PlaceIcon place={place} />
          {placeLabel(place, home)}
        </button>
      ))}
    </nav>
  );
}

function PlaceIcon({ place }: { place: Place }) {
  const Icon = place.kind === "home" ? Home : place.git ? FolderGit2 : Folder;
  return <Icon className={cn("h-3.5 w-3.5 shrink-0", place.git ? "text-accent" : "text-muted")} aria-hidden />;
}

function EntryRow({ entry, highlighted, onHighlight, onOpen }: { entry: BrowseEntry; highlighted: boolean; onHighlight: () => void; onOpen: () => void }) {
  const enterable = canEnter(entry);
  const Icon = !entry.dir ? File : !entry.readable && !entry.outside ? Lock : entry.symlink ? FolderSymlink : entry.git ? FolderGit2 : Folder;
  const badges: ReactNode[] = [];
  if (entry.git) badges.push(<Badge key="git" tone="accent">git</Badge>);
  if (entry.symlink && entry.target) badges.push(<span key="link" className="truncate font-mono text-2xs text-muted">→ {entry.target}</span>);
  if (entry.outside) badges.push(<Badge key="out" tone="warning">outside</Badge>);
  else if (entry.error) badges.push(<Badge key="err" tone="warning">{entry.readable ? entry.error : "no access"}</Badge>);
  return (
    <li data-path={entry.path} className={cn("flex items-stretch", highlighted && "bg-selected")}>
      <button
        type="button"
        onClick={entry.dir ? onHighlight : undefined}
        onDoubleClick={onOpen}
        disabled={!entry.dir}
        aria-current={highlighted ? "true" : undefined}
        className={cn(
          "flex min-h-12 min-w-0 flex-1 items-center gap-2 px-3 text-left text-sm hover:bg-hover disabled:cursor-default disabled:hover:bg-transparent md:min-h-8",
          entry.dir ? "text-heading" : "text-muted",
          entry.hidden && "opacity-75",
        )}
      >
        <Icon className={cn("h-4 w-4 shrink-0", entry.git ? "text-accent" : "text-muted")} aria-hidden />
        <span className="truncate">{entry.name}</span>
        <span className="flex min-w-0 items-center gap-1">{badges}</span>
        <span className="ml-auto hidden shrink-0 pl-2 font-mono text-2xs text-muted sm:inline">{entry.mtime && !entry.mtime.startsWith("0001") ? relativeTime(entry.mtime) : ""}</span>
      </button>
      {entry.dir && (
        <button
          type="button"
          onClick={onOpen}
          disabled={!enterable}
          aria-label={`Open ${entry.name}`}
          className="inline-flex min-h-12 w-11 shrink-0 items-center justify-center text-muted hover:bg-hover hover:text-heading disabled:opacity-30 md:min-h-8 md:w-8"
        >
          <ChevronRight className="h-4 w-4" aria-hidden />
        </button>
      )}
    </li>
  );
}

function Badge({ tone, children }: { tone: "accent" | "warning"; children: ReactNode }) {
  return (
    <span className={cn("shrink-0 rounded px-1 text-2xs font-medium", tone === "accent" ? "bg-accent/10 text-accent" : "bg-warning/10 text-warning")}>{children}</span>
  );
}

function ListFooter({ listing, shown, filtered, loading }: { listing?: Listing; shown: number; filtered: boolean; loading: boolean }) {
  if (loading && !listing) return <p className="p-3 text-xs text-muted">Reading the folder…</p>;
  if (!listing) return null;
  const notes = [
    listing.more > 0 ? `${listing.more} more not shown (filter to find them)` : "",
    listing.hidden_count > 0 ? `${listing.hidden_count} hidden` : "",
    listing.file_count > 0 ? `${listing.file_count} file${listing.file_count === 1 ? "" : "s"}` : "",
    listing.partial ? "the folder was slow; some details are missing" : "",
  ].filter(Boolean);
  return (
    <p className="px-3 py-2 text-2xs text-muted">
      {shown === 0 ? (filtered ? "Nothing here matches. " : "No folders here. ") : ""}
      {notes.join(" · ")}
    </p>
  );
}

interface FactsBarProps {
  target: string;
  home?: string;
  facts?: FolderFacts;
  loading: boolean;
  purpose: "project" | "job";
  onCancel: () => void;
  onSelect: () => void;
}

/** The chosen folder, its facts and warnings, and the buttons: always in view. */
function FactsBar({ target, home, facts, loading, purpose, onCancel, onSelect }: FactsBarProps) {
  const warnings = facts ? folderWarnings(facts, purpose) : [];
  const note = facts ? hostNote(facts) : "";
  return (
    <div className="shrink-0 border-t border-border bg-surface-raised p-2">
      <div aria-live="polite" className="flex min-h-10 flex-col gap-0.5 px-1 pb-2 text-xs">
        <span className="truncate font-mono text-heading" title={target}>
          {target ? tildePath(target, home) : "Pick a folder"}
        </span>
        {facts ? (
          <>
            <span className="text-body">{factsSummary(facts)}</span>
            {note && <span className="text-muted">{note}</span>}
            {warnings.map((warning) => (
              <span key={warning.text} className={warning.tone === "danger" ? "text-danger" : warning.tone === "warning" ? "text-warning" : "text-muted"}>
                {warning.text}
              </span>
            ))}
          </>
        ) : (
          target && <span className="text-muted">{loading ? "Looking…" : ""}</span>
        )}
      </div>
      <div className="flex justify-end gap-2">
        <Button variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button variant="primary" onClick={onSelect} disabled={!target} title="Cmd/Ctrl+Enter">
          Select this folder
        </Button>
      </div>
    </div>
  );
}
