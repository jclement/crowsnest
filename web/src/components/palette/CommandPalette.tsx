// The command palette (DESIGN.md 12, "Command palette"): one input, fuzzy-ranked results in
// groups, keyboard first. A full-screen sheet on a phone, a top-anchored panel from md.
//
// Levels: the root list; an item's actions (→, Tab, ⌘Enter when it has no alt, or the row's
// chevron); a confirmation (cancel a job, restart the orchestrator); and a line of text (message
// a job's agent). Esc or Backspace on an empty query goes back a level; Esc at the root closes.
//
// Data comes from the app's live queries, so rows change while the palette is open. The
// selection follows the selected item's id (lib/palette.ts keepSelection), so a job arriving
// above it never moves the highlight to a different item.
//
// ARIA: the input is a combobox owning a listbox through aria-activedescendant (focus never
// leaves the input); groups are role=group with their heading as label; a polite status line
// announces the result count. Row chevrons are not focusable, so options hold nothing interactive.
import {
  FolderGit2,
  Newspaper,
  PauseCircle,
  ArrowLeft,
  Bell,
  ChevronRight,
  ClipboardList,
  CornerDownLeft,
  FilePlus2,
  HelpCircle,
  Inbox,
  Keyboard,
  ListTodo,
  LogOut,
  MessageSquare,
  MessageSquarePlus,
  MessageSquareWarning,
  Mic,
  Monitor,
  Moon,
  NotebookText,
  CalendarClock,
  Coins,
  Minimize2,
  RotateCw,
  Search,
  Send,
  Ship,
  SquareTerminal,
  Sun,
  TerminalSquare,
  Bot,
  BellRing,
  Volume2,
  FolderKanban,
  BookText,
  type LucideIcon,
} from "lucide-react";
import { useEffect, useId, useMemo, useState, type KeyboardEvent, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { cn } from "../../lib/cn";
import { useOrchestrator } from "../../lib/conversation-queries";
import { highlightRuns } from "../../lib/fuzzy";
import {
  buildItems,
  completeInput,
  flatten,
  keepSelection,
  rankItems,
  scoreItem,
  type ConfirmSpec,
  type InputSpec,
  type PaletteAction,
  type PaletteCommand,
  type PaletteIcon,
  type PaletteItem,
  type PaletteSources,
  type RankedItem,
} from "../../lib/palette";
import { queryTokens } from "../../lib/fuzzy";
import { useApprovals, useFleet, useJobs, useMe, usePause, useProjects, useQuestions } from "../../lib/queries";
import { readTheme, THEME_EVENT, type ThemeChoice } from "../../lib/theme";
import { Chip, StatusDot } from "../ui/Chip";

const icons: Record<PaletteIcon, LucideIcon> = {
  jobs: ListTodo,
  chat: MessageSquare,
  inbox: Inbox,
  fleet: Ship,
  terminal: SquareTerminal,
  send: Send,
  form: FilePlus2,
  job: ClipboardList,
  question: HelpCircle,
  dialog: MessageSquareWarning,
  agent: Bot,
  pane: TerminalSquare,
  poke: BellRing,
  restart: RotateCw,
  new: MessageSquarePlus,
  bell: Bell,
  journal: NotebookText,
  schedule: CalendarClock,
  spend: Coins,
  compact: Minimize2,
  keyboard: Keyboard,
  "theme-system": Monitor,
  "theme-light": Sun,
  "theme-dark": Moon,
  signout: LogOut,
  speak: Volume2,
  voice: Mic,
  project: FolderKanban,
  instructions: BookText,
  pause: PauseCircle,
  digest: Newspaper,
  worktrees: FolderGit2,
};

const ROOT = { kind: "root" } as const;

type Level =
  | { kind: "root" }
  | { kind: "actions"; item: PaletteItem }
  | { kind: "confirm"; confirm: ConfirmSpec; from: string }
  | { kind: "input"; input: InputSpec; from: string };

export interface CommandPaletteProps {
  inputRef: RefObject<HTMLInputElement | null>;
  onClose: () => void;
  /** Carries out an effect (components/palette/useRunCommand.ts). */
  run: (command: PaletteCommand) => void;
  recents: readonly string[];
  /** Called with the root item that was run, for the recents list. */
  onRan: (itemId: string) => void;
  mac: boolean;
  speak: PaletteSources["speak"];
  voiceSettings: boolean;
}

export function CommandPalette({ inputRef, onClose, run, recents, onRan, mac, speak, voiceSettings }: CommandPaletteProps) {
  const ids = useId();
  const listId = `${ids}-list`;
  const optionId = (index: number) => `${ids}-opt-${index}`;
  const [stack, setStack] = useState<Level[]>([{ kind: "root" }]);
  const [rootQuery, setRootQuery] = useState("");
  const [levelQuery, setLevelQuery] = useState("");
  // The selected item's id, and the row it was last on (where the selection lands if it goes).
  const [selection, setSelection] = useState<{ id: string | null; index: number }>({ id: null, index: 0 });
  const level = stack[stack.length - 1] ?? ROOT;
  const query = level.kind === "root" ? rootQuery : levelQuery;

  const sources = usePaletteSources(speak, voiceSettings);
  const items = useMemo(() => buildItems(sources), [sources]);

  const groups = useMemo(() => (level.kind === "root" ? rankItems(items, rootQuery, recents) : null), [level, items, rootQuery, recents]);
  const rows: RankedItem[] = useMemo(() => {
    if (groups) return flatten(groups);
    if (level.kind === "actions") return actionRows(level.item, levelQuery);
    if (level.kind === "confirm") {
      const { confirm } = level;
      return [
        plainRow({ id: "confirm:yes", group: "actions", title: confirm.confirmLabel, icon: "send", command: confirm.command, verb: confirm.confirmLabel }),
        // Never run: activate() goes back a level for this row.
        plainRow({ id: "confirm:no", group: "actions", title: confirm.cancelLabel, icon: "restart", command: confirm.command, verb: "Back" }),
      ];
    }
    return [];
  }, [groups, level, levelQuery]);

  const index = keepSelection(rows, selection.id, selection.index);
  const selected = index >= 0 ? rows[index] : undefined;

  // Keep the selected row in view, whether the keys or the data moved it.
  useEffect(() => {
    if (index < 0) return;
    document.getElementById(optionId(index))?.scrollIntoView?.({ block: "nearest" });
    // optionId is derived from a stable id.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [index, selected?.item.id]);

  const select = (next: number) => {
    if (rows.length === 0) return;
    const wrapped = (next + rows.length) % rows.length;
    setSelection({ id: rows[wrapped]?.item.id ?? null, index: wrapped });
  };

  const push = (next: Level) => {
    setStack((current) => [...current, next]);
    setLevelQuery("");
    setSelection({ id: null, index: 0 });
  };

  const pop = () => {
    setStack((current) => (current.length > 1 ? current.slice(0, -1) : current));
    setLevelQuery("");
    setSelection({ id: null, index: 0 });
  };

  /** The id recents remember: the root item that led here. */
  const rootItemId = (): string | null => {
    const first = stack[1];
    if (first?.kind === "actions") return first.item.id;
    if (first?.kind === "confirm" || first?.kind === "input") return first.from;
    return null;
  };

  const execute = (command: PaletteCommand, fromItem: string | null) => {
    if (command.kind === "confirm") return push({ kind: "confirm", confirm: command.confirm, from: fromItem ?? rootItemId() ?? "" });
    if (command.kind === "input") return push({ kind: "input", input: command.input, from: fromItem ?? rootItemId() ?? "" });
    const remembered = fromItem ?? rootItemId();
    if (remembered) onRan(remembered);
    onClose();
    run(command);
  };

  const activate = (row: RankedItem | undefined, alt: boolean) => {
    if (!row) return;
    if (level.kind === "confirm") {
      if (row.item.id === "confirm:no") return pop();
      return execute(row.item.command, null);
    }
    const fromItem = level.kind === "root" ? row.item.id : null;
    if (alt) {
      if (row.item.alt) return execute(row.item.alt.command, fromItem);
      if (row.item.actions?.length) return push({ kind: "actions", item: row.item });
    }
    execute(row.item.command, fromItem);
  };

  const openActions = (row: RankedItem | undefined) => {
    if (row?.item.actions?.length && level.kind === "root") push({ kind: "actions", item: row.item });
  };

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    const ctrl = event.ctrlKey && !event.metaKey && !event.altKey;
    const caretAtEnd = event.currentTarget.selectionStart === event.currentTarget.value.length;
    const caretAtStart = event.currentTarget.selectionStart === 0 && event.currentTarget.selectionEnd === 0;
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      if (stack.length > 1) pop();
      else onClose();
      return;
    }
    if (event.key === "ArrowDown" || (ctrl && event.key === "n")) {
      event.preventDefault();
      select(index + 1);
    } else if (event.key === "ArrowUp" || (ctrl && event.key === "p")) {
      event.preventDefault();
      select(index - 1);
    } else if (event.key === "Enter") {
      event.preventDefault();
      if (event.nativeEvent.isComposing) return;
      if (level.kind === "input") {
        if (levelQuery.trim()) execute(completeInput(level.input, levelQuery.trim()), null);
        return;
      }
      activate(selected, event.metaKey || event.ctrlKey);
    } else if (event.key === "Tab") {
      event.preventDefault();
      if (!event.shiftKey) openActions(selected);
      else if (stack.length > 1) pop();
    } else if (event.key === "ArrowRight" && caretAtEnd && level.kind === "root" && selected?.item.actions?.length) {
      event.preventDefault();
      openActions(selected);
    } else if (event.key === "ArrowLeft" && caretAtStart && stack.length > 1) {
      event.preventDefault();
      pop();
    } else if (event.key === "Backspace" && query === "" && stack.length > 1) {
      event.preventDefault();
      pop();
    }
  };

  const onChange = (value: string) => {
    if (level.kind === "root") setRootQuery(value);
    else setLevelQuery(value);
    setSelection({ id: null, index: 0 });
  };

  const placeholder =
    level.kind === "root"
      ? "Search, go to, or type a job…"
      : level.kind === "actions"
        ? "Filter actions…"
        : level.kind === "input"
          ? level.input.placeholder
          : "Confirm or go back";

  const resultText =
    level.kind === "input"
      ? "Type the message and press Enter"
      : rows.length === 0
        ? "No results"
        : `${rows.length} result${rows.length === 1 ? "" : "s"}`;

  return createPortal(
    <div className="fixed inset-0 z-50 flex items-stretch justify-center md:items-start md:p-6 md:pt-[12vh]" data-palette>
      <div aria-hidden className="absolute inset-0 hidden bg-overlay md:block" onClick={onClose} />
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        className={cn(
          "relative flex w-full flex-col bg-surface",
          "pb-[env(safe-area-inset-bottom)] pt-[env(safe-area-inset-top)]",
          "md:max-h-[70vh] md:max-w-xl md:rounded-lg md:border md:border-border md:pb-0 md:pt-0 md:shadow-2xl",
        )}
      >
        <div className="flex min-h-12 shrink-0 items-center gap-2 border-b border-border pl-3 pr-1">
          {stack.length > 1 ? (
            <button
              type="button"
              tabIndex={-1}
              onMouseDown={(event) => event.preventDefault()}
              onClick={pop}
              aria-label="Back"
              className="-ml-2 inline-flex h-11 w-11 shrink-0 items-center justify-center rounded text-muted hover:bg-hover hover:text-heading md:h-8 md:w-8"
            >
              <ArrowLeft className="h-4 w-4" aria-hidden />
            </button>
          ) : (
            <Search className="h-4 w-4 shrink-0 text-muted" aria-hidden />
          )}
          {stack.length > 1 && <Breadcrumb level={level} />}
          <input
            ref={inputRef}
            autoFocus
            value={query}
            onChange={(event) => onChange(event.target.value)}
            onKeyDown={onKeyDown}
            placeholder={placeholder}
            role="combobox"
            aria-label={level.kind === "input" ? level.input.title : "Search or run a command"}
            aria-expanded={level.kind !== "input" && rows.length > 0}
            aria-controls={listId}
            aria-autocomplete="list"
            aria-activedescendant={selected && level.kind !== "input" ? optionId(index) : undefined}
            autoComplete="off"
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
            enterKeyHint={level.kind === "input" ? "send" : "go"}
            className="min-h-11 min-w-0 flex-1 bg-transparent text-base text-heading outline-none placeholder:text-muted md:text-sm"
          />
          <button
            type="button"
            tabIndex={-1}
            onMouseDown={(event) => event.preventDefault()}
            onClick={onClose}
            className="inline-flex min-h-11 shrink-0 items-center rounded px-2 text-sm text-accent hover:bg-hover md:hidden"
          >
            Cancel
          </button>
          <kbd className="mr-2 hidden shrink-0 rounded border border-border px-1.5 font-mono text-2xs text-muted md:inline">esc</kbd>
        </div>

        {level.kind === "actions" && (
          <div className="border-b border-border px-3 py-2">
            <p className="text-sm font-semibold text-heading">{level.item.title}</p>
            {level.item.subtitle && <p className="mt-0.5 truncate text-xs text-muted">{level.item.subtitle}</p>}
          </div>
        )}
        {level.kind === "confirm" && (
          <div className="border-b border-border px-3 py-2">
            <p className="text-sm font-semibold text-heading">{level.confirm.title}</p>
            <p className="mt-0.5 text-xs text-body">{level.confirm.body}</p>
          </div>
        )}
        {level.kind === "input" && (
          <div className="px-3 py-3 text-xs text-muted">
            <span className="font-medium text-heading">{level.input.title}.</span> Enter sends it; Esc goes back.
          </div>
        )}

        <div
          id={listId}
          role="listbox"
          aria-label={level.kind === "actions" ? `Actions for ${shortTitle(level.item)}` : "Results"}
          className={cn("min-h-0 flex-1 overflow-y-auto overscroll-contain py-1", level.kind === "input" && "hidden")}
        >
          {rows.length === 0 && level.kind !== "input" && <p className="px-3 py-6 text-center text-sm text-muted">Nothing matches. Type a sentence to give it as a job.</p>}
          {groups
            ? groups.map((group) => {
                const headingId = `${ids}-group-${group.id}`;
                return (
                  <div key={group.id} role="group" aria-labelledby={headingId}>
                    <div id={headingId} className="px-3 pb-0.5 pt-2 text-2xs font-semibold uppercase tracking-wide text-muted">
                      {group.label}
                    </div>
                    {group.items.map((row) => {
                      const rowIndex = rows.indexOf(row);
                      return (
                        <Row
                          key={row.item.id}
                          id={optionId(rowIndex)}
                          row={row}
                          selected={rowIndex === index}
                          onHover={() => rowIndex !== index && select(rowIndex)}
                          onRun={() => activate(row, false)}
                          onActions={row.item.actions?.length ? () => push({ kind: "actions", item: row.item }) : undefined}
                        />
                      );
                    })}
                  </div>
                );
              })
            : rows.map((row, rowIndex) => (
                <Row
                  key={row.item.id}
                  id={optionId(rowIndex)}
                  row={row}
                  selected={rowIndex === index}
                  danger={row.item.id === "confirm:yes" || isDanger(level, row)}
                  onHover={() => rowIndex !== index && select(rowIndex)}
                  onRun={() => activate(row, false)}
                />
              ))}
        </div>

        <p role="status" aria-live="polite" className="sr-only">
          {resultText}
        </p>
        <Footer level={level} selected={selected} mac={mac} />
      </div>
    </div>,
    document.body,
  );
}

/** The live sources, read from the queries the app already keeps fresh. */
function usePaletteSources(speak: PaletteSources["speak"], voiceSettings: boolean): PaletteSources {
  const jobs = useJobs();
  const questions = useQuestions("open");
  const approvals = useApprovals("pending");
  const fleet = useFleet();
  const me = useMe();
  const orchestrator = useOrchestrator();
  const projects = useProjects();
  const pause = usePause();
  const [theme, setTheme] = useState<ThemeChoice>(readTheme);
  useEffect(() => {
    const follow = (event: Event) => setTheme((event as CustomEvent<ThemeChoice>).detail);
    window.addEventListener(THEME_EVENT, follow);
    return () => window.removeEventListener(THEME_EVENT, follow);
  }, []);
  return useMemo(
    () => ({
      jobs: jobs.data,
      questions: questions.data,
      approvals: approvals.data,
      fleet: fleet.data,
      orchestrator: !!orchestrator.data && orchestrator.data.state !== "disabled",
      auth: !!me.data?.features.auth,
      theme,
      speak,
      voiceSettings,
      projects: projects.data?.projects,
      paused: pause.data?.paused ?? false,
    }),
    [jobs.data, questions.data, approvals.data, fleet.data, orchestrator.data, me.data, theme, speak, voiceSettings, projects.data, pause.data],
  );
}

function plainRow(item: PaletteItem): RankedItem {
  return { item, score: 0, titlePositions: [], subtitlePositions: [] };
}

/** An item's actions as rows, filtered by the query. */
function actionRows(parent: PaletteItem, query: string): RankedItem[] {
  const tokens = queryTokens(query);
  const rows: RankedItem[] = [];
  for (const action of parent.actions ?? []) {
    const item = actionItem(parent, action);
    if (tokens.length === 0) {
      rows.push(plainRow(item));
      continue;
    }
    const scored = scoreItem(tokens, item);
    if (scored) rows.push({ item, ...scored });
  }
  return tokens.length === 0 ? rows : rows.sort((a, b) => b.score - a.score);
}

function actionItem(parent: PaletteItem, action: PaletteAction): PaletteItem {
  return {
    id: `${parent.id}>${action.id}`,
    group: "actions",
    title: action.label,
    icon: parent.icon,
    command: action.command,
    verb: "Run",
    keywords: action.danger ? ["danger"] : undefined,
  };
}

function isDanger(level: Level, row: RankedItem): boolean {
  if (level.kind !== "actions") return false;
  const actionId = row.item.id.slice(level.item.id.length + 1);
  return !!level.item.actions?.find((action) => action.id === actionId)?.danger;
}

function shortTitle(item: PaletteItem): string {
  const id = item.id.replace(/^(job|q|agent|pane|dialog):/, "");
  return item.id.startsWith("job:") || item.id.startsWith("q:") ? id : item.title.length > 40 ? `${item.title.slice(0, 39)}…` : item.title;
}

function Breadcrumb({ level }: { level: Level }) {
  const text = level.kind === "actions" ? shortTitle(level.item) : level.kind === "confirm" ? "Confirm" : level.kind === "input" ? level.input.title : "";
  return (
    <span className="inline-flex max-w-[40%] shrink-0 items-center gap-0.5 truncate rounded bg-surface-sunken px-1.5 py-0.5 font-mono text-2xs text-muted">
      <span className="truncate">{text}</span>
      <ChevronRight className="h-3 w-3 shrink-0" aria-hidden />
    </span>
  );
}

interface RowProps {
  id: string;
  row: RankedItem;
  selected: boolean;
  danger?: boolean;
  onHover: () => void;
  onRun: () => void;
  onActions?: () => void;
}

function Row({ id, row, selected, danger, onHover, onRun, onActions }: RowProps) {
  const { item } = row;
  const Icon = icons[item.icon];
  return (
    <div
      id={id}
      role="option"
      aria-selected={selected}
      onMouseMove={onHover}
      onMouseDown={(event) => event.preventDefault()}
      onClick={onRun}
      className={cn(
        "mx-1 flex min-h-11 cursor-pointer items-center gap-2.5 rounded px-2 py-1 pointer-fine:min-h-9",
        selected ? "bg-selected" : "hover:bg-hover",
      )}
    >
      {item.status ? (
        <span className="flex h-4 w-4 shrink-0 items-center justify-center">
          <StatusDot status={item.status} />
        </span>
      ) : (
        <Icon className={cn("h-4 w-4 shrink-0", danger ? "text-danger" : "text-muted")} aria-hidden />
      )}
      <span className="flex min-w-0 flex-1 flex-col">
        <span data-part="title" className={cn("truncate text-sm", danger ? "text-danger" : "text-heading")}>
          <Highlighted text={item.title} positions={row.titlePositions} />
        </span>
        {item.subtitle && (
          <span className="truncate text-2xs text-muted">
            <Highlighted text={item.subtitle} positions={row.subtitlePositions} />
          </span>
        )}
      </span>
      {item.chip && (
        <Chip tone={item.chip.tone} className="shrink-0">
          {item.chip.label}
        </Chip>
      )}
      {selected && (
        <span aria-hidden className="hidden shrink-0 items-center gap-1 text-2xs text-muted pointer-fine:inline-flex">
          {item.verb}
          <CornerDownLeft className="h-3 w-3" aria-hidden />
        </span>
      )}
      {onActions && (
        <span
          aria-hidden
          title="More actions"
          onClick={(event) => {
            event.stopPropagation();
            onActions();
          }}
          className="-mr-2 inline-flex h-11 w-11 shrink-0 items-center justify-center rounded text-muted hover:bg-hover hover:text-heading pointer-fine:h-7 pointer-fine:w-7"
        >
          <ChevronRight className="h-4 w-4" />
        </span>
      )}
    </div>
  );
}

function Highlighted({ text, positions }: { text: string; positions: number[] }) {
  if (positions.length === 0) return <>{text}</>;
  return (
    <>
      {highlightRuns(text, positions).map((run, index) =>
        run.match ? (
          <mark key={index} className="bg-transparent font-semibold text-accent">
            {run.text}
          </mark>
        ) : (
          <span key={index}>{run.text}</span>
        ),
      )}
    </>
  );
}

function Footer({ level, selected, mac }: { level: Level; selected: RankedItem | undefined; mac: boolean }) {
  const mod = mac ? "⌘" : "Ctrl";
  const hints: ReactNode[] = [];
  if (level.kind === "input") {
    hints.push(<Hint key="send" keys="↵" label="Send" />);
  } else {
    hints.push(<Hint key="move" keys="↑↓" label="Move" />);
    if (selected) hints.push(<Hint key="run" keys="↵" label={selected.item.verb} />);
    if (selected?.item.alt) hints.push(<Hint key="alt" keys={`${mod}↵`} label={selected.item.alt.label.replace(/…$/, "")} />);
    if (level.kind === "root" && selected?.item.actions?.length) hints.push(<Hint key="actions" keys="→" label="Actions" />);
  }
  hints.push(<Hint key="esc" keys="esc" label={level.kind === "root" ? "Close" : "Back"} />);
  return <div className="hidden shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-t border-border px-3 py-1.5 text-2xs text-muted md:flex">{hints}</div>;
}

function Hint({ keys, label }: { keys: string; label: string }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1">
      <kbd className="rounded border border-border px-1 font-mono">{keys}</kbd>
      <span className="truncate">{label}</span>
    </span>
  );
}
