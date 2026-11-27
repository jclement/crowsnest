// The Journal (DESIGN.md 7.4 and 12): what crowsnest did on its own or for the human, newest
// first: pokes and skipped pokes with the reason, prompts sent, answers, restarts, alerts and
// backstop nudges. A dense table with a kind filter and "Load older"; reached from the Chat menu
// and the Jobs view as a Sheet (JournalSheet).
import { NotebookText, RefreshCw } from "lucide-react";
import { useState } from "react";
import { cn } from "../../lib/cn";
import { relativeTime } from "../../lib/format";
import { journalKindLabels, journalKinds, type JournalEntry, type JournalKind } from "../../lib/journal";
import { useJournal } from "../../lib/queries";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Chip, type ChipTone } from "../ui/Chip";
import { EmptyState } from "../ui/EmptyState";
import { Sheet } from "../ui/Sheet";
import { SkeletonRows } from "../ui/Skeleton";

const kindTones: Record<JournalKind, ChipTone> = {
  poke: "accent",
  skip: "neutral",
  send: "neutral",
  answer: "success",
  restart: "warning",
  alert: "danger",
  backstop: "warning",
  task: "accent",
  upkeep: "neutral",
  pause: "warning",
  land: "success",
  review: "accent",
  digest: "neutral",
};

export function JournalPanel() {
  const [kind, setKind] = useState<JournalKind | "all">("all");
  const journal = useJournal(kind === "all" ? [] : [kind]);
  const entries = journal.data?.pages.flatMap((page) => page.entries) ?? [];
  const total = journal.data?.pages[0]?.total ?? 0;

  return (
    <div className="flex min-h-0 flex-col">
      <div role="group" aria-label="Filter by kind" className="flex flex-wrap gap-1 border-b border-border px-3 py-2">
        {(["all", ...journalKinds] as const).map((option) => (
          <button
            key={option}
            type="button"
            aria-pressed={kind === option}
            onClick={() => setKind(option)}
            className={cn(
              "min-h-11 rounded border px-2 text-xs md:min-h-7",
              kind === option ? "border-accent bg-accent/10 text-accent" : "border-border text-muted hover:bg-hover hover:text-heading",
            )}
          >
            {option === "all" ? "All" : journalKindLabels[option]}
          </button>
        ))}
      </div>
      {journal.isPending ? (
        <SkeletonRows rows={6} label="Loading the journal" />
      ) : journal.isError ? (
        <Banner tone="danger" role="alert" action={<Button compact icon={RefreshCw} onClick={() => void journal.refetch()}>Retry</Button>}>
          Could not load the journal: {journal.error.message}
        </Banner>
      ) : entries.length === 0 ? (
        <EmptyState icon={NotebookText} title="Nothing journalled yet">
          Pokes, skipped pokes, prompts you send, answers, restarts and alerts appear here.
        </EmptyState>
      ) : (
        <>
          <table className="w-full table-fixed border-collapse text-xs">
            <caption className="sr-only">Journal, newest first ({total} entries)</caption>
            <thead className="sticky top-0 z-10 bg-surface-raised text-left text-2xs uppercase tracking-wide text-muted">
              <tr className="border-b border-border">
                <th scope="col" className="w-20 px-2 py-1 font-semibold">When</th>
                <th scope="col" className="w-20 px-2 py-1 font-semibold">Kind</th>
                <th scope="col" className="px-2 py-1 font-semibold">What</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((entry) => (
                <JournalRow key={entry.id} entry={entry} />
              ))}
            </tbody>
          </table>
          <div className="flex items-center justify-between gap-2 border-t border-border px-3 py-2 text-2xs text-muted">
            <span>
              {entries.length} of {total}
            </span>
            {journal.hasNextPage && (
              <Button compact variant="ghost" onClick={() => void journal.fetchNextPage()} disabled={journal.isFetchingNextPage}>
                {journal.isFetchingNextPage ? "Loading…" : "Load older"}
              </Button>
            )}
          </div>
        </>
      )}
    </div>
  );
}

function JournalRow({ entry }: { entry: JournalEntry }) {
  return (
    <tr className="border-b border-border align-top hover:bg-hover">
      <td className="whitespace-nowrap px-2 py-1 text-muted">
        <time dateTime={entry.at} title={new Date(entry.at).toLocaleString()}>
          {relativeTime(entry.at)}
        </time>
      </td>
      <td className="px-2 py-1">
        <Chip tone={kindTones[entry.kind] ?? "neutral"}>{journalKindLabels[entry.kind] ?? entry.kind}</Chip>
      </td>
      <td className="break-words px-2 py-1 text-body">{entry.summary}</td>
    </tr>
  );
}

/** The Journal in a Sheet: full screen on a phone, a modal from md. */
export function JournalSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Sheet open={open} onClose={onClose} title="Journal">
      {open && <JournalPanel />}
    </Sheet>
  );
}
