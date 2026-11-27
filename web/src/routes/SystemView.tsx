// The System page (/system; account menu → System, and the palette): the doctor's checks as the
// running server sees them, with Re-check; versions; the agents and which one runs what; the
// running instance (uptime, orchestrator, hooks seen firing, recent errors); paths; settings
// not at their default and where they came from; and "Copy diagnostics", a redacted text report
// for pasting into an issue. Since R4 (day thirty): the update notice, Storage (with Download
// backup and Clean up now), Machines and Sign in with Tailscale (components/system/OpsCards.tsx).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ClipboardCopy, RefreshCw } from "lucide-react";
import { useState, type ReactNode } from "react";
import { CheckList, StatusMark } from "../components/system/CheckList";
import { MachineDetail } from "../components/system/MachineStats";
import { MachinesCard, StorageCard, TailscaleCard, UpdatedNotice } from "../components/system/OpsCards";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { Chip } from "../components/ui/Chip";
import { useToast } from "../components/ui/toast-context";
import { sourceLabels, systemEndpoints, systemKeys, type Check, type SystemReport } from "../lib/system";

const groups: { id: Check["group"]; title: string }[] = [
  { id: "herdr", title: "herdr" },
  { id: "agents", title: "Agents" },
  { id: "claude", title: "Claude Code" },
  { id: "machine", title: "Machine" },
  { id: "optional", title: "Optional features" },
  { id: "config", title: "Configuration" },
];

const verdictText = { ok: "Ready", warn: "Working, with warnings", fail: "Something needs fixing" } as const;

export function SystemView() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const report = useQuery({ queryKey: systemKeys.system, queryFn: () => systemEndpoints.system(), refetchInterval: 30_000 });
  const recheck = useMutation({
    mutationFn: () => systemEndpoints.system(true),
    onSuccess: (data) => queryClient.setQueryData(systemKeys.system, data),
  });
  const [copying, setCopying] = useState(false);

  const copyDiagnostics = () => {
    setCopying(true);
    systemEndpoints
      .diagnostics()
      .then((text) => navigator.clipboard.writeText(text))
      .then(() => toast.show({ tone: "success", title: "Diagnostics copied", description: "Redacted: no secrets or tokens, your home folder as ~." }))
      .catch((error: unknown) => toast.show({ tone: "danger", title: "Could not copy the diagnostics", description: (error as Error).message }))
      .finally(() => setCopying(false));
  };

  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-4 p-3 md:p-5">
      <header className="flex flex-wrap items-center gap-2">
        <h1 className="mr-auto text-base font-semibold text-heading">System</h1>
        <Button icon={RefreshCw} onClick={() => recheck.mutate()} disabled={recheck.isPending}>
          {recheck.isPending ? "Checking…" : "Re-check"}
        </Button>
        <Button icon={ClipboardCopy} onClick={copyDiagnostics} disabled={copying}>
          Copy diagnostics
        </Button>
      </header>
      <UpdatedNotice />
      {report.isPending && <p className="text-xs text-muted">Running the checks…</p>}
      {report.isError && <p className="text-xs text-danger">Could not load the system report. {(report.error as Error).message}</p>}
      {report.data && <SystemBody report={report.data} />}
    </div>
  );
}

