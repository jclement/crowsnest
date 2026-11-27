// One file's diff body (DESIGN.md 6.21): unified or split rows, syntax tokens from the whole old
// and new text (so both sides highlight correctly), word-level emphasis on modified lines, the
// gaps between hunks expandable from the file's text (20 lines up or down, or all of it), and the
// rows rendered in chunks as they scroll near, so a 5,000-line file does not block the page.
// Every line keeps its +/− sign and a spoken name; colour is never the only cue. Text only:
// tokens are rendered as text nodes, never HTML. Invisible and direction-changing characters are
// drawn as chips, and the file says how many it has (R7-S3).
//
// With a comment layer (DiffViewer passes one), the line numbers are buttons: click one to select
// the line, shift-click or drag for a range (on a phone, tap one, then tap another to extend), each
// hunk has "Ask for change here", lines with a note get a marker in the gutter, and the notes and
// the open editor are drawn after the last line they cover (comments.ts says where).
import { ArrowDown, ArrowUp, ChevronsUpDown, MessageSquarePlus } from "lucide-react";
import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { cn } from "../../lib/cn";
import {
  applyEmphasis,
  EXPAND_STEP,
  gapsOf,
  languageFor,
  parseHunks,
  splitLines,
  splitRows,
  unifiedRows,
  type DiffLine,
  type Expansion,
  type FileView,
  type Gap,
  type Range,
  type Token,
} from "../../lib/changes";
import { hunkRange, inSelection, sideOf, type DiffComment, type Selection } from "../../lib/comments";
import { findHidden } from "../../lib/hidden";
import { highlightLines, highlightOne } from "../../lib/syntax";
import { HiddenWarning, Revealed } from "../ui/Revealed";

const CHUNK = 300;

export interface FileDiffProps {
  view: FileView;
  split: boolean;
  wrap: boolean;
  /** Asks the server for more context when the file's text is not available to expand from. */
  onMoreContext: (lines: number) => void;
  /** Line comments: selection, markers, notes and the editor (none: plain numbers). */
  comments?: CommentLayer;
}

/** What FileDiff needs to draw and take line comments on one file. */
export interface CommentLayer {
  selection: Selection | null;
  /** Lines of each side that carry a note. */
  marked: { old: Set<number>; new: Set<number> };
  /** Notes drawn in the rows, by "side:last line". */
  inline: Map<string, DiffComment[]>;
  /** The open editor goes after this line. */
  editorAt: { side: "old" | "new"; line: number } | null;
  renderEditor: () => ReactNode;
  renderComments: (comments: DiffComment[]) => ReactNode;
  /** A click or tap on a line number; extend asks to move the selection's end there. */
  onLine: (side: "old" | "new", line: number, extend: boolean) => void;
  /** "Ask for change here" on a hunk: select its lines and open the editor. */
  onHunk: (range: { side: "old" | "new"; start: number; end: number }) => void;
  /** On a phone a tap on another line extends an open selection. */
  phone: boolean;
}

