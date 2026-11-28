// The System page, Settings and the first-run checklist (DESIGN.md 3.2, 3.4, 4.14.1): the
// TypeScript mirror of internal/doctor's Report and internal/system's SettingsPage and
// OnboardingView, their endpoints and query keys, and small helpers the views share.
import { api } from "./api";

export type CheckStatus = "ok" | "info" | "warn" | "fail" | "skip";

/** One line of the doctor report (internal/doctor Check). */
export interface Check {
  id: string;
  group: "herdr" | "agents" | "claude" | "machine" | "optional" | "config" | "live";
  title: string;
  status: CheckStatus;
  detail?: string;
  fix?: string;
  /** A shell command that fixes it (the checklist offers to run it in a terminal). */
  command?: string;
  /** Set on a check that spent something (a --deep model call). */
  cost?: string;
}

export interface AgentInfo {
  name: "claude" | "codex";
  installed: boolean;
  path?: string;
  version?: string;
  signed_in?: boolean;
  auth_method?: string;
  usable: boolean;
}

export interface Selection {
  setting: string;
  orchestrator: "" | "claude" | "codex";
  jobs: "" | "claude" | "codex";
  reason: string;
}

export interface LogLine {
  time: string;
  level: string;
  message: string;
}

export interface LiveInfo {
  running: boolean;
  setup_required?: boolean;
  version?: string;
  uptime?: string;
  started_at?: string;
  orchestrator?: string;
  orchestrator_ok?: boolean;
  detail?: string;
  herdr_connected?: boolean;
  hooks?: Record<string, string>;
  last_errors?: LogLine[];
  update_latest?: string;
}

export interface SettingSource {
  name: string;
  value: string;
  source: SourceKind;
  where: string;
}

/** GET /system: the doctor report as the running server sees it. */
export interface SystemReport {
  checked_at: string;
  verdict: "ok" | "warn" | "fail";
  summary: string;
  versions: { crowsnest: string; herdr?: string; claude?: string; codex?: string };
  agents: AgentInfo[];
  selection: Selection;
  paths: { data: string; state: string; config: string; database: string; orchestrator: string; agent_socket: string; herdr?: string; claude?: string; codex?: string };
  settings?: SettingSource[];
  checks: Check[];
  deep: boolean;
  live?: LiveInfo;
}

export type SourceKind = "flag" | "env" | "database" | "file" | "default";
export type ApplyKind = "live" | "orchestrator" | "restart";

/** One row of GET /settings (internal/system SettingView). */
export interface SettingView {
  name: string;
  label: string;
  section: string;
  help: string;
  kind: "text" | "bool" | "int" | "duration" | "list" | "choice" | "secret";
  choices?: string[];
  apply: ApplyKind;
  write_only?: boolean;
  /** A "no-…" option shown as its positive: on means the option is false. */
  invert?: boolean;
  value: string;
  is_set: boolean;
  default: string;
  source: SourceKind;
  where: string;
  locked?: string;
  pending?: boolean;
  /** The backup's value a restore set aside (R9-S2): it does not apply until used again. */
  restored_value?: string;
  restored_why?: string;
}

export interface SettingsPage {
  settings: SettingView[];
  bind: string;
  bind_command: string;
  name: string;
  config_file?: string;
  orchestrator_engine?: string;
}

export interface OrchestratorBrief {
  state: string;
  pane_id: string;
  detail?: string;
  engine?: string;
}

/** GET /onboarding: the first-run checklist. */
export interface OnboardingView {
  pending: boolean;
  report: SystemReport;
  choose_engine: boolean;
  engine: string;
  dir: string;
  orchestrator: OrchestratorBrief;
  codex_gaps?: string[];
  bind: string;
  name: string;
}

export const systemEndpoints = {
  system: (recheck = false) => api<SystemReport>(recheck ? "/system?recheck=1" : "/system"),
  /** The redacted text report for an issue (text/plain, not the JSON envelope). */
  diagnostics: async (): Promise<string> => {
    const response = await fetch("/api/v1/system/diagnostics", { headers: { "X-Crowsnest": "1" }, credentials: "same-origin" });
    if (!response.ok) throw new Error("Could not build the diagnostics (" + response.status + ")");
    return response.text();
  },
  settings: () => api<SettingsPage>("/settings"),
  updateSetting: (name: string, value: string) => api<SettingView>(`/settings/${encodeURIComponent(name)}`, { method: "PUT", body: { value } }),
  clearSetting: (name: string) => api<SettingView>(`/settings/${encodeURIComponent(name)}`, { method: "PUT", body: { clear: true } }),
  onboarding: (recheck = false) => api<OnboardingView>(recheck ? "/onboarding?recheck=1" : "/onboarding"),
  startOnboarding: (engine?: string) => api<OnboardingView>("/onboarding/start", { method: "POST", body: engine ? { engine } : {} }),
  openShell: (command: string) => api<{ pane_id: string }>("/onboarding/shell", { method: "POST", body: { command } }),
  demoState: (state: string) => api<{ state: string }>("/system/demo-state", { method: "POST", body: { state } }),
};

export const systemKeys = {
  system: ["system"] as const,
  settings: ["settings"] as const,
  onboarding: ["onboarding"] as const,
};

export const statusLabels: Record<CheckStatus, string> = { ok: "OK", info: "Info", warn: "Warning", fail: "Problem", skip: "Skipped" };

export const sourceLabels: Record<SourceKind, string> = {
  flag: "command line",
  env: "environment",
  database: "set here",
  file: "config file",
  default: "default",
};

export const applyLabels: Record<ApplyKind, string> = {
  live: "Applies at once",
  orchestrator: "Applies when the orchestrator restarts",
  restart: "Applies when crowsnest restarts",
};

/** The checks the first-run checklist shows, in its order. */
export const checklistIDs = ["herdr.installed", "herdr.session", "agent.claude", "agent.codex", "agent.selection", "claude.trust"] as const;

export function findCheck(report: SystemReport | undefined, id: string): Check | undefined {
  return report?.checks.find((check) => check.id === id);
}

/** Whether the essentials are there: herdr installed and an agent to run. */
export function readyToStart(report: SystemReport): boolean {
  const herdr = findCheck(report, "herdr.installed");
  return herdr?.status !== "fail" && report.selection.orchestrator !== "";
}
