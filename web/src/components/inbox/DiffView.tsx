// DiffView: a question's `files` as diffs (DESIGN.md 8.1). Unified on a phone, side by side from
// lg; long unchanged runs fold into a "N unchanged lines" row that expands in place. Text is
// rendered as text nodes only, so file contents can never become markup.
//
// Unified (the phone's) has one narrow line-number gutter (the new line's number, or the old one
// for a deletion) and does not wrap: code scrolls sideways inside its file, so a line is never
// broken mid-word at 38 characters (R4 Fable 3.6). "Wrap" in the file's header turns wrapping on.
import { FileDiff, WrapText } from "lucide-react";
import { useMemo, useState } from "react";
import { cn } from "../../lib/cn";
import { collapse, diffStats, lineDiff, splitRows, type DiffGap, type DiffItem, type DiffLine, type SplitRow } from "../../lib/diff";
import { LARGE_QUERY, useMediaQuery } from "../../lib/media";
import type { QuestionFile } from "../../lib/questions";

interface DiffViewProps {
  files: QuestionFile[];
  /** Force a layout (tests, screenshots); by default unified below lg and split from lg. */
  layout?: "unified" | "split";
}

export function DiffView({ files, layout }: DiffViewProps) {
  const large = useMediaQuery(LARGE_QUERY);
  const mode = layout ?? (large ? "split" : "unified");
  return (
    <div className="flex flex-col gap-3">
      {files.map((file, index) => (
        <FileDiffCard key={`${file.filename}-${index}`} file={file} mode={mode} />
      ))}
    </div>
  );
}

function FileDiffCard({ file, mode }: { file: QuestionFile; mode: "unified" | "split" }) {
  const lines = useMemo(() => lineDiff(file.original, file.final), [file.original, file.final]);
  const stats = diffStats(lines);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const items = useMemo<Indexed[]>(() => {
    const collapsed = collapse(lines);
    // An expanded gap is replaced by its lines.
    return collapsed.flatMap((item, index): Indexed[] =>
      item.kind === "gap" && expanded.has(index) ? item.lines : [{ ...item, gapIndex: index }],
    );
  }, [lines, expanded]);
  const expand = (index: number) => setExpanded((current) => new Set(current).add(index));
  const [wrap, setWrap] = useState(false);

  return (
    <section aria-label={`Diff of ${file.filename}`} className="min-w-0 overflow-hidden rounded-md border border-border">
      <header className="flex min-h-8 items-center gap-2 border-b border-border bg-surface-raised px-2.5 py-1">
        <FileDiff className="h-4 w-4 shrink-0 text-muted" aria-hidden />
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-heading" title={file.filename}>
          {file.filename}
        </span>
        <span className="font-mono text-2xs text-success">+{stats.added}</span>
        <span className="font-mono text-2xs text-danger">−{stats.removed}</span>
        {mode === "unified" && (
          <button
            type="button"
            aria-pressed={wrap}
            onClick={() => setWrap((current) => !current)}
            className={cn(
              "-my-1 inline-flex min-h-11 min-w-11 items-center justify-center gap-1 rounded px-1.5 text-2xs hover:bg-hover md:min-h-6 md:min-w-0",
              wrap ? "text-accent" : "text-muted",
            )}
          >
            <WrapText className="h-3 w-3" aria-hidden />
            Wrap
          </button>
        )}
      </header>
      {stats.added === 0 && stats.removed === 0 ? (
        <p className="px-2.5 py-2 text-xs text-muted">No changes.</p>
      ) : (
        // Focusable: unwrapped code scrolls sideways, and a keyboard must reach it (axe's scrollable-region-focusable).
        <div className="overflow-x-auto" tabIndex={0} role="group" aria-label={`Changes in ${file.filename}`}>
          {mode === "split" ? <SplitTable items={items} onExpand={expand} /> : <UnifiedTable items={items} onExpand={expand} wrap={wrap} />}
        </div>
      )}
    </section>
  );
}