export function FileDiff({ view, split, wrap, onMoreContext, comments }: FileDiffProps) {
  const [expansions, setExpansions] = useState<Record<number, Expansion>>({});
  const [shown, setShown] = useState(CHUNK);
  const language = languageFor(view.path);
  const hunks = useMemo(() => parseHunks(view.diff), [view.diff]);
  const newLines = useMemo(() => (view.new_text === null ? null : splitLines(view.new_text)), [view.new_text]);
  const gaps = useMemo(() => gapsOf(hunks, newLines ? newLines.length : null), [hunks, newLines]);
  const oldTokens = useMemo(() => (view.old_text === null ? null : highlightLines(view.old_text, language)), [view.old_text, language]);
  const newTokens = useMemo(() => (view.new_text === null ? null : highlightLines(view.new_text, language)), [view.new_text, language]);
  const rows = useMemo(
    () => (split ? splitRows(hunks, gaps, expansions, newLines) : unifiedRows(hunks, gaps, expansions, newLines)),
    [split, hunks, gaps, expansions, newLines],
  );
  // The whole diff text is scanned, not only the rows rendered so far: a hidden character far down
  // must be announced before the reviewer ticks Viewed.
  const hidden = useMemo(() => findHidden(view.path, view.diff), [view.path, view.diff]);
  // The rows reuse the hunks' line objects, so a hunk's first line finds its "Ask for change here".
  const hunkStarts = useMemo(() => new Map(hunks.map((hunk, index) => [hunk.lines[0], index])), [hunks]);
  const dragging = useRef<"old" | "new" | null>(null);
  useEffect(() => {
    const stop = () => (dragging.current = null);
    window.addEventListener("pointerup", stop);
    return () => window.removeEventListener("pointerup", stop);
  }, []);
  const sentinel = useRef<HTMLTableRowElement>(null);
  useEffect(() => {
    const element = sentinel.current;
    if (!element || shown >= rows.length || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) setShown((count) => count + CHUNK);
    }, { rootMargin: "800px" });
    observer.observe(element);
    return () => observer.disconnect();
  }, [shown, rows.length]);

  const tokensOf = (line: DiffLine, side: "old" | "new"): Token[] => {
    if (side === "old" && line.old > 0 && oldTokens) return oldTokens[line.old - 1] ?? [];
    if (line.new > 0 && newTokens) return newTokens[line.new - 1] ?? [];
    if (line.old > 0 && oldTokens) return oldTokens[line.old - 1] ?? [];
    return highlightOne(line.text, language);
  };
  const expand = (gap: Gap, how: "up" | "down" | "all") =>
    setExpansions((current) => {
      const now = current[gap.index] ?? { top: 0, bottom: 0 };
      const size = gap.newEnd - gap.newStart + 1;
      // "up" reveals lines just above the hunk below (the gap's bottom); "down", just below the one above.
      const next = how === "all" ? { top: size, bottom: 0 } : how === "up" ? { ...now, bottom: now.bottom + EXPAND_STEP } : { ...now, top: now.top + EXPAND_STEP };
      return { ...current, [gap.index]: next };
    });

  if (hunks.length === 0) {
    return <p className="px-3 py-2 text-xs text-muted">{view.since ? "No change in the text since you viewed it." : "No text changes (a mode change or an empty file)."}</p>;
  }
  const visible = rows.slice(0, shown);
  const columns = split ? 6 : 4;
  const number = (n: number, side: "old" | "new", className?: string) =>
    comments ? (
      <NumberButton
        key={side}
        n={n}
        side={side}
        selected={inSelection(comments.selection, view.path, side, n)}
        marked={n > 0 && comments.marked[side].has(n)}
        className={className}
        onPress={(extend) => comments.onLine(side, n, extend || (comments.phone && comments.selection?.path === view.path && comments.selection.side === side))}
        onDragStart={() => (dragging.current = side)}
        onDragOver={() => dragging.current === side && comments.onLine(side, n, true)}
      />
    ) : (
      <LineNumber key={side} n={n} className={className} />
    );
  // Rows after a line: its notes, then the editor if it is open there.
  const after = (keys: [("old" | "new"), number][], index: number): ReactNode[] => {
    if (!comments) return [];
    const extra: ReactNode[] = [];
    for (const [side, line] of keys) {
      if (line <= 0) continue;
      const notes = comments.inline.get(`${side}:${line}`);
      if (notes) {
        extra.push(
          <tr key={`notes-${index}-${side}`} data-comment-row="">
            <td colSpan={columns} className="bg-surface px-2 py-1.5 font-sans">
              {comments.renderComments(notes)}
            </td>
          </tr>,
        );
      }
      if (comments.editorAt && comments.editorAt.side === side && comments.editorAt.line === line) {
        extra.push(
          <tr key={`editor-${index}-${side}`} data-comment-row="">
            <td colSpan={columns} className="bg-surface px-2 py-1.5 font-sans">
              {comments.renderEditor()}
            </td>
          </tr>,
        );
      }
    }
    return extra;
  };
  const hunkBar = (first: DiffLine | null | undefined, index: number): ReactNode => {
    if (!comments || !first) return null;
    const at = hunkStarts.get(first);
    if (at === undefined) return null;
    const hunk = hunks[at];
    if (!hunk) return null;
    const range = hunkRange(hunk);
    return (
      <tr key={`hunk-${at}-${index}`} className="bg-accent/5 text-muted">
        <td colSpan={columns} className="px-1 py-0">
          <div className="flex flex-wrap items-center gap-2 font-sans text-2xs">
            <button
              type="button"
              aria-label={`Ask for change here: the change at ${range.side === "old" ? "old " : ""}line ${range.start}`}
              onClick={() => comments.onHunk(range)}
              className="inline-flex min-h-11 items-center gap-1 rounded px-1.5 hover:bg-hover hover:text-heading md:min-h-6"
            >
              <MessageSquarePlus className="h-3.5 w-3.5" aria-hidden />
              Ask for change here
            </button>
            <span className="font-mono">
              @@ −{hunk.oldStart},{hunk.oldLines} +{hunk.newStart},{hunk.newLines} @@
            </span>
          </div>
        </td>
      </tr>
    );
  };
  const codeClass = cn("pr-3 align-top", wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre");
  const gapRow = (gap: Gap, hidden: number, header: string | undefined, key: string, columns: number) => (
    <tr key={key} className="bg-accent/5 text-muted">
      <td colSpan={columns} className="px-2 py-0.5">
        <div className="flex flex-wrap items-center gap-1 font-sans text-2xs">
          {newLines ? (
            <>
              {gap.index > 0 && (
                <GapButton icon={ArrowDown} label={`Show ${Math.min(EXPAND_STEP, hidden)} more lines below the change above`} onClick={() => expand(gap, "down")} />
              )}
              {gap.index < hunks.length && (
                <GapButton icon={ArrowUp} label={`Show ${Math.min(EXPAND_STEP, hidden)} more lines above the change below`} onClick={() => expand(gap, "up")} />
              )}
              <GapButton icon={ChevronsUpDown} label={`Show all ${hidden} hidden lines`} onClick={() => expand(gap, "all")} />
            </>
          ) : (
            <>
              <button type="button" className="min-h-11 rounded px-1.5 py-0.5 hover:bg-hover hover:text-heading md:min-h-0" onClick={() => onMoreContext(view.context + EXPAND_STEP)}>
                More context
              </button>
              <button type="button" className="min-h-11 rounded px-1.5 py-0.5 hover:bg-hover hover:text-heading md:min-h-0" onClick={() => onMoreContext(100000)}>
                Whole file
              </button>
            </>
          )}
          <span>
            {hidden} unchanged line{hidden === 1 ? "" : "s"}
            {header && <span className="ml-2 font-mono">{header}</span>}
          </span>
        </div>
      </td>
    </tr>
  );
  return (
    <div className={cn("overflow-x-auto bg-surface", !wrap && "max-w-full")}>
      <HiddenWarning report={hidden} what="This file's diff" className="m-1" />
      <table aria-label={`Diff of ${view.path}`} className={cn("w-full border-collapse font-mono text-2xs leading-5", split && "table-fixed")}>
        {split && (
          <colgroup>
            <col className="w-10" />
            <col className="w-4" />
            <col />
            <col className="w-10" />
            <col className="w-4" />
            <col />
          </colgroup>
        )}
        <tbody>
          {visible.map((row, index) => {
            if (row.type === "gap") return gapRow(row.gap, row.hidden, row.header, `gap-${row.gap.index}-${index}`, columns);
            const previous = visible[index - 1];
            const startsChange = row.change !== null && !(previous && previous.type !== "gap" && previous.change === row.change);
            if (row.type === "line") {
              const line = row.line;
              const side = line.kind === "del" ? "old" : "new";
              const picked = !!comments && (inSelection(comments.selection, view.path, "old", line.old) || inSelection(comments.selection, view.path, "new", line.new));
              return (
                <Fragment key={index}>
                  {hunkBar(line, index)}
                  <tr data-change={row.change ?? undefined} data-change-start={startsChange ? "" : undefined} className={rowTone(line.kind)}>
                    {line.old > 0 ? number(line.old, sideOf(line, "old")) : <LineNumber n={0} />}
                    {line.new > 0 ? number(line.new, sideOf(line, "new")) : <LineNumber n={0} />}
                    <Sign kind={line.kind} />
                    <td className={cn(codeClass, picked && selectedCode)}>
                      <Code tokens={tokensOf(line, side)} emphasis={row.emphasis} kind={line.kind} />
                      {line.noNewline && <NoNewline />}
                    </td>
                  </tr>
                  {after(
                    [
                      ["old", line.kind === "add" ? 0 : line.old],
                      ["new", line.kind === "del" ? 0 : line.new],
                    ],
                    index,
                  )}
                </Fragment>
              );
            }
            const { left, right } = row;
            const same = left && right && left === right;
            const leftTone = left ? rowTone(same ? "context" : left.kind) : "bg-surface-sunken";
            const rightTone = right ? rowTone(same ? "context" : right.kind) : "bg-surface-sunken";
            const leftPicked = !!comments && !!left && inSelection(comments.selection, view.path, "old", left.old);
            const rightPicked = !!comments && !!right && inSelection(comments.selection, view.path, "new", right.new);
            return (
              <Fragment key={index}>
                {hunkBar(left ?? right, index) ?? (left !== right ? hunkBar(right, index) : null)}
                <tr data-change={row.change ?? undefined} data-change-start={startsChange ? "" : undefined}>
                  {left && left.old > 0 ? number(left.old, "old", leftTone) : <LineNumber n={0} className={leftTone} />}
                  <Sign kind={left ? (same ? "context" : left.kind) : null} className={leftTone} />
                  <td className={cn(codeClass, "border-r border-border", leftTone, leftPicked && selectedCode)}>
                    {left && <Code tokens={tokensOf(left, "old")} emphasis={row.leftEmphasis} kind={same ? "context" : left.kind} />}
                  </td>
                  {right && right.new > 0 ? number(right.new, "new", rightTone) : <LineNumber n={0} className={rightTone} />}
                  <Sign kind={right ? (same ? "context" : right.kind) : null} className={rightTone} />
                  <td className={cn(codeClass, rightTone, rightPicked && selectedCode)}>
                    {right && <Code tokens={tokensOf(right, "new")} emphasis={row.rightEmphasis} kind={same ? "context" : right.kind} />}
                    {right?.noNewline && <NoNewline />}
                  </td>
                </tr>
                {after(
                  [
                    ["old", left && left.kind !== "add" ? left.old : 0],
                    ["new", right && right.kind !== "del" ? right.new : 0],
                  ],
                  index,
                )}
              </Fragment>
            );
          })}
          {shown < rows.length && (
            <tr ref={sentinel}>
              <td colSpan={split ? 6 : 4} className="px-3 py-1 font-sans text-2xs text-muted">
                <button type="button" className="underline" onClick={() => setShown((count) => count + CHUNK * 4)}>
                  {rows.length - shown} more lines…
                </button>
              </td>
            </tr>
          )}
        </tbody>
      </table>
      {view.cut && <p className="px-3 py-1 font-sans text-2xs text-warning">The diff was cut at 512 KB.</p>}
    </div>
  );
}

