// The Spend sheet (DESIGN.md 7.11): today's, this week's or the last 30 days' spend broken down by
// job (the orchestrator is its own row), by model or by day, as a sortable table with CSV export,
// and the header button that opens it showing today's and this week's totals. Every figure is
// marked an estimate, with the price table's date and the note that subscriptions are not billed
// per token. Reached from the Jobs view, the Chat menu and the command palette.
import { ArrowDown, ArrowUp, Coins, Download, RefreshCw } from "lucide-react";
import { useState } from "react";
import { cn } from "../../lib/cn";
import { useSpendReport, useSpendSummary } from "../../lib/queries";
import {
  costText,
  formatTokens,
  formatUSD,
  rangeDates,
  reportCSV,
  sortRows,
  SPEND_NOTE,
  type SpendGroup,
  type SpendRange,
  type SpendReport,
  type SpendRow,
  type SpendSort,
  type SpendSortKey,
} from "../../lib/spend";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { EmptyState } from "../ui/EmptyState";
import { Sheet } from "../ui/Sheet";
import { SkeletonRows } from "../ui/Skeleton";
import { TabPanel, Tabs } from "../ui/Tabs";

/** The header button: today and this week at a glance. Opens the sheet. */
export function SpendButton({ onOpen }: { onOpen: () => void }) {
  const summary = useSpendSummary();
  const data = summary.data;
  const money = (row: SpendRow | undefined) => (!row ? "…" : row.cost_usd === null ? formatTokens(row.total_tokens) + " tok" : formatUSD(row.cost_usd));
  return (
    <Button compact variant="ghost" icon={Coins} onClick={onOpen} title={`Estimated spend. ${SPEND_NOTE}`}>
      {/* The accessible name starts and ends with words only a screen reader needs, and contains the
          visible text whole (axe's label-in-name rule). */}
      <span className="font-mono">
        <span className="sr-only">Spend: </span>
        {money(data?.today)} <span className="text-muted">today</span> · {money(data?.week)} <span className="text-muted">wk</span>
        <span className="sr-only"> (estimates)</span>
      </span>
    </Button>
  );
}

export function SpendSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Sheet open={open} onClose={onClose} title="Spend (estimated)">
      {open && <SpendPanel />}
    </Sheet>
  );
}

const rangeLabels: Record<SpendRange, string> = { today: "Today", week: "This week", "30d": "30 days" };
const groupLabels: Record<SpendGroup, string> = { job: "By job", model: "By model", day: "By day" };

export function SpendPanel() {
  const [range, setRange] = useState<SpendRange>("week");
  const [group, setGroup] = useState<SpendGroup>("job");
  const summary = useSpendSummary();
  const { from, to } = rangeDates(range, summary.data);
  const report = useSpendReport(group, from, to, !!summary.data);
  return (
    <div className="flex min-h-0 flex-col gap-2 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Tabs<SpendRange> id="spend-range" label="Range" className="w-full whitespace-nowrap sm:w-auto" value={range} onChange={setRange} items={(["today", "week", "30d"] as const).map((value) => ({ value, label: rangeLabels[value] }))} />
        <Tabs<SpendGroup> id="spend-group" label="Group by" className="w-full whitespace-nowrap sm:w-auto" value={group} onChange={setGroup} items={(["job", "model", "day"] as const).map((value) => ({ value, label: groupLabels[value] }))} />
      </div>
      <TabPanel id="spend-range" value={range} className="flex min-h-0 flex-col">
        <TabPanel id="spend-group" value={group} className="flex min-h-0 flex-col gap-2">
          <ReportBody report={report.data} pending={report.isPending || summary.isPending} error={report.error ?? summary.error} retry={() => void report.refetch()} />
        </TabPanel>
      </TabPanel>
    </div>
  );
}

function ReportBody({ report, pending, error, retry }: { report: SpendReport | undefined; pending: boolean; error: Error | null; retry: () => void }) {
  if (error && !report) {
    return (
      <Banner tone="danger" role="alert" action={<Button compact icon={RefreshCw} onClick={retry}>Retry</Button>}>
        Could not load spend: {error.message}
      </Banner>
    );
  }
  if (pending || !report) return <SkeletonRows rows={5} label="Loading spend" />;
  return (
    <>
      <div className="flex flex-wrap items-baseline justify-between gap-2 text-xs">
        <span>
          <span className="font-mono text-sm font-semibold text-heading">{costText(report.total)}</span>{" "}
          <span className="text-muted">
            · {formatTokens(report.total.total_tokens)} tokens · {report.total.turns} model calls ·{" "}
            <span className="whitespace-nowrap">{report.from === report.to ? report.from : `${report.from} to ${report.to}`}</span>
          </span>
        </span>
        <Button compact variant="secondary" icon={Download} onClick={() => download(report)} disabled={report.rows.length === 0}>
          CSV
        </Button>
      </div>
      {report.rows.length === 0 ? (
        <EmptyState icon={Coins} title="Nothing spent in this range">
          Spend is read from each job's Claude transcript as the agent works.
        </EmptyState>
      ) : (
        <SpendTable report={report} />
      )}
      <p className="text-2xs text-muted">
        {SPEND_NOTE} Prices checked {report.prices_checked}
        {report.prices_file ? `, with overrides from ${report.prices_file}` : ""}. Codex jobs are not measured; jobs the orchestrator holds are on its row.
      </p>
    </>
  );
}

