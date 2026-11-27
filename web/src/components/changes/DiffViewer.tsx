// The diff viewer (DESIGN.md 6.21): one job's, lead's or the orchestrator's changes, live. A scope
// switch (all, commits on this branch, uncommitted, since you viewed), a file list with status,
// diffstat bars, a filter and Viewed ticks, and the files themselves: sticky headers, a Viewed
// checkbox that collapses the file and is tied to its content (a file that changes again comes
// back flagged, with "since viewed" to see only what changed), unified or split rows (split from
// lg, remembered), whitespace and wrap toggles, and keys: j/k file, n/p change, v viewed, f
// fullscreen, Esc out of it. On a phone: one file at a time, the list as a sheet, Previous, Viewed
// and Next at the bottom within thumb reach, and a swipe moves between files.
//
// Fullscreen is the standalone route (/diff?job=j_12, /diff?project=<slug>): the same component
// with the app's chrome hidden, so it can be opened in its own tab and deep-linked. Entering and
// leaving it keeps the scope, the file and the scroll position (sessionStorage).
import {
  Check,
  ChevronDown,
  ChevronRight,
  ChevronsDownUp,
  ChevronsUpDown,
  Columns2,
  Copy,
  ExternalLink,
  Eye,
  EyeOff,
  FileWarning,
  Filter,
  List,
  Maximize2,
  Minimize2,
  PanelLeftClose,
  PanelLeftOpen,
  RefreshCw,
  Rows3,
  Space,
  TriangleAlert,
  WrapText,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { cn } from "../../lib/cn";
import {
  loadPrefs,
  positionKey,
  savePrefs,
  statCells,
  statusLetter,
  step,
  viewedText,
  visibleFiles,
  whoSearch,
  type ChangeScope,
  type DiffFile,
  type DiffSummary,
  type DiffWho,
  type Position,
  type ViewerPrefs,
} from "../../lib/changes";
import { changesApi, changesKeys, useDiffFile, useDiffSummary, useMarks } from "../../lib/changes-queries";
import { describeHidden, findHidden, hidesSomething } from "../../lib/hidden";
import { Revealed } from "../ui/Revealed";
import { useQueryClient } from "@tanstack/react-query";
import { useMediaQuery } from "../../lib/media";
import { focusContext, isMacPlatform } from "../../lib/hotkeys";
import { Banner } from "../ui/Banner";
import { Button, IconButton } from "../ui/Button";
import { Chip } from "../ui/Chip";
import { Sheet } from "../ui/Sheet";
import { SkeletonRows } from "../ui/Skeleton";
import { useToast } from "../ui/toast-context";
import { FileDiff } from "./FileDiff";
import { HelpButton } from "../help/HelpButton";

export interface DiffViewerProps {
  who: DiffWho;
  /** Rendered by the standalone route, with the app's chrome hidden. */
  fullscreen?: boolean;
  initialScope?: ChangeScope;
  initialFile?: string;
  initialBase?: string;
  /** Told when the scope, base or current file changes (the route keeps them in its URL). */
  onState?: (state: { scope: ChangeScope; file?: string; base: string }) => void;
  /** Fullscreen on (from the embedded viewer) or off (from the route). */
  onFullscreen?: (on: boolean, state: { scope: ChangeScope; file?: string; base: string }) => void;
  /** The heading shown above the toolbar ("" for none). */
  title?: ReactNode;
  className?: string;
}

const SCOPE_SHORT: Record<ChangeScope, string> = { all: "All", committed: "Commits", uncommitted: "Uncommitted", since_viewed: "Since viewed" };

export function DiffViewer({ who, fullscreen, initialScope, initialFile, initialBase, onState, onFullscreen, title, className }: DiffViewerProps) {
  const [scope, setScope] = useState<ChangeScope>(initialScope ?? "all");
  const [base, setBase] = useState(initialBase ?? "");
  const [filter, setFilter] = useState("");
  const [prefs, setPrefsState] = useState<ViewerPrefs>(() => loadPrefs());
  const [current, setCurrent] = useState<string | undefined>(initialFile ?? readPosition(who).file);
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const [since, setSince] = useState<Record<string, boolean>>({});
  const [listSheet, setListSheet] = useState(false);
  const wide = useMediaQuery("(min-width: 1024px)");
  const phone = !useMediaQuery("(min-width: 768px)");
  const scroller = useRef<HTMLDivElement>(null);
  const queryClient = useQueryClient();
  const toast = useToast();
  const summaryQuery = useDiffSummary(who, scope, base);
  const summary = summaryQuery.data;
  const { mark, unmark } = useMarks(who, scope, base);
  // Side by side only when the viewer itself is wide (the job panel is narrower than the window).
  const [roomy, setRoomy] = useState(true);
  const summaryLoaded = !!summaryQuery.data;
  const sectionRef = useRef<HTMLElement>(null);
  useEffect(() => {
    const element = sectionRef.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver((entries) => setRoomy((entries[0]?.contentRect.width ?? 0) >= 960));
    observer.observe(element);
    return () => observer.disconnect();
  }, [summaryLoaded, phone]);
  const split = prefs.split && wide && roomy;
  // A narrow viewer (the job panel) starts with the file list hidden, to give the diff the room.
  const [narrowList, setNarrowList] = useState(false);
  const listShown = roomy ? prefs.listOpen : narrowList;

  const setPrefs = (change: Partial<ViewerPrefs>) =>
    setPrefsState((now) => {
      const next = { ...now, ...change };
      savePrefs(next);
      return next;
    });

  const files = useMemo(() => visibleFiles(summary?.files ?? [], filter, prefs.hideViewed), [summary, filter, prefs.hideViewed]);
  const index = files.findIndex((file) => file.path === current);
  const stateNow = useCallback(() => ({ scope, file: current, base }), [scope, current, base]);
  useEffect(() => onState?.({ scope, file: current, base }), [scope, current, base, onState]);

  // Open or collapsed is remembered per version of a file: one that changes again opens again.
  const openKey = (file: DiffFile) => file.path + "\x00" + file.hash;
  const isOpen = (file: DiffFile) => open[openKey(file)] ?? !(file.viewed || file.churn || file.too_large);
  const sinceOn = (file: DiffFile) => file.changed_since_viewed && file.since_available && (since[file.path] ?? scope === "since_viewed");

  // ---- Position: the current file follows the scroll, and is kept for fullscreen and new tabs.
  const restored = useRef(false);
  useEffect(() => {
    if (restored.current || !summary || phone) return;
    restored.current = true;
    const position = readPosition(who);
    const target = initialFile ?? position.file;
    if (!target) return;
    const go = () => scrollToFile(scroller.current, target, initialFile ? 0 : (position.offset ?? 0));
    requestAnimationFrame(go);
    setTimeout(go, 400); // again once the bodies above it have loaded
  }, [summary, who, initialFile, phone]);
  // A jump (a click in the list, j/k) owns the current file for a moment: the scroll it causes,
  // clamped at the end of the list, must not hand "current" to a neighbour.
  const pinned = useRef(0);
  const onScroll = useCallback(() => {
    const container = scroller.current;
    if (!container || phone || Date.now() < pinned.current) return;
    const top = container.getBoundingClientRect().top;
    let found: { path: string; offset: number } | null = null;
    for (const section of container.querySelectorAll<HTMLElement>("[data-file-section]")) {
      const rect = section.getBoundingClientRect();
      if (rect.top - top <= 48) found = { path: section.dataset.fileSection ?? "", offset: top - rect.top };
      else break;
    }
    if (found) {
      setCurrent(found.path);
      writePosition(who, { file: found.path, offset: Math.max(0, Math.round(found.offset)) });
    }
  }, [who, phone]);

  const goToFile = useCallback(
    (path: string | undefined) => {
      if (!path) return;
      setCurrent(path);
      writePosition(who, { file: path, offset: 0 });
      pinned.current = Date.now() + 600;
      if (!phone) scrollToFile(scroller.current, path, 0);
    },
    [who, phone],
  );

  const toggleViewed = useCallback(
    (file: DiffFile) => {
      const onError = (error: Error) => toast.show({ tone: "danger", title: "Could not change the mark", description: error.message });
      if (file.viewed) {
        setOpen((now) => ({ ...now, [file.path + "\x00" + file.hash]: true }));
        unmark.mutate([file.path], { onError });
      } else {
        setOpen((now) => ({ ...now, [file.path + "\x00" + file.hash]: false }));
        mark.mutate({ files: [{ path: file.path, hash: file.hash }] }, { onError });
      }
    },
    [mark, unmark, toast],
  );

  const fullscreenToggle = useCallback(() => {
    writePosition(who, { ...readPosition(who), file: current });
    onFullscreen?.(!fullscreen, stateNow());
  }, [who, current, fullscreen, onFullscreen, stateNow]);

  const refresh = async () => {
    try {
      const fresh = await changesApi.summary(who, scope, base, true);
      queryClient.setQueryData(changesKeys.summary(who, scope, base), fresh);
    } catch (error) {
      toast.show({ tone: "danger", title: "Refresh failed", description: (error as Error).message });
    }
  };

  // ---- Keys: j/k file, n/p change, v viewed, f fullscreen, Esc out of fullscreen.
  const gAt = useRef(0);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return;
      const focus = focusContext(event.target, isMacPlatform());
      if (focus.typing || focus.terminal) return;
      if (document.querySelector("[role=dialog][aria-modal=true]")) return; // a sheet or the palette is open
      const now = Date.now();
      if (event.key === "g") {
        gAt.current = now;
        return;
      }
      if (now - gAt.current < 1500) return; // g then a letter is the app's "go to"
      switch (event.key) {
        case "j":
          goToFile(files[step(index, 1, files.length)]?.path);
          break;
        case "k":
          goToFile(files[step(index, -1, files.length)]?.path);
          break;
        case "n":
        case "p":
          jumpToChange(scroller.current, event.key === "n" ? 1 : -1);
          break;
        case "v": {
          const file = files[index];
          if (file) toggleViewed(file);
          break;
        }
        case "f":
          if (onFullscreen) fullscreenToggle();
          break;
        case "Escape":
          if (fullscreen && onFullscreen) fullscreenToggle();
          else return;
          break;
        default:
          return;
      }
      event.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [files, index, goToFile, toggleViewed, fullscreen, onFullscreen, fullscreenToggle]);

  if (summaryQuery.isPending) return <SkeletonRows rows={8} label="Reading the diff" />;
  if (summaryQuery.isError && !summary) {
    return (
      <Banner tone={(summaryQuery.error as { status?: number }).status === 410 ? "info" : "danger"} role="alert">
        {summaryQuery.error.message}
      </Banner>
    );
  }
  if (!summary) return null;

  const toolbar = (
    <Toolbar
      summary={summary}
      scope={scope}
      setScope={(next) => {
        setScope(next);
        setSince({});
      }}
      base={base}
      setBase={setBase}
      prefs={prefs}
      setPrefs={setPrefs}
      wide={wide && roomy}
      phone={phone}
      listShown={listShown}
      onToggleList={() => (roomy ? setPrefs({ listOpen: !prefs.listOpen }) : setNarrowList(!narrowList))}
      fullscreen={!!fullscreen}
      canFullscreen={!!onFullscreen}
      onFullscreen={fullscreenToggle}
      onRefresh={() => void refresh()}
      refreshing={summaryQuery.isFetching}
      onOpenList={() => setListSheet(true)}
      onExpandAll={(value) => setOpen(Object.fromEntries((summary.files ?? []).map((file) => [openKey(file), value])))}
      onMarkAll={() =>
        mark.mutate({ files: [], all: true }, { onError: (error) => toast.show({ tone: "danger", title: "Could not mark them", description: error.message }) })
      }
      onClearMarks={() => unmark.mutate([], { onError: (error) => toast.show({ tone: "danger", title: "Could not clear", description: error.message }) })}
      who={who}
    />
  );
  const list = (
    <FileList
      files={files}
      all={summary.files}
      current={current}
      filter={filter}
      setFilter={setFilter}
      onPick={(path) => {
        goToFile(path);
        setListSheet(false);
      }}
      onToggleViewed={toggleViewed}
    />
  );
  const section = (file: DiffFile) => (
    <FileSection
      key={file.path}
      who={who}
      base={base}
      scope={scope}
      file={file}
      open={phone || isOpen(file)}
      onOpen={(value) => setOpen((now) => ({ ...now, [openKey(file)]: value }))}
      since={sinceOn(file)}
      onSince={(value) => {
        setSince((now) => ({ ...now, [file.path]: value }));
        setOpen((now) => ({ ...now, [openKey(file)]: true }));
      }}
      split={split}
      wrap={prefs.wrap}
      ignoreWhitespace={prefs.ignoreWhitespace}
      onToggleViewed={() => toggleViewed(file)}
      current={file.path === current}
      phone={phone}
    />
  );
  const empty = (
    <p className="p-4 text-sm text-muted">
      {summary.total === 0
        ? scope === "since_viewed"
          ? "Nothing new: every file is marked viewed and none has changed since."
          : "No changes here."
        : prefs.hideViewed && files.length === 0 && !filter
          ? `All ${summary.total} files are viewed. Turn off “Hide viewed” to see them.`
          : "No file matches the filter."}
    </p>
  );

  if (phone) {
    const file = files[index] ?? files[0];
    const position = file ? files.indexOf(file) : -1;
    return (
      <section aria-label={`Changes: ${summary.label}`} className={cn("flex min-h-0 flex-col", fullscreen ? "h-full" : "min-h-[70vh]", className)}>
        {title}
        {toolbar}
        <SwipeArea className="min-h-0 flex-1 overflow-y-auto" onSwipe={(by) => goToFile(files[step(position, by, files.length)]?.path)}>
          {file ? section(file) : empty}
        </SwipeArea>
        {file && (
          <nav aria-label="Files" className="sticky bottom-0 z-10 flex items-center gap-2 border-t border-border bg-surface-raised px-2 py-1.5 pb-[max(0.375rem,env(safe-area-inset-bottom))]">
            <Button compact icon={ChevronRight} className="[&>svg]:rotate-180" disabled={position <= 0} onClick={() => goToFile(files[position - 1]?.path)}>
              Previous
            </Button>
            <span className="min-w-0 flex-1 text-center text-2xs text-muted">
              {position + 1} / {files.length}
            </span>
            <ViewedBox file={file} onToggle={() => toggleViewed(file)} big />
            <Button compact icon={ChevronRight} disabled={position >= files.length - 1} onClick={() => goToFile(files[position + 1]?.path)}>
              Next
            </Button>
          </nav>
        )}
        <Sheet open={listSheet} onClose={() => setListSheet(false)} title={`Files · ${viewedText(summary.viewed, summary.total)}`}>
          {list}
        </Sheet>
      </section>
    );
  }

  return (
    <section ref={sectionRef} aria-label={`Changes: ${summary.label}`} className={cn("flex min-h-0 flex-col overflow-hidden rounded border border-border bg-surface", fullscreen ? "h-full rounded-none border-0" : "h-[78vh] min-h-[28rem]", className)}>
      {title}
      {toolbar}
      <div className="flex min-h-0 flex-1">
        {listShown && <aside aria-label="Changed files" className="flex w-64 shrink-0 flex-col border-r border-border bg-surface-raised xl:w-72">{list}</aside>}
        <div ref={scroller} onScroll={onScroll} className="min-w-0 flex-1 overflow-auto" data-diff-scroller>
          {files.length === 0 ? empty : files.map(section)}
          {files.length > 0 && <div className="h-[85vh]" aria-hidden />}
        </div>
      </div>
    </section>
  );
}

