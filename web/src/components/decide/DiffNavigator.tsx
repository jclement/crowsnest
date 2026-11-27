// DiffNavigator: a long diff made navigable on a phone (R4 Codex P1, Fable 3.6). A file list
// first (name, +added −removed, a tap jumps to the file and opens it), then each file as a
// collapsible section around the Inbox's DiffView. Files are collapsed by default when the diff
// is big (more than SMALL_FILES files or SMALL_LINES changed lines in all), so the packet opens
// on its decision, not on line 1 of a 900-line patch; a small diff is shown open.
import { ChevronDown, ChevronRight, FileDiff } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { cn } from "../../lib/cn";
import { diffStats, lineDiff } from "../../lib/diff";
import type { QuestionFile } from "../../lib/questions";
import { DiffView } from "../inbox/DiffView";

/** At most this many files are shown open by default. */
const SMALL_FILES = 3;
/** At most this many changed lines in all are shown open by default. */
const SMALL_LINES = 120;

interface FileEntry {
  file: QuestionFile;
  added: number;
  removed: number;
}

export function DiffNavigator({ files }: { files: QuestionFile[] }) {
  const entries = useMemo<FileEntry[]>(
    () =>
      files.map((file) => {
        const stats = diffStats(lineDiff(file.original, file.final));
        return { file, added: stats.added, removed: stats.removed };
      }),
    [files],
  );
  const changed = entries.reduce((sum, entry) => sum + entry.added + entry.removed, 0);
  const small = files.length <= SMALL_FILES && changed <= SMALL_LINES;
  const [open, setOpen] = useState<Set<number>>(() => (small ? new Set(entries.map((_, index) => index)) : new Set()));
  const sections = useRef<(HTMLElement | null)[]>([]);

  const toggle = (index: number) =>
    setOpen((current) => {
      const next = new Set(current);
      if (next.has(index)) next.delete(index);
      else next.add(index);
      return next;
    });
  const jump = (index: number) => {
    setOpen((current) => new Set(current).add(index));
    // After the section opens, bring its header to the top of the scroll area.
    // (scrollIntoView is optional: jsdom has none, and the frame can run after a test ended.)
    requestAnimationFrame(() => sections.current[index]?.scrollIntoView?.({ block: "start", behavior: "smooth" }));
  };

  return (
    <div className="flex flex-col gap-2" data-testid="diff-navigator">
      <nav aria-label="Changed files" className="rounded-md border border-border">
        <p className="border-b border-border bg-surface-raised px-2.5 py-1 text-2xs text-muted">
          {files.length} {files.length === 1 ? "file" : "files"} · <span className="text-success">+{sum(entries, "added")}</span>{" "}
          <span className="text-danger">−{sum(entries, "removed")}</span>
          {!small && " · collapsed: tap a file to open it"}
        </p>
        <ul className="divide-y divide-border">
          {entries.map((entry, index) => (
            <li key={`${entry.file.filename}-${index}`}>
              <button
                type="button"
                onClick={() => jump(index)}
                className="flex min-h-11 w-full items-center gap-2 px-2.5 text-left hover:bg-hover md:min-h-8"
              >
                <FileDiff className="h-3.5 w-3.5 shrink-0 text-muted" aria-hidden />
                <span className="min-w-0 flex-1 truncate font-mono text-xs text-heading" title={entry.file.filename}>
                  {entry.file.filename}
                </span>
                <span className="font-mono text-2xs text-success">+{entry.added}</span>
                <span className="font-mono text-2xs text-danger">−{entry.removed}</span>
              </button>
            </li>
          ))}
        </ul>
      </nav>
      {entries.map((entry, index) => {
        const expanded = open.has(index);
        return (
          <section
            key={`${entry.file.filename}-${index}`}
            ref={(element) => {
              sections.current[index] = element;
            }}
            aria-label={`File ${entry.file.filename}`}
            className="scroll-mt-2"
          >
            <button
              type="button"
              aria-expanded={expanded}
              onClick={() => toggle(index)}
              className={cn(
                "flex min-h-11 w-full items-center gap-1.5 rounded-md border border-border px-2.5 text-left text-xs hover:bg-hover md:min-h-8",
                expanded && "rounded-b-none border-b-0",
              )}
            >
              {expanded ? <ChevronDown className="h-3.5 w-3.5 shrink-0" aria-hidden /> : <ChevronRight className="h-3.5 w-3.5 shrink-0" aria-hidden />}
              <span className="min-w-0 flex-1 truncate font-mono text-heading">{entry.file.filename}</span>
              <span className="text-2xs text-muted">{expanded ? "Hide" : "Show"}</span>
            </button>
            {expanded && <DiffView files={[entry.file]} />}
          </section>
        );
      })}
    </div>
  );
}

function sum(entries: FileEntry[], key: "added" | "removed"): number {
  return entries.reduce((total, entry) => total + entry[key], 0);
}
