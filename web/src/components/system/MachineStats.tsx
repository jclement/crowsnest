// The machine at a glance (DESIGN.md 12.x): the header's strip and the System page's card.
//
// The strip shows CPU % with a one-minute sparkline, memory used of total, the 1-minute load
// coloured against the core count, and from lg up free disk on the data volume and the agents
// working. On a phone it folds into one chip (CPU %) that opens the same figures on tap. Every
// figure has a fixed width in tabular numerals, so nothing shifts as values change, and a tooltip
// with the detail. Saturation (load over two per core, CPU over 90 %, memory over 95 %) turns a
// figure to the warning colour with a triangle beside it, never colour alone.
//
// The figures arrive as `sys` events every two seconds while a browser is connected (lib/live.ts
// appends them to the query below); the query's first load brings the last minute.
import { HeavyDetail } from "../control/HeavySlots";
import { useQuery } from "@tanstack/react-query";
import { Bot, Cpu, HardDrive, MemoryStick, TriangleAlert, Gauge } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "../../lib/cn";
import { cpuLevel, loadLevel, memLevel, shortBytes, sysEndpoints, sysKeys, useSysStats, type Level, type SysSample, type SysStats } from "../../lib/sysstats";
import { Card } from "../ui/Card";

const levelInk: Record<Level, string> = { ok: "text-chrome-fg", busy: "text-chrome-fg", saturated: "text-chrome-warning" };

/**
 * A tiny sparkline of CPU % over the last minute: one series, a 1.5 px line over a faint fill to
 * the baseline, no axes. Its colour is the caller's (the header draws it in the chrome's accent).
 */
export function Sparkline({ values, width = 48, height = 16, className }: { values: number[]; width?: number; height?: number; className?: string }) {
  const baseline = <line x1={0} x2={width} y1={height - 0.5} y2={height - 0.5} stroke="currentColor" strokeOpacity={0.25} strokeWidth={1} />;
  if (values.length < 2) {
    // Not enough samples for a line yet: the baseline alone, so the slot reads as a chart.
    return (
      <svg aria-hidden width={width} height={height} viewBox={`0 0 ${width} ${height}`} className={cn("shrink-0", className)}>
        {baseline}
      </svg>
    );
  }
  const step = width / (values.length - 1);
  const y = (value: number) => height - 1 - (Math.min(100, Math.max(0, value)) / 100) * (height - 2);
  const points = values.map((value, index) => `${(index * step).toFixed(1)},${y(value).toFixed(1)}`).join(" ");
  return (
    <svg aria-hidden width={width} height={height} viewBox={`0 0 ${width} ${height}`} className={cn("shrink-0 overflow-visible", className)}>
      {baseline}
      <polygon points={`0,${height - 0.5} ${points} ${width},${height - 0.5}`} fill="currentColor" fillOpacity={0.14} stroke="none" />
      <polyline points={points} fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}

function Figure({ icon, label, title, level = "ok", width, children }: { icon: ReactNode; label: string; title: string; level?: Level; width: string; children: ReactNode }) {
  return (
    <span title={title} aria-label={`${label}: ${title}`} className={cn("inline-flex shrink-0 items-center gap-1 text-xs tabular-nums", levelInk[level])} style={{ minWidth: width }}>
      <span className="text-chrome-muted" aria-hidden>
        {icon}
      </span>
      {children}
      {level === "saturated" && <TriangleAlert className="h-3 w-3" aria-label="saturated" />}
    </span>
  );
}

function cpuTitle(sample: SysSample) {
  return `${sample.cpu.toFixed(0)}% of ${sample.cores.toFixed(sample.cores % 1 ? 1 : 0)} cores${sample.scope === "container" ? " (this container's quota)" : ""}`;
}

function memTitle(sample: SysSample) {
  const share = sample.mem_total ? (100 * sample.mem_used) / sample.mem_total : 0;
  return `${shortBytes(sample.mem_used)} of ${shortBytes(sample.mem_total)} used (${share.toFixed(0)}%)${sample.scope === "container" ? ", this container's limit" : ""}`;
}

function loadTitle(sample: SysSample) {
  return `Load ${sample.load1.toFixed(2)} / ${sample.load5.toFixed(2)} / ${sample.load15.toFixed(2)} (1, 5, 15 min) on ${sample.cores.toFixed(0)} cores`;
}

/** The figures in a row; shared by the desktop strip and the phone popover. */
function Figures({ stats, wide }: { stats: SysStats; wide?: boolean }) {
  const sample = stats.latest;
  if (!sample) return null;
  return (
    <>
      <Figure icon={<Cpu className="h-3.5 w-3.5" />} label="CPU" title={cpuTitle(sample)} level={cpuLevel(sample.cpu)} width="5.75rem">
        <Sparkline values={stats.history.map((point) => point.cpu)} className="text-chrome-accent" />
        <span className="w-8 text-right">{sample.cpu.toFixed(0)}%</span>
      </Figure>
      <Figure icon={<MemoryStick className="h-3.5 w-3.5" />} label="Memory" title={memTitle(sample)} level={memLevel(sample.mem_used, sample.mem_total)} width="6.5rem">
        <span>
          {shortBytes(sample.mem_used)}
          <span className="text-chrome-muted">/{shortBytes(sample.mem_total)}</span>
        </span>
      </Figure>
      <Figure icon={<Gauge className="h-3.5 w-3.5" />} label="Load" title={loadTitle(sample)} level={loadLevel(sample.load1, sample.cores)} width="3.5rem">
        <span>{sample.load1.toFixed(sample.load1 >= 10 ? 0 : 1)}</span>
      </Figure>
      {wide && sample.disk_total > 0 && (
        <Figure icon={<HardDrive className="h-3.5 w-3.5" />} label="Disk" title={`${shortBytes(sample.disk_free)} free of ${shortBytes(sample.disk_total)} on the data volume`} width="3.75rem">
          <span>{shortBytes(sample.disk_free)}</span>
        </Figure>
      )}
      {wide && sample.working >= 0 && (
        <Figure icon={<Bot className="h-3.5 w-3.5" />} label="Agents working" title={`${sample.working} agent${sample.working === 1 ? "" : "s"} working`} width="2.25rem">
          <span>{sample.working}</span>
        </Figure>
      )}
    </>
  );
}

/** The header strip: inline from md, a tap-to-expand chip on phones. */
export function MachineStrip({ className }: { className?: string }) {
  const stats = useSysStats();
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [open]);
  const sample = stats.data?.latest;
  if (!stats.data || !sample) return null;
  const worst = [cpuLevel(sample.cpu), loadLevel(sample.load1, sample.cores), memLevel(sample.mem_used, sample.mem_total)].includes("saturated");
  return (
    <div ref={rootRef} className={cn("relative flex shrink-0 items-center", className)} data-testid="machine-strip">
      <div className="hidden items-center gap-3 md:flex lg:hidden">
        <Figures stats={stats.data} />
      </div>
      <div className="hidden items-center gap-3 lg:flex">
        <Figures stats={stats.data} wide />
      </div>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        aria-label={`Machine: ${cpuTitle(sample)}. Show more`}
        className={cn(
          "inline-flex h-11 items-center gap-1 rounded px-1.5 text-xs tabular-nums hover:bg-chrome-hover md:hidden",
          worst ? "text-chrome-warning" : "text-chrome-fg",
        )}
      >
        {worst ? <TriangleAlert className="h-3.5 w-3.5" aria-hidden /> : <Cpu className="h-3.5 w-3.5 text-chrome-muted" aria-hidden />}
        <span className="w-8 text-right">{sample.cpu.toFixed(0)}%</span>
      </button>
      {open && (
        <div role="dialog" aria-label="Machine" className="absolute left-0 top-full z-30 mt-1 flex w-max max-w-[calc(100vw-1.5rem)] flex-col gap-2 rounded-md border border-chrome-border bg-chrome p-3 shadow-xl md:hidden">
          <Figures stats={stats.data} wide />
        </div>
      )}
    </div>
  );
}