function rowTone(kind: DiffLine["kind"]): string {
  return kind === "add" ? "bg-diff-add" : kind === "del" ? "bg-diff-del" : "";
}

function LineNumber({ n, className }: { n: number; className?: string }) {
  return <td className={cn("w-10 select-none px-1 text-right align-top text-muted", className)}>{n > 0 ? n : ""}</td>;
}

/** The selected lines' code: a bar down the left edge, so the selection reads without colour too. */
const selectedCode = "shadow-[inset_3px_0_0_var(--color-accent)] bg-accent/10";

/**
 * A line number that selects its line: a mouse press selects (shift extends) and a drag over
 * other numbers of the same side extends; a tap or a key press goes through the click.
 */
function NumberButton({ n, side, selected, marked, className, onPress, onDragStart, onDragOver }: {
  n: number;
  side: "old" | "new";
  selected: boolean;
  marked: boolean;
  className?: string;
  onPress: (extend: boolean) => void;
  onDragStart: () => void;
  onDragOver: () => void;
}) {
  const byMouse = useRef(false);
  return (
    <td className={cn("w-10 select-none p-0 text-right align-top", selected ? "bg-accent/25" : className)}>
      <button
        type="button"
        data-line={n}
        data-side={side}
        aria-pressed={selected}
        aria-label={`Line ${n}, ${side} side${marked ? ", has a comment" : ""}`}
        title="Select to comment (shift-click or drag for a range; c to comment)"
        onPointerDown={(event) => {
          byMouse.current = event.pointerType === "mouse";
          if (!byMouse.current || event.button !== 0) return;
          event.preventDefault(); // no text selection while dragging across numbers
          onPress(event.shiftKey);
          onDragStart();
        }}
        onPointerEnter={(event) => {
          if (event.pointerType === "mouse" && event.buttons === 1) onDragOver();
        }}
        onClick={(event) => {
          if (byMouse.current) {
            byMouse.current = false;
            return; // the press already selected
          }
          onPress(event.shiftKey);
        }}
        className={cn(
          "inline-flex w-full items-center justify-end gap-0.5 px-1 text-muted hover:bg-accent/15 hover:text-heading focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent",
          selected && "font-semibold text-heading",
        )}
      >
        {marked && <span aria-hidden className="inline-block h-1.5 w-1.5 shrink-0 rounded-full bg-accent" />}
        {n}
      </button>
    </td>
  );
}