function SystemBody({ report }: { report: SystemReport }) {
  const live = report.live;
  return (
    <>
      <Card aria-label="Verdict">
        <div className="flex items-start gap-3">
          <StatusMark status={report.verdict} className="mt-0.5 h-6 w-6" />
          <div className="min-w-0">
            <p className="text-sm font-semibold text-heading">{verdictText[report.verdict]}</p>
            <p className="break-words font-mono text-2xs text-muted">{report.summary}</p>
            <p className="mt-1 text-2xs text-muted">Checked {new Date(report.checked_at).toLocaleTimeString()}. The same list as `crowsnest doctor` on the machine.</p>
          </div>
        </div>
      </Card>
      <div className="grid gap-4 md:grid-cols-2">
        <Card title="Versions">
          <Facts
            rows={[
              ["crowsnest", report.versions.crowsnest],
              ["herdr", report.versions.herdr ?? "not found"],
              ["Claude Code", report.versions.claude ?? "not found"],
              ["Codex", report.versions.codex ?? "not found"],
            ]}
          />
        </Card>
        <Card title="Agents">
          <ul className="flex flex-col gap-2">
            {report.agents.map((agent) => (
              <li key={agent.name} className="flex flex-wrap items-center gap-2 text-xs">
                <span className="w-14 font-medium text-heading">{agent.name}</span>
                {agent.installed ? <Chip tone={agent.usable ? "success" : "warning"}>{agent.signed_in === false ? "not signed in" : agent.signed_in ? "signed in" : "installed"}</Chip> : <Chip>not installed</Chip>}
                {report.selection.orchestrator === agent.name && <Chip tone="accent">orchestrator</Chip>}
                {report.selection.jobs === agent.name && <Chip tone="accent">jobs</Chip>}
                {agent.auth_method && <span className="text-muted">{agent.auth_method}</span>}
              </li>
            ))}
          </ul>
          <p className="mt-2 text-2xs text-muted">
            Engine setting <code>{report.selection.setting}</code>: {report.selection.reason}.
          </p>
        </Card>
        {live && (
          <Card title="Running">
            <Facts
              rows={[
                ["Uptime", live.uptime ?? "-"],
                ["Orchestrator", (live.orchestrator ?? "-") + (live.detail ? ` (${live.detail})` : "")],
                ["herdr", live.herdr_connected ? "connected" : "not connected"],
                ["Hooks seen", live.hooks && Object.keys(live.hooks).length > 0 ? Object.keys(live.hooks).sort().join(", ") : "none yet"],
              ]}
            />
          </Card>
        )}
        <Card title="Paths">
          <Facts
            mono
            rows={[
              ["Data", report.paths.data],
              ["State", report.paths.state],
              ["Database", report.paths.database],
              ["Orchestrator", report.paths.orchestrator],
              ["Agent socket", report.paths.agent_socket],
            ]}
          />
        </Card>
      </div>
      <MachineDetail />
      <StorageCard />
      <MachinesCard />
      <TailscaleCard />
      {live?.last_errors && live.last_errors.length > 0 && (
        <Card title="Recent warnings and errors">
          <ul className="flex flex-col gap-1">
            {[...live.last_errors].reverse().map((line, index) => (
              <li key={index} className="flex gap-2 text-2xs">
                <span className="shrink-0 font-mono text-muted">{new Date(line.time).toLocaleTimeString()}</span>
                <Chip tone={line.level === "error" ? "danger" : "warning"}>{line.level}</Chip>
                <span className="min-w-0 break-words text-body">{line.message}</span>
              </li>
            ))}
          </ul>
        </Card>
      )}
      {groups.map((group) => {
        const checks = report.checks.filter((check) => check.group === group.id);
        if (checks.length === 0) return null;
        return (
          <Card key={group.id} title={group.title}>
            <CheckList checks={checks} />
          </Card>
        );
      })}
      {report.settings && report.settings.length > 0 && (
        <Card title="Settings not at their default">
          <ul className="flex flex-col gap-1">
            {report.settings.map((setting) => (
              <li key={setting.name} className="flex flex-wrap items-center gap-2 text-xs">
                <code className="text-heading">{setting.name}</code>
                <span className="font-mono text-body">{setting.value}</span>
                <Chip>{sourceLabels[setting.source]}</Chip>
                <span className="font-mono text-2xs text-muted">{setting.where}</span>
              </li>
            ))}
          </ul>
        </Card>
      )}
    </>
  );
}

function Facts({ rows, mono }: { rows: [string, ReactNode][]; mono?: boolean }) {
  return (
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
      {rows.map(([label, value]) => (
        <div key={label} className="contents">
          <dt className="text-muted">{label}</dt>
          <dd className={mono ? "break-all font-mono text-2xs text-body" : "break-words text-body"}>{value}</dd>
        </div>
      ))}
    </dl>
  );
}