type Indexed = DiffItem & { gapIndex?: number };

const rowTone: Record<DiffLine["kind"], string> = {
  add: "bg-diff-add",
  del: "bg-diff-del",
  same: "",
};
const marker: Record<DiffLine["kind"], string> = { add: "+", del: "−", same: " " };

function UnifiedTable({ items, onExpand, wrap }: { items: Indexed[]; onExpand: (index: number) => void; wrap: boolean }) {
  return (
    <table className="w-full border-collapse font-mono text-2xs leading-5" data-layout="unified" data-wrap={wrap ? "on" : "off"}>
      <tbody>
        {items.map((item, index) =>
          item.kind === "gap" ? (
            <GapRow key={`g${index}`} gap={item} columns={3} onExpand={() => onExpand(item.gapIndex ?? -1)} />
          ) : (
            <tr key={index} className={rowTone[item.kind]} data-kind={item.kind}>
              <LineNo value={item.kind === "del" ? item.oldNo : item.newNo} narrow />
              <td aria-hidden className={cn("w-4 select-none pl-1 text-center align-top", markerTone(item.kind))}>
                {marker[item.kind]}
              </td>
              <Code text={item.text} nowrap={!wrap} />
            </tr>
          ),
        )}
      </tbody>
    </table>
  );
}

function SplitTable({ items, onExpand }: { items: Indexed[]; onExpand: (index: number) => void }) {
  const rows = splitRows(items) as (SplitRow | (DiffGap & { gapIndex?: number }))[];
  return (
    <table className="w-full table-fixed border-collapse font-mono text-2xs leading-5" data-layout="split">
      <colgroup>
        <col className="w-10" />
        <col />
        <col className="w-10" />
        <col />
      </colgroup>
      <tbody>
        {rows.map((row, index) =>
          "kind" in row ? (
            <GapRow key={`g${index}`} gap={row} columns={4} onExpand={() => onExpand(row.gapIndex ?? -1)} />
          ) : (
            <tr key={index}>
              <LineNo value={row.left?.oldNo} className={row.left ? rowTone[row.left.kind] : "bg-surface-sunken"} />
              <Code text={row.left?.text} className={cn("border-r border-border", row.left ? rowTone[row.left.kind] : "bg-surface-sunken")} kind={row.left?.kind} />
              <LineNo value={row.right?.newNo} className={row.right ? rowTone[row.right.kind] : "bg-surface-sunken"} />
              <Code text={row.right?.text} className={row.right ? rowTone[row.right.kind] : "bg-surface-sunken"} kind={row.right?.kind} />
            </tr>
          ),
        )}
      </tbody>
    </table>
  );
}

function markerTone(kind: DiffLine["kind"]): string {
  return kind === "add" ? "text-success" : kind === "del" ? "text-danger" : "text-muted";
}

function LineNo({ value, className, narrow }: { value?: number; className?: string; narrow?: boolean }) {
  return (
    <td className={cn("select-none border-r border-border text-right align-top text-muted", narrow ? "w-8 px-1" : "w-10 px-1.5", className)}>{value ?? ""}</td>
  );
}

function Code({ text, className, kind, nowrap }: { text?: string; className?: string; kind?: DiffLine["kind"]; nowrap?: boolean }) {
  return (
    <td className={cn(nowrap ? "whitespace-pre" : "whitespace-pre-wrap break-all", "px-2 align-top text-heading", className)} data-kind={kind}>
      {text ?? ""}
    </td>
  );
}

function GapRow({ gap, columns, onExpand }: { gap: DiffGap; columns: number; onExpand: () => void }) {
  return (
    <tr className="bg-surface-sunken">
      <td colSpan={columns} className="p-0">
        <button type="button" onClick={onExpand} className="w-full px-2 py-1 text-left text-2xs text-muted hover:bg-hover hover:text-heading">
          ⋯ {gap.count} unchanged {gap.count === 1 ? "line" : "lines"}
        </button>
      </td>
    </tr>
  );
}