const signWords = { add: "added", del: "removed", context: "unchanged" } as const;

function Sign({ kind, className }: { kind: DiffLine["kind"] | null; className?: string }) {
  return (
    <td className={cn("w-4 select-none text-center align-top", kind === "add" && "text-success", kind === "del" && "text-danger", className)}>
      {kind && kind !== "context" && <span aria-hidden>{kind === "add" ? "+" : "−"}</span>}
      {kind && <span className="sr-only">{signWords[kind]}</span>}
    </td>
  );
}

function Code({ tokens, emphasis, kind }: { tokens: Token[]; emphasis?: Range[]; kind: DiffLine["kind"] }) {
  const pieces = applyEmphasis(tokens, emphasis);
  if (pieces.length === 0) return <span className="select-none"> </span>;
  return (
    <>
      {pieces.map((piece, index) => {
        const text = piece.className ? (
          <span className={piece.className}>
            <Revealed text={piece.text} />
          </span>
        ) : (
          <Revealed text={piece.text} />
        );
        return piece.mark ? (
          <mark key={index} className={cn("rounded-sm text-inherit", kind === "add" ? "bg-success/30" : "bg-danger/30")}>
            {text}
          </mark>
        ) : (
          <Fragment key={index}>{text}</Fragment>
        );
      })}
    </>
  );
}

function NoNewline() {
  return (
    <span className="ml-2 select-none font-sans text-2xs text-muted" title="No newline at end of file">
      ⏎̸ no newline at end
    </span>
  );
}

function GapButton({ icon: Icon, label, onClick }: { icon: typeof ArrowUp; label: string; onClick: () => void }) {
  return (
    <button type="button" aria-label={label} title={label} onClick={onClick} className="inline-flex h-11 w-11 items-center justify-center rounded hover:bg-hover hover:text-heading md:h-6 md:w-7">
      <Icon className="h-3.5 w-3.5" aria-hidden />
    </button>
  );
}