// ---- Toolbar -----------------------------------------------------------------------------------

interface ToolbarProps {
  listShown: boolean;
  onToggleList: () => void;
  summary: DiffSummary;
  scope: ChangeScope;
  setScope: (scope: ChangeScope) => void;
  base: string;
  setBase: (base: string) => void;
  prefs: ViewerPrefs;
  setPrefs: (change: Partial<ViewerPrefs>) => void;
  wide: boolean;
  phone: boolean;
  fullscreen: boolean;
  canFullscreen: boolean;
  onFullscreen: () => void;
  onRefresh: () => void;
  refreshing: boolean;
  onOpenList: () => void;
  onExpandAll: (open: boolean) => void;
  onMarkAll: () => void;
  onClearMarks: () => void;
  who: DiffWho;
}

function Toolbar(props: ToolbarProps) {
  const { summary, scope, setScope, prefs, setPrefs, wide, phone, fullscreen } = props;
  const percent = summary.total === 0 ? 0 : Math.round((summary.viewed / summary.total) * 100);
  const newTab = () => {
    const search = new URLSearchParams({ ...whoSearch(props.who), scope });
    if (props.base) search.set("base", props.base);
    window.open("/diff?" + search.toString(), "_blank", "noopener");
  };
  return (
    <div className="flex flex-col gap-1.5 border-b border-border bg-surface-raised px-2 py-1.5">
      <div className="flex flex-wrap items-center gap-1.5">
        {phone && (
          <Button compact icon={List} onClick={props.onOpenList}>
            Files
          </Button>
        )}
        {!phone && <IconButton icon={props.listShown ? PanelLeftClose : PanelLeftOpen} label={props.listShown ? "Hide the file list" : "Show the file list"} onClick={props.onToggleList} />}
        <div role="radiogroup" aria-label="Which changes" className="flex flex-wrap gap-1">
          {summary.scopes.map((info) => (
            <button
              key={info.name}
              type="button"
              role="radio"
              aria-checked={scope === info.name}
              title={`${info.label}: ${info.files} files, +${info.added} −${info.deleted}`}
              onClick={() => setScope(info.name)}
              className={cn(
                "min-h-11 rounded border px-2 text-xs md:min-h-7",
                scope === info.name ? "border-accent bg-accent/10 font-medium text-heading" : "border-border text-body hover:bg-hover",
              )}
            >
              {phone ? SCOPE_SHORT[info.name] : info.label} <span className="text-muted">{info.files}</span>
            </button>
          ))}
        </div>
        {summary.base_choices && summary.base_choices.length > 1 && (
          <label className="flex items-center gap-1 text-2xs text-muted">
            <span>since</span>
            <select
              value={props.base}
              onChange={(event) => props.setBase(event.target.value)}
              className="min-h-11 max-w-[14rem] rounded border border-border bg-surface px-1 text-xs text-body md:min-h-7"
              aria-label="Compare commits since"
            >
              {summary.base_choices.map((choice) => (
                <option key={choice.ref || "upstream"} value={choice.ref}>
                  {choice.label}
                </option>
              ))}
            </select>
          </label>
        )}
        <div className="ml-auto flex items-center gap-2" aria-live="polite">
          <span className="text-xs text-body">{viewedText(summary.viewed, summary.total)}</span>
          <div className="h-1.5 w-20 overflow-hidden rounded bg-surface-sunken" role="progressbar" aria-label="Files viewed" aria-valuemin={0} aria-valuemax={summary.total} aria-valuenow={summary.viewed}>
            <div className="h-full bg-success" style={{ width: `${percent}%` }} />
          </div>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-0.5">
        <Toggle on={prefs.hideViewed} icon={prefs.hideViewed ? EyeOff : Eye} label="Hide viewed" onClick={() => setPrefs({ hideViewed: !prefs.hideViewed })} />
        {wide && <Toggle on={prefs.split} icon={prefs.split ? Columns2 : Rows3} label={prefs.split ? "Split" : "Unified"} onClick={() => setPrefs({ split: !prefs.split })} pressedLabel="Split view" />}
        <Toggle on={prefs.ignoreWhitespace} icon={Space} label="Hide whitespace" iconOnly={phone} onClick={() => setPrefs({ ignoreWhitespace: !prefs.ignoreWhitespace })} />
        <Toggle on={prefs.wrap} icon={WrapText} label="Wrap" iconOnly={phone} onClick={() => setPrefs({ wrap: !prefs.wrap })} />
        {!phone && (
          <>
            <IconButton icon={ChevronsUpDown} label="Expand all files" onClick={() => props.onExpandAll(true)} />
            <IconButton icon={ChevronsDownUp} label="Collapse all files" onClick={() => props.onExpandAll(false)} />
          </>
        )}
        {phone ? (
          <>
            <IconButton icon={Check} label="Mark all viewed" onClick={props.onMarkAll} disabled={summary.total === 0 || summary.viewed === summary.total} />
            <IconButton icon={X} label="Clear marks" onClick={props.onClearMarks} disabled={summary.viewed === 0} />
          </>
        ) : (
          <>
            <Button compact variant="ghost" icon={Check} onClick={props.onMarkAll} disabled={summary.total === 0 || summary.viewed === summary.total}>
              Mark all viewed
            </Button>
            <Button compact variant="ghost" icon={X} onClick={props.onClearMarks} disabled={summary.viewed === 0}>
              Clear marks
            </Button>
          </>
        )}
        <span className="ml-auto flex items-center">
          <span className="mr-1 hidden text-2xs text-muted md:inline" title={summary.base ? `base ${summary.base.slice(0, 10)}` : undefined}>
            {summary.base_label ? `vs ${summary.base_label}` : ""}
          </span>
          <IconButton icon={RefreshCw} label="Refresh" onClick={props.onRefresh} className={cn(props.refreshing && "[&>svg]:animate-spin")} />
          <HelpButton topic="how-to/review-changes" what="the diff viewer and Viewed marks" inline={false} />
          {!fullscreen && <IconButton icon={ExternalLink} label="Open in a new tab" onClick={newTab} />}
          {props.canFullscreen && <IconButton icon={fullscreen ? Minimize2 : Maximize2} label={fullscreen ? "Leave full screen (Esc)" : "Full screen (f)"} onClick={props.onFullscreen} />}
        </span>
      </div>
      {summary.truncated && <p className="text-2xs text-warning">Only the first 1,500 files are listed.</p>}
    </div>
  );
}