/** The System page's card: the same figures larger, with what each means, and each agent's share. */
export function MachineDetail() {
  const stats = useSysStats();
  const agents = useQuery({ queryKey: sysKeys.agents, queryFn: sysEndpoints.agents, refetchInterval: 5_000 });
  const sample = stats.data?.latest;
  return (
    <Card title={`Machine${sample?.scope === "container" ? " (this container's cgroup)" : ""}`}>
      {!sample && <p className="text-xs text-muted">Waiting for the first sample…</p>}
      {sample && (
        <div className="flex flex-col gap-3">
          <dl className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-4">
            <Stat label="CPU" value={`${sample.cpu.toFixed(0)}%`} detail={cpuTitle(sample)} level={cpuLevel(sample.cpu)}>
              <Sparkline values={stats.data?.history.map((point) => point.cpu) ?? []} width={96} height={24} className="text-accent" />
            </Stat>
            <Stat label="Memory" value={`${shortBytes(sample.mem_used)} / ${shortBytes(sample.mem_total)}`} detail={memTitle(sample)} level={memLevel(sample.mem_used, sample.mem_total)} />
            <Stat label="Load" value={sample.load1.toFixed(2)} detail={loadTitle(sample)} level={loadLevel(sample.load1, sample.cores)} />
            <Stat label="Disk free" value={sample.disk_total ? shortBytes(sample.disk_free) : "-"} detail="on the data volume" level="ok" />
          </dl>
          <div>
            <h3 className="mb-1 text-2xs uppercase tracking-wide text-muted">Agents (their process and everything it runs)</h3>
            {agents.data && agents.data.length === 0 && <p className="text-xs text-muted">No agents running.</p>}
            {agents.data && agents.data.length > 0 && (
              <table className="w-full text-xs">
                <thead className="text-left text-2xs text-muted">
                  <tr>
                    <th className="py-1 font-normal">Agent</th>
                    <th className="py-1 text-right font-normal">CPU</th>
                    <th className="py-1 text-right font-normal">Memory</th>
                    <th className="hidden py-1 text-right font-normal sm:table-cell">Processes</th>
                  </tr>
                </thead>
                <tbody className="tabular-nums">
                  {agents.data.map((agent) => (
                    <tr key={agent.pane_id} className="border-t border-border">
                      <td className="max-w-0 truncate py-1 text-heading">
                        {agent.name || agent.pane_id} <span className="text-muted">{agent.kind}</span>
                      </td>
                      <td className="py-1 text-right">{agent.cpu.toFixed(0)}%</td>
                      <td className="py-1 text-right">{shortBytes(agent.rss)}</td>
                      <td className="hidden py-1 text-right sm:table-cell">{agent.processes}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
          <HeavyDetail />
        </div>
      )}
    </Card>
  );
}

const statInk: Record<Level, string> = { ok: "text-heading", busy: "text-heading", saturated: "text-warning" };

function Stat({ label, value, detail, level, children }: { label: string; value: string; detail: string; level: Level; children?: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <dt className="text-2xs uppercase tracking-wide text-muted">{label}</dt>
      <dd className={cn("flex items-center gap-1 text-sm font-semibold tabular-nums", statInk[level])}>
        {value}
        {level === "saturated" && <TriangleAlert className="h-3.5 w-3.5" aria-label="saturated" />}
      </dd>
      {children}
      <dd className="text-2xs text-muted">{detail}</dd>
    </div>
  );
}