/** Columns: the label, then numbers; the narrow ones hide on a phone. */
const columns: { key: SpendSortKey; label: string; numeric: boolean; wide?: boolean; value: (row: SpendRow) => string }[] = [
  { key: "label", label: "Name", numeric: false, value: (row) => row.label },
  { key: "cost", label: "Est. cost", numeric: true, value: (row) => (row.cost_usd === null ? "unknown" : `${row.cost_partial ? "≥" : ""}${formatUSD(row.cost_usd)}`) },
  { key: "total", label: "Tokens", numeric: true, value: (row) => formatTokens(row.total_tokens) },
  { key: "input", label: "In", numeric: true, wide: true, value: (row) => formatTokens(row.input_tokens) },
  { key: "output", label: "Out", numeric: true, wide: true, value: (row) => formatTokens(row.output_tokens) },
  { key: "cache_write", label: "Cache w", numeric: true, wide: true, value: (row) => formatTokens(row.cache_write_tokens) },
  { key: "cache_read", label: "Cache r", numeric: true, wide: true, value: (row) => formatTokens(row.cache_read_tokens) },
  { key: "turns", label: "Calls", numeric: true, wide: true, value: (row) => String(row.turns) },
  { key: "tool_calls", label: "Tools", numeric: true, wide: true, value: (row) => String(row.tool_calls) },
];

export function SpendTable({ report }: { report: SpendReport }) {
  const [sort, setSort] = useState<SpendSort>(report.group === "day" ? { key: "label", descending: false } : { key: "cost", descending: true });
  const rows = sortRows(report.rows, sort);
  const press = (key: SpendSortKey) => setSort((current) => (current.key === key ? { key, descending: !current.descending } : { key, descending: key !== "label" }));
  return (
    <div className="min-h-0 overflow-auto rounded-md border border-border">
      <table className="w-full border-collapse text-xs">
        <thead className="sticky top-0 bg-surface-raised">
          <tr>
            {columns.map((column) => {
              const active = sort.key === column.key;
              return (
                <th
                  key={column.key}
                  scope="col"
                  aria-sort={active ? (sort.descending ? "descending" : "ascending") : "none"}
                  className={cn("border-b border-border p-0 font-semibold text-muted", column.wide && "hidden md:table-cell")}
                >
                  <button
                    type="button"
                    onClick={() => press(column.key)}
                    className={cn("flex min-h-11 w-full items-center gap-1 px-2 hover:text-heading md:min-h-7", column.numeric ? "justify-end" : "justify-start", active && "text-heading")}
                  >
                    {column.label}
                    {active && (sort.descending ? <ArrowDown className="h-3 w-3" aria-hidden /> : <ArrowUp className="h-3 w-3" aria-hidden />)}
                  </button>
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.key} className="border-b border-border hover:bg-hover">
              {columns.map((column) => (
                <td
                  key={column.key}
                  className={cn(
                    "px-2 py-1",
                    column.numeric ? "whitespace-nowrap text-right font-mono" : "max-w-[12rem] truncate text-heading md:max-w-xs",
                    column.wide && "hidden md:table-cell",
                  )}
                  title={column.key === "label" ? `${row.label} (${row.key})` : undefined}
                >
                  {column.value(row)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
        <tfoot>
          <tr className="bg-surface-raised font-semibold">
            {columns.map((column) => (
              <td key={column.key} className={cn("px-2 py-1", column.numeric && "whitespace-nowrap text-right font-mono", column.wide && "hidden md:table-cell")}>
                {column.value(report.total)}
              </td>
            ))}
          </tr>
        </tfoot>
      </table>
    </div>
  );
}

function download(report: SpendReport) {
  const blob = new Blob([reportCSV(report)], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `crowsnest-spend-${report.group}-${report.from}-${report.to}.csv`;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