function Toggle({ on, icon, label, onClick, pressedLabel, iconOnly }: { on: boolean; icon: typeof Eye; label: string; onClick: () => void; pressedLabel?: string; iconOnly?: boolean }) {
  return (
    <Button
      compact
      variant="ghost"
      icon={icon}
      aria-pressed={on}
      aria-label={pressedLabel ?? (iconOnly ? label : undefined)}
      title={iconOnly ? label : undefined}
      onClick={onClick}
      className={cn(on && "bg-accent/10 text-heading", iconOnly && "w-11")}
    >
      {!iconOnly && label}
    </Button>
  );
}

// ---- File list ---------------------------------------------------------------------------------

function FileList({ files, all, current, filter, setFilter, onPick, onToggleViewed }: {
  files: DiffFile[];
  all: DiffFile[];
  current?: string;
  filter: string;
  setFilter: (value: string) => void;
  onPick: (path: string) => void;
  onToggleViewed: (file: DiffFile) => void;
}) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <label className="flex items-center gap-1 border-b border-border px-2 py-1">
        <Filter className="h-3.5 w-3.5 shrink-0 text-muted" aria-hidden />
        <span className="sr-only">Filter files</span>
        <input
          type="search"
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
          placeholder={`Filter ${all.length} files`}
          className="min-h-11 w-full bg-transparent text-xs text-body outline-none placeholder:text-muted md:min-h-7"
        />
      </label>
      <ul aria-label="Files" className="min-h-0 flex-1 overflow-y-auto py-0.5">
        {files.map((file) => (
          <li key={file.path} className={cn("flex items-center gap-1 pr-1", file.path === current && "bg-accent/10")}>
            <ViewedBox file={file} onToggle={() => onToggleViewed(file)} />
            <button
              type="button"
              onClick={() => onPick(file.path)}
              aria-current={file.path === current ? "true" : undefined}
              className="flex min-h-11 min-w-0 flex-1 items-center gap-1.5 text-left text-xs md:min-h-7"
              title={file.old_path ? `${file.old_path} → ${file.path}` : file.path}
            >
              <StatusBadge status={file.status} />
              <span className={cn("min-w-0 flex-1 truncate font-mono text-2xs", file.viewed ? "text-muted" : "text-body")} dir="rtl">
                <bdi>
                  <Revealed text={file.path} />
                </bdi>
              </span>
              {hidesSomething(findHidden(file.path, file.old_path)) && (
                <TriangleAlert className="h-3.5 w-3.5 shrink-0 text-danger" aria-label="its name hides characters" role="img" />
              )}
              {file.changed_since_viewed && <FileWarning className="h-3.5 w-3.5 shrink-0 text-warning" aria-label="changed since you viewed it" />}
              <StatBar file={file} />
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function StatusBadge({ status }: { status: DiffFile["status"] }) {
  const tone = status === "added" ? "text-success border-success/50" : status === "deleted" ? "text-danger border-danger/50" : status === "renamed" ? "text-accent border-accent/50" : "text-warning border-warning/50";
  return (
    <span className={cn("inline-flex h-4 w-4 shrink-0 items-center justify-center rounded border font-mono text-[10px] font-semibold", tone)} title={status}>
      <span aria-hidden>{statusLetter[status]}</span>
      <span className="sr-only">{status}</span>
    </span>
  );
}

function StatBar({ file }: { file: DiffFile }) {
  if (file.binary || file.too_large) return <span className="shrink-0 text-2xs text-muted">bin</span>;
  return (
    <span className="flex shrink-0 items-center gap-1" title={`+${file.added} −${file.deleted}`}>
      <span className="font-mono text-2xs">
        <span className="text-success">+{file.added}</span> <span className="text-danger">−{file.deleted}</span>
      </span>
      <span className="hidden gap-px sm:flex" aria-hidden>
        {statCells(file.added, file.deleted).map((cell, index) => (
          <span key={index} className={cn("h-2 w-1.5 rounded-[1px]", cell === "add" ? "bg-success" : cell === "del" ? "bg-danger" : "bg-border")} />
        ))}
      </span>
    </span>
  );
}

function ViewedBox({ file, onToggle, big }: { file: DiffFile; onToggle: () => void; big?: boolean }) {
  return (
    <label className={cn("flex shrink-0 cursor-pointer items-center justify-center gap-1 rounded text-xs", big ? "min-h-11 border border-border px-2" : "h-11 w-9 md:h-7 md:w-7")} title={file.viewed ? "Viewed: uncheck to look again" : "Mark viewed"}>
      <input type="checkbox" checked={file.viewed} onChange={onToggle} className="h-4 w-4 accent-[var(--color-success)]" aria-label={`Viewed: ${file.path}`} />
      {big && <span>Viewed</span>}
    </label>
  );
}

// ---- One file ----------------------------------------------------------------------------------

interface FileSectionProps {
  who: DiffWho;
  base: string;
  scope: ChangeScope;
  file: DiffFile;
  open: boolean;
  onOpen: (open: boolean) => void;
  since: boolean;
  onSince: (on: boolean) => void;
  split: boolean;
  wrap: boolean;
  ignoreWhitespace: boolean;
  onToggleViewed: () => void;
  current: boolean;
  phone: boolean;
}

function FileSection(props: FileSectionProps) {
  const { file, open } = props;
  const toast = useToast();
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(file.path);
      toast.show({ tone: "success", title: "Path copied", description: file.path });
    } catch {
      toast.show({ tone: "warning", title: "Could not copy", description: file.path });
    }
  };
  return (
    <section data-file-section={file.path} aria-label={file.path} className={cn("border-b border-border", props.current && !props.phone && "ring-1 ring-inset ring-accent/40")}>
      <header className="sticky top-0 z-[5] flex min-h-11 flex-wrap items-center gap-1.5 border-b border-border bg-surface-raised px-1.5 py-1 md:min-h-9">
        {!props.phone && (
          <button type="button" aria-expanded={open} aria-label={open ? `Collapse ${file.path}` : `Expand ${file.path}`} onClick={() => props.onOpen(!open)} className="inline-flex h-11 w-8 items-center justify-center rounded text-muted hover:bg-hover md:h-7">
            {open ? <ChevronDown className="h-4 w-4" aria-hidden /> : <ChevronRight className="h-4 w-4" aria-hidden />}
          </button>
        )}
        <StatusBadge status={file.status} />
        <h3 className="min-w-0 flex-1 basis-[55%] break-all font-mono text-xs text-heading">
          {file.old_path && file.old_path !== file.path ? (
            <>
              <span className="text-muted">
                <Revealed text={file.old_path} />
              </span>{" "}
              → <Revealed text={file.path} />
            </>
          ) : (
            <Revealed text={file.path} />
          )}
        </h3>
        {hidesSomething(findHidden(file.path, file.old_path)) && (
          <Chip tone="danger" title={describeHidden(findHidden(file.path, file.old_path))}>
            <TriangleAlert className="mr-1 inline h-3 w-3" aria-hidden />
            name hides characters
          </Chip>
        )}
        {file.churn && <Chip title={`Collapsed by default: ${file.churn_why}`}>{file.churn_why}</Chip>}
        {file.changed_since_viewed && (
          <Chip tone="warning" title={file.viewed_at ? `You viewed it ${new Date(file.viewed_at).toLocaleString()}` : undefined}>
            <FileWarning className="mr-1 inline h-3 w-3" aria-hidden />
            changed since you viewed it
          </Chip>
        )}
        {file.changed_since_viewed && file.since_available && (
          <Button compact variant="ghost" aria-pressed={props.since} onClick={() => props.onSince(!props.since)} className={cn(props.since && "bg-warning/10")}>
            {props.since ? "Since viewed" : "Show since viewed"}
          </Button>
        )}
        <StatBar file={file} />
        <IconButton icon={Copy} label={`Copy path ${file.path}`} onClick={() => void copy()} />
        {!props.phone && <ViewedBox file={file} onToggle={props.onToggleViewed} big />}
      </header>
      {open && <LazyBody {...props} />}
    </section>
  );
}

/** Fetches and renders the file's diff once its section is near the screen. */
function LazyBody(props: FileSectionProps) {
  const { file } = props;
  const holder = useRef<HTMLDivElement>(null);
  const [near, setNear] = useState(props.phone || typeof IntersectionObserver === "undefined");
  const [context, setContext] = useState(0);
  useEffect(() => {
    if (near || !holder.current) return;
    const observer = new IntersectionObserver((entries) => entries.some((entry) => entry.isIntersecting) && setNear(true), { rootMargin: "600px" });
    observer.observe(holder.current);
    return () => observer.disconnect();
  }, [near]);
  const skip = file.too_large || (file.binary && !props.since);
  const query = useDiffFile(props.who, file.path, file.hash, props.scope, { context, ignoreWhitespace: props.ignoreWhitespace, since: props.since, baseline: file.viewed_at }, props.base, near && !skip);
  let body: ReactNode;
  if (file.too_large) body = <p className="px-3 py-2 text-xs text-muted">{file.note ?? "Too large to show."}</p>;
  else if (file.binary) body = <p className="px-3 py-2 text-xs text-muted">Binary file ({file.status}); not shown.</p>;
  else if (!near || query.isPending) body = <SkeletonRows rows={3} label={`Loading ${file.path}`} />;
  else if (query.isError) body = <p className="px-3 py-2 text-xs text-danger">Could not load the diff: {query.error.message}</p>;
  else if (query.data.binary) body = <p className="px-3 py-2 text-xs text-muted">Binary file; not shown.</p>;
  else
    body = (
      <>
        {query.data.since && (
          <p className="border-b border-border bg-warning/10 px-3 py-1 text-2xs text-body">
            What changed since you viewed it{query.data.viewed_at ? ` (${new Date(query.data.viewed_at).toLocaleString()})` : ""}: only the new edits.
          </p>
        )}
        <FileDiff view={query.data} split={props.split} wrap={props.wrap} onMoreContext={setContext} />
      </>
    );
  return <div ref={holder}>{body}</div>;
}

// ---- Helpers -----------------------------------------------------------------------------------

function scrollToFile(container: HTMLElement | null, path: string, offset: number) {
  if (!container) return;
  const section = Array.from(container.querySelectorAll<HTMLElement>("[data-file-section]")).find((element) => element.dataset.fileSection === path);
  if (!section) return;
  container.scrollTop = section.offsetTop - container.offsetTop + offset;
  if (container.scrollTop === 0 && section.offsetTop > container.offsetTop) section.scrollIntoView({ block: "start" });
}

function jumpToChange(container: HTMLElement | null, by: 1 | -1) {
  if (!container) return;
  const top = container.getBoundingClientRect().top + 44; // below the sticky file header
  const starts = Array.from(container.querySelectorAll<HTMLElement>("[data-change-start]"));
  const positions = starts.map((element) => element.getBoundingClientRect().top - top);
  const target = by > 0 ? starts[positions.findIndex((position) => position > 4)] : starts[positions.map((position) => position < -4).lastIndexOf(true)];
  if (!target) return;
  container.scrollTop += target.getBoundingClientRect().top - top - 8;
}

function readPosition(who: DiffWho): Position {
  try {
    return JSON.parse(sessionStorage.getItem(positionKey(who)) ?? "{}") as Position;
  } catch {
    return {};
  }
}

function writePosition(who: DiffWho, position: Position) {
  try {
    sessionStorage.setItem(positionKey(who), JSON.stringify(position));
  } catch {
    // not kept
  }
}

/** A horizontal swipe of 70 px or more moves to the next (left) or previous (right) file. */
function SwipeArea({ className, onSwipe, children }: { className?: string; onSwipe: (by: 1 | -1) => void; children: ReactNode }) {
  const start = useRef<{ x: number; y: number } | null>(null);
  return (
    <div
      className={className}
      onTouchStart={(event) => {
        const touch = event.touches[0];
        start.current = touch ? { x: touch.clientX, y: touch.clientY } : null;
      }}
      onTouchEnd={(event) => {
        const touch = event.changedTouches[0];
        const from = start.current;
        start.current = null;
        if (!from || !touch) return;
        const dx = touch.clientX - from.x;
        const dy = touch.clientY - from.y;
        if (Math.abs(dx) > 70 && Math.abs(dx) > Math.abs(dy) * 2) onSwipe(dx < 0 ? 1 : -1);
      }}
    >
      {children}
    </div>
  );
}
