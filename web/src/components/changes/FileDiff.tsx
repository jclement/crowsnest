// One file's diff body (DESIGN.md 6.20): unified or split rows, syntax tokens from the whole old
// and new text (so both sides highlight correctly), word-level emphasis on modified lines, the
// gaps between hunks expandable from the file's text (20 lines up or down, or all of it), and the
// rows rendered in chunks as they scroll near, so a 5,000-line file does not block the page.
// Every line keeps its +/− sign and a spoken name; colour is never the only cue. Text only:
// tokens are rendered as text nodes, never HTML.
import { ArrowDown, ArrowUp, ChevronsUpDown } from "lucide-react";
import { Fragment, useEffect, useMemo, useRef, useState } from "react";
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
import { highlightLines, highlightOne } from "../../lib/syntax";

const CHUNK = 300;

export interface FileDiffProps {
  view: FileView;
  split: boolean;
  wrap: boolean;
  /** Asks the server for more context when the file's text is not available to expand from. */
  onMoreContext: (lines: number) => void;
}

export function FileDiff({ view, split, wrap, onMoreContext }: FileDiffProps) {
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
            if (row.type === "gap") return gapRow(row.gap, row.hidden, row.header, `gap-${row.gap.index}-${index}`, split ? 6 : 4);
            const previous = visible[index - 1];
            const startsChange = row.change !== null && !(previous && previous.type !== "gap" && previous.change === row.change);
            if (row.type === "line") {
              const line = row.line;
              const side = line.kind === "del" ? "old" : "new";
              return (
                <tr key={index} data-change={row.change ?? undefined} data-change-start={startsChange ? "" : undefined} className={rowTone(line.kind)}>
                  <LineNumber n={line.old} />
                  <LineNumber n={line.new} />
                  <Sign kind={line.kind} />
                  <td className={codeClass}>
                    <Code tokens={tokensOf(line, side)} emphasis={row.emphasis} kind={line.kind} />
                    {line.noNewline && <NoNewline />}
                  </td>
                </tr>
              );
            }
            const { left, right } = row;
            const same = left && right && left === right;
            return (
              <tr key={index} data-change={row.change ?? undefined} data-change-start={startsChange ? "" : undefined}>
                <LineNumber n={left?.old ?? 0} className={left ? rowTone(same ? "context" : left.kind) : "bg-surface-sunken"} />
                <Sign kind={left ? (same ? "context" : left.kind) : null} className={left ? rowTone(same ? "context" : left.kind) : "bg-surface-sunken"} />
                <td className={cn(codeClass, "border-r border-border", left ? rowTone(same ? "context" : left.kind) : "bg-surface-sunken")}>
                  {left && <Code tokens={tokensOf(left, "old")} emphasis={row.leftEmphasis} kind={same ? "context" : left.kind} />}
                </td>
                <LineNumber n={right?.new ?? 0} className={right ? rowTone(same ? "context" : right.kind) : "bg-surface-sunken"} />
                <Sign kind={right ? (same ? "context" : right.kind) : null} className={right ? rowTone(same ? "context" : right.kind) : "bg-surface-sunken"} />
                <td className={cn(codeClass, right ? rowTone(same ? "context" : right.kind) : "bg-surface-sunken")}>
                  {right && <Code tokens={tokensOf(right, "new")} emphasis={row.rightEmphasis} kind={same ? "context" : right.kind} />}
                  {right?.noNewline && <NoNewline />}
                </td>
              </tr>
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
  return kind === "add" ? "bg-success/10" : kind === "del" ? "bg-danger/10" : "";
}

function LineNumber({ n, className }: { n: number; className?: string }) {
  return <td className={cn("w-10 select-none px-1 text-right align-top text-muted", className)}>{n > 0 ? n : ""}</td>;
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
        const text = piece.className ? <span className={piece.className}>{piece.text}</span> : piece.text;
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
