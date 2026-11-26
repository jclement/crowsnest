// Machine stats (DESIGN.md 12.x): the TypeScript mirror of internal/sysstat's Sample and Stats,
// the query that loads the last minute, how a `sys` event appends to it, and the thresholds
// that turn the header strip's figures to the warning colour.
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";

export interface SysSample {
  at: string;
  cpu: number;
  cores: number;
  mem_used: number;
  mem_total: number;
  load1: number;
  load5: number;
  load15: number;
  disk_free: number;
  disk_total: number;
  scope: "host" | "container";
  /** Working agents; -1 when unknown. */
  working: number;
}

export interface SysStats {
  latest: SysSample | null;
  history: SysSample[];
}

export interface AgentUsage {
  pane_id: string;
  name: string;
  kind: string;
  status: string;
  workspace: string;
  cpu: number;
  rss: number;
  processes: number;
}

/** How many samples the sparkline keeps: one minute at the server's two seconds. */
export const HISTORY_LENGTH = 30;

export const sysEndpoints = {
  stats: () => api<SysStats>("/system/stats"),
  agents: () => api<AgentUsage[]>("/system/agents"),
};

export const sysKeys = { stats: ["sys-stats"] as const, agents: ["sys-agents"] as const };

/** True when data looks like a Sample. */
export function isSample(data: unknown): data is SysSample {
  const candidate = data as Partial<SysSample> | null;
  return !!candidate && typeof candidate.cpu === "number" && typeof candidate.mem_total === "number" && typeof candidate.at === "string";
}

/** The stats after a `sys` event: the sample appended, the oldest dropped past a minute. */
export function appendSample(previous: SysStats | undefined, sample: SysSample): SysStats {
  const history = [...(previous?.history ?? []), sample].slice(-HISTORY_LENGTH);
  return { latest: sample, history };
}

export type Level = "ok" | "busy" | "saturated";

/** CPU: busy past 70 %, saturated past 90 %. */
export function cpuLevel(cpu: number): Level {
  if (cpu >= 90) return "saturated";
  if (cpu >= 70) return "busy";
  return "ok";
}

/** Load against the cores: busy past one per core, saturated past two (the 63 on 16 cores case). */
export function loadLevel(load: number, cores: number): Level {
  const perCore = cores > 0 ? load / cores : 0;
  if (perCore >= 2) return "saturated";
  if (perCore >= 1) return "busy";
  return "ok";
}

/** Memory: busy past 85 % used, saturated past 95 %. */
export function memLevel(used: number, total: number): Level {
  const share = total > 0 ? used / total : 0;
  if (share >= 0.95) return "saturated";
  if (share >= 0.85) return "busy";
  return "ok";
}

/** Bytes as a short figure: 12.4G, 512M. */
export function shortBytes(value: number): string {
  const units = ["B", "K", "M", "G", "T"];
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return (value >= 100 || unit === 0 ? value.toFixed(0) : value.toFixed(1)) + units[unit];
}

/** The stats query; `sys` events keep it current (lib/live.ts). */
export function useSysStats() {
  return useQuery({ queryKey: sysKeys.stats, queryFn: sysEndpoints.stats, staleTime: Infinity, refetchOnWindowFocus: false });
}
