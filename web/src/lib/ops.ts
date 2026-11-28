// Day thirty (DESIGN.md 4.17 to 4.21): the TypeScript mirror of internal/retention's Storage and
// Report, internal/machines' Token and Machine, httpd's Summary, TailscaleResponse and
// UpdateNotice, with their endpoints and query keys.
import { api } from "./api";

export interface Area {
  name: string;
  label: string;
  bytes: number;
  count: number;
  oldest?: string;
  note?: string;
}

export interface TableUse {
  table: string;
  label: string;
  rows: number;
  oldest?: string;
}

export interface HeldJob {
  id: string;
  title: string;
  state: string;
  settled_at: string;
  reason: string;
}

export interface CleanupReport {
  dry_run: boolean;
  at: string;
  policy: { ArchiveAfterDays: number; DeleteAfterDays: number };
  archive: number;
  delete_jobs: number;
  held: HeldJob[];
  confirmed: number;
  sweeps: { name: string; label: string; count: number }[];
  state_dirs: number;
  state_bytes: number;
  blobs_freed: number;
  errors?: string[];
}

export interface Storage {
  areas: Area[];
  tables: TableUse[];
  policy: { ArchiveAfterDays: number; DeleteAfterDays: number };
  last_run: CleanupReport | null;
  worktrees: string;
}

export interface Summary {
  name: string;
  version: string;
  attention: number;
  ready_to_land: number;
  review_objects: number;
  questions: number;
  blocking: number;
  approvals: number;
  needs_you: number;
  running: number;
  jobs: Record<string, number>;
  orchestrator: string;
  paused: boolean;
  stats?: { cpu: number; mem_used: number; mem_total: number; load1: number };
  at: string;
}

export interface ApiToken {
  id: number;
  name: string;
  scope: string;
  hint: string;
  created_at: string;
  last_used_at?: string;
  revoked_at?: string;
}

export interface Machine {
  id: number;
  name: string;
  url: string;
  created_at: string;
  summary?: Summary;
  error?: string;
}

export interface TailscaleState {
  enabled: boolean;
  serve_configured: boolean;
  current_login: string;
  via_tailscale: boolean;
  allowed: string[];
  /** Sign in with Tailscale together with publishing (R9-S3): the risk, in words. */
  local_risk?: string;
}

export interface UpdateNotice {
  from: string;
  to: string;
  at: string;
  release_url: string;
}

export const opsEndpoints = {
  storage: () => api<Storage>("/storage"),
  cleanup: (dryRun: boolean, confirm: string[] = []) => api<CleanupReport>("/storage/cleanup", { method: "POST", body: { dry_run: dryRun, confirm } }),
  summary: () => api<Summary>("/summary"),
  tokens: () => api<ApiToken[]>("/tokens"),
  mintToken: (name: string) => api<{ token: string; record: ApiToken }>("/tokens", { method: "POST", body: { name } }),
  revokeToken: (id: number) => api<null>(`/tokens/${id}`, { method: "DELETE" }),
  machines: () => api<Machine[]>("/machines"),
  addMachine: (input: { name: string; url: string; token: string }) => api<Machine>("/machines", { method: "POST", body: input }),
  removeMachine: (id: number) => api<null>(`/machines/${id}`, { method: "DELETE" }),
  tailscale: () => api<TailscaleState>("/auth/tailscale"),
  setTailscale: (logins: string[]) => api<TailscaleState>("/auth/tailscale", { method: "PUT", body: { logins } }),
  allowMe: () => api<TailscaleState>("/auth/tailscale/allow-me", { method: "POST", body: {} }),
  updated: () => api<UpdateNotice | null>("/updated"),
  dismissUpdated: () => api<null>("/updated/seen", { method: "POST", body: {} }),
};

export const opsKeys = {
  storage: ["storage"] as const,
  tokens: ["tokens"] as const,
  machines: ["machines"] as const,
  tailscale: ["tailscale"] as const,
  updated: ["updated"] as const,
};

/** The URL "Download backup" opens: a fresh backup, streamed as a file. */
export const backupURL = "/api/v1/backup";

/** Bytes as a short figure: 1.2 MB. */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[unit]}`;
}
