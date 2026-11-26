// Report and settings fixtures for the System, Settings and checklist tests.
import type { OnboardingView, SettingsPage, SystemReport } from "../../lib/system";

export function report(overrides: Partial<SystemReport> = {}): SystemReport {
  return {
    checked_at: "2026-11-26T12:00:00Z",
    verdict: "ok",
    summary: "herdr 0.9.5 ✓  claude 2.1.332 ✓ signed in  codex not found",
    versions: { crowsnest: "v1.0.0", herdr: "0.9.5", claude: "2.1.332" },
    agents: [
      { name: "claude", installed: true, path: "/bin/claude", version: "2.1.332", signed_in: true, auth_method: "claude.ai", usable: true },
      { name: "codex", installed: false, usable: false },
    ],
    selection: { setting: "auto", orchestrator: "claude", jobs: "claude", reason: "Claude Code is installed and signed in" },
    paths: { data: "/d", state: "/s", config: "/c", database: "/d/crowsnest.db", orchestrator: "/d/orchestrator", agent_socket: "/s/agent.sock" },
    checks: [
      { id: "herdr.installed", group: "herdr", title: "herdr installed", status: "ok", detail: "herdr 0.9.5 at /bin/herdr" },
      { id: "herdr.session", group: "herdr", title: 'herdr session "crowsnest"', status: "ok", detail: "running" },
      { id: "agent.claude", group: "agents", title: "Claude Code", status: "ok", detail: "2.1.332, signed in" },
      { id: "agent.codex", group: "agents", title: "Codex", status: "info", detail: "not installed" },
      { id: "claude.trust", group: "claude", title: "Orchestrator folder trusted", status: "info", detail: "Claude will ask once" },
    ],
    deep: false,
    live: { running: true, uptime: "3m 2s", orchestrator: "idle", orchestrator_ok: true, herdr_connected: true, hooks: { Stop: "2026-11-26T12:00:00Z" }, last_errors: [{ time: "2026-11-26T12:00:00Z", level: "warn", message: "herdr is not answering" }] },
    ...overrides,
  };
}

export function herdrMissing(): SystemReport {
  const base = report();
  return {
    ...base,
    verdict: "fail",
    versions: { crowsnest: "v1.0.0", claude: "2.1.332" },
    checks: base.checks.map((check) =>
      check.id === "herdr.installed"
        ? { ...check, status: "fail", detail: "no herdr on PATH", fix: "Install herdr with Homebrew", command: "brew install herdr" }
        : check,
    ),
  };
}

export function onboarding(overrides: Partial<OnboardingView> = {}): OnboardingView {
  return {
    pending: true,
    report: report(),
    choose_engine: false,
    engine: "auto",
    dir: "/d/orchestrator",
    orchestrator: { state: "waiting", pane_id: "", detail: "Waiting for the first-run checklist" },
    bind: "127.0.0.1:2769",
    name: "quark",
    ...overrides,
  };
}

export function settingsPage(): SettingsPage {
  return {
    name: "quark",
    bind: "127.0.0.1:2769",
    bind_command: "crowsnest --bind tailnet",
    orchestrator_engine: "claude",
    settings: [
      { name: "model", label: "Orchestrator model", section: "Agents", help: "sonnet, opus or haiku", kind: "text", apply: "orchestrator", value: "sonnet", is_set: false, default: "sonnet", source: "default", where: "default" },
      { name: "poke-interval", label: "Poke interval", section: "Orchestrator upkeep", help: "how often", kind: "duration", apply: "live", value: "7m", is_set: false, default: "15m", source: "env", where: "CROWSNEST_POKE_INTERVAL", locked: "Set by the environment (CROWSNEST_POKE_INTERVAL): change it there" },
      { name: "no-remote-control", label: "Remote Control", section: "Agents", help: "Remote Control", kind: "bool", invert: true, apply: "orchestrator", value: "false", is_set: false, default: "false", source: "default", where: "default" },
      { name: "notify-url", label: "Notify URL", section: "Notices", help: "write-only", kind: "secret", write_only: true, apply: "restart", value: "", is_set: true, default: "", source: "database", where: "database" },
    ],
  };
}
