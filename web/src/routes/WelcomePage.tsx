// The guided first run (/welcome, DESIGN.md 4.14.1). Setup's password step lands here: a short
// checklist driven by the doctor's checks (herdr, the agents, the orchestrator's folder), most of
// it already green; a choice of agent when both Claude Code and Codex are there; optional steps
// folded away; then Start. The orchestrator starts, and if Claude stops at its one-time folder
// trust question the question is answered right here, as a card with its buttons. Once the
// orchestrator is up the browser goes to Chat, where its first message waits.
//
// Missing pieces are explained with the exact command for this machine and re-checked by
// themselves; Claude's sign-in needs a terminal, so the checklist opens one in the app with the
// command typed in.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ChevronRight, Loader2, RefreshCw, Rocket, SquareTerminal } from "lucide-react";
import { useEffect, useState } from "react";
import { DialogCard } from "../components/fleet/DialogCard";
import { Footer } from "../components/Footer";
import { NotificationSettings } from "../components/push/NotificationSettings";
import { CheckRow, CommandLine, StatusMark } from "../components/system/CheckList";
import { TerminalPanel } from "../components/terminal/TerminalPanel";
import { ThemeToggle } from "../components/ThemeToggle";
import { Button } from "../components/ui/Button";
import { Sheet } from "../components/ui/Sheet";
import { PublishCard } from "../components/system/PublishCard";
import { ApiError, endpoints, queryKeys, terminalPaths, type Me } from "../lib/api";
import { findCheck, readyToStart, systemEndpoints, systemKeys, type Check, type OnboardingView } from "../lib/system";

/** How often the checklist re-checks the machine while something is missing. */
const RECHECK_MS = 8_000;
/** How often it follows the orchestrator after Start. */
const FOLLOW_MS = 1_500;

const startedStates = new Set(["idle", "working"]);
const dialogStates = new Set(["needs_terminal", "blocked"]);

export function WelcomePage() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [started, setStarted] = useState(false);
  const onboarding = useQuery({
    queryKey: systemKeys.onboarding,
    queryFn: () => systemEndpoints.onboarding(true),
    refetchInterval: (query) => {
      const data = query.state.data;
      if (started) return FOLLOW_MS;
      return data && readyToStart(data.report) ? false : RECHECK_MS;
    },
  });
  const state = onboarding.data?.orchestrator.state ?? "";

  useEffect(() => {
    if (started && startedStates.has(state)) void navigate({ to: "/chat" });
  }, [started, state, navigate]);

  // Someone who finished the checklist elsewhere (or reloads after Start) goes on to the app.
  useEffect(() => {
    if (onboarding.data && !onboarding.data.pending && !started) {
      queryClient.setQueryData<Me>(queryKeys.me, (me) => (me ? { ...me, features: { ...me.features, onboarding: false } } : me));
      void navigate({ to: "/" });
    }
  }, [onboarding.data, started, navigate, queryClient]);

  return (
    <div className="flex min-h-full flex-col">
      <header className="flex h-11 shrink-0 items-center gap-2 bg-chrome pl-3 pr-1 text-chrome-fg md:h-10">
        <img src="/icons/crowsnest.svg" alt="" className="h-5 w-5" />
        <span className="text-sm font-semibold">crowsnest</span>
        <div className="ml-auto">
          <ThemeToggle />
        </div>
      </header>
      <main className="flex flex-1 justify-center p-4 md:p-8">
        <div className="flex w-full max-w-xl flex-col gap-4">
          {onboarding.isPending && <p className="text-xs text-muted">Checking this machine…</p>}
          {onboarding.isError && <p className="text-xs text-danger">Could not load the checklist. {(onboarding.error as Error).message}</p>}
          {onboarding.data &&
            (started ? (
              <Starting view={onboarding.data} />
            ) : (
              <Checklist
                view={onboarding.data}
                rechecking={onboarding.isFetching}
                onRecheck={() => void queryClient.invalidateQueries({ queryKey: systemKeys.onboarding })}
                onStarted={(view) => {
                  queryClient.setQueryData(systemKeys.onboarding, view);
                  // The app redirects here while /auth/me says the checklist is unfinished.
                  queryClient.setQueryData<Me>(queryKeys.me, (me) => (me ? { ...me, features: { ...me.features, onboarding: false } } : me));
                  setStarted(true);
                }}
              />
            ))}
        </div>
      </main>
      <Footer className="px-3 py-2 text-center" />
    </div>
  );
}

interface ChecklistProps {
  view: OnboardingView;
  rechecking: boolean;
  onRecheck: () => void;
  onStarted: (view: OnboardingView) => void;
}

export function Checklist({ view, rechecking, onRecheck, onStarted }: ChecklistProps) {
  const report = view.report;
  const [engine, setEngine] = useState<"claude" | "codex">(report.selection.orchestrator === "codex" ? "codex" : "claude");
  const [terminalPane, setTerminalPane] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const shell = useMutation({ mutationFn: (command: string) => systemEndpoints.openShell(command) });
  const start = useMutation({ mutationFn: () => systemEndpoints.startOnboarding(view.choose_engine ? engine : undefined) });
  const ready = readyToStart(report);

  const openTerminal = (command: string) => {
    setError(null);
    shell
      .mutateAsync(command)
      .then((result) => setTerminalPane(result.pane_id))
      .catch((caught: unknown) => setError(caught instanceof ApiError ? caught.message : "Could not open a terminal."));
  };
  const begin = () => {
    setError(null);
    start
      .mutateAsync()
      .then(onStarted)
      .catch((caught: unknown) => setError(caught instanceof ApiError ? caught.message : "Could not start."));
  };

  const rows: Check[] = ["herdr.installed", "herdr.session", "agent.claude", "agent.codex"].map((id) => findCheck(report, id)).filter((check): check is Check => check !== undefined);
  const trust = findCheck(report, "claude.trust");
  const herdrRunning = findCheck(report, "herdr.installed")?.status !== "fail";

  return (
    <>
      <div>
        <h1 className="text-base font-semibold text-heading">Almost there</h1>
        <p className="mt-1 text-xs text-muted">
          Crowsnest checked this machine. {ready ? "Everything it needs is here: press Start." : "Something is missing; the steps below say how to add it, and this page notices when it is done."}
        </p>
      </div>
      <section aria-label="Checks" className="rounded-md border border-border bg-surface px-3">
        <ul className="divide-y divide-border">
          {rows.map((check) => (
            <CheckRow
              key={check.id}
              check={check}
              action={
                check.command && (check.command === "claude" || check.command === "codex login") && herdrRunning ? (
                  <Button compact icon={SquareTerminal} onClick={() => openTerminal(check.command ?? "")} disabled={shell.isPending}>
                    Open a terminal to sign in
                  </Button>
                ) : undefined
              }
            />
          ))}
        </ul>
      </section>
      {view.choose_engine && (
        <fieldset className="flex flex-col gap-2 rounded-md border border-border bg-surface p-3">
          <legend className="px-1 text-xs font-medium text-heading">Which agent runs the orchestrator?</legend>
          {(["claude", "codex"] as const).map((name) => (
            <label key={name} className="flex min-h-11 cursor-pointer items-start gap-2 text-sm md:min-h-8">
              <input type="radio" name="engine" value={name} checked={engine === name} onChange={() => setEngine(name)} className="mt-1" />
              <span>
                <span className="font-medium text-heading">{name === "claude" ? "Claude Code" : "Codex"}</span>
                <span className="block text-2xs text-muted">
                  {name === "claude" ? "Recommended: everything works, including the stop-hook guarantee, approvals and voice." : "Works, with gaps: no stop-hook guarantee, no structured approvals, no voice replies."}
                </span>
              </span>
            </label>
          ))}
        </fieldset>
      )}
      {(report.selection.orchestrator === "codex" || (view.choose_engine && engine === "codex")) && view.codex_gaps && (
        <section className="rounded-md border border-warning/40 bg-warning/5 p-3 text-xs">
          <p className="font-medium text-heading">What Codex mode lacks</p>
          <ul className="mt-1 list-disc pl-4 text-body">
            {view.codex_gaps.map((gap) => (
              <li key={gap}>{gap}</li>
            ))}
          </ul>
        </section>
      )}
      <section className="flex gap-2.5 rounded-md border border-border bg-surface p-3">
        <StatusMark status={trust?.status ?? "info"} className="mt-0.5" />
        <div className="min-w-0 text-xs">
          <p className="text-sm font-medium text-heading">The orchestrator's folder</p>
          <p className="break-all font-mono text-2xs text-muted">{view.dir}</p>
          <p className="mt-1 text-body">
            {trust?.status === "ok"
              ? "Claude already trusts it."
              : "The first time, Claude asks whether to trust this folder. The question appears on this page with its buttons; answer Yes."}
          </p>
        </div>
      </section>
      <OptionalSteps view={view} />
      {error && (
        <p role="alert" className="rounded border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger">
          {error}
        </p>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="primary" icon={Rocket} onClick={begin} disabled={!ready || start.isPending}>
          {start.isPending ? "Starting…" : "Start"}
        </Button>
        <Button icon={RefreshCw} onClick={onRecheck} disabled={rechecking}>
          {rechecking ? "Checking…" : "Re-check"}
        </Button>
        {!ready && <span className="text-2xs text-muted">Start needs herdr and one agent.</span>}
      </div>
      <Sheet
        open={terminalPane !== null}
        onClose={() => {
          setTerminalPane(null);
          onRecheck();
        }}
        title="Sign in"
      >
        {terminalPane && (
          <div className="flex flex-col gap-2 p-2">
            <p className="px-1 text-2xs text-muted">Follow the sign-in in this terminal. Close this sheet when it is done; the checklist checks again.</p>
            <TerminalPanel path={terminalPaths.pane(terminalPane)} label="setup terminal" fill="fixed" />
          </div>
        )}
      </Sheet>
    </>
  );
}

function OptionalSteps({ view }: { view: OnboardingView }) {
  const [notifications, setNotifications] = useState(false);
  const voice = findCheck(view.report, "optional.voice");
  const tailscale = findCheck(view.report, "tailscale.serve");
  return (
    <details className="group rounded-md border border-border bg-surface">
      <summary className="flex min-h-11 cursor-pointer list-none items-center gap-1 px-3 text-sm font-medium text-heading md:min-h-9">
        <ChevronRight className="h-4 w-4 text-muted transition-transform group-open:rotate-90" aria-hidden />
        Optional, any time later
      </summary>
      <ul className="flex flex-col gap-3 px-3 pb-3 text-xs">
        <li>
          <p className="font-medium text-heading">Name: {view.name}</p>
          <p className="text-muted">Shown in the header and notifications. Change it in the account menu → Account.</p>
        </li>
        <li>
          <p className="font-medium text-heading">Voice</p>
          <p className="text-muted">{voice?.detail ?? "Talk mode needs an ElevenLabs key."}</p>
          {voice?.fix && voice.status !== "ok" && <p className="text-body">{voice.fix}</p>}
        </li>
        <li>
          <p className="font-medium text-heading">Notifications on this device</p>
          <Button compact onClick={() => setNotifications(true)} className="mt-1">
            Set up notifications
          </Button>
        </li>
        <li>
          <p className="font-medium text-heading">Reach it from your phone</p>
          <PublishCard
            bare
            fallback={
              <>
                <p className="text-muted">{tailscale?.detail ?? view.bind}</p>
                {tailscale?.command && tailscale.status !== "ok" && <CommandLine command={tailscale.command} className="mt-1" />}
                {!tailscale && <CommandLine command="crowsnest --bind tailnet" className="mt-1" />}
              </>
            }
          />
        </li>
      </ul>
      <NotificationSettings open={notifications} onClose={() => setNotifications(false)} />
    </details>
  );
}

/** After Start: follows the orchestrator; a start-up question (folder trust) is answered here. */
function Starting({ view }: { view: OnboardingView }) {
  const navigate = useNavigate();
  const orchestrator = view.orchestrator;
  const showDialog = dialogStates.has(orchestrator.state) && orchestrator.pane_id !== "";
  const screen = useQuery({
    queryKey: ["welcome-screen", orchestrator.pane_id],
    queryFn: () => endpoints.agentScreen(orchestrator.pane_id),
    enabled: showDialog,
    refetchInterval: FOLLOW_MS,
  });
  return (
    <div className="flex flex-col gap-3" role="status">
      <div className="flex items-center gap-2">
        <Loader2 className="h-4 w-4 animate-spin text-accent" aria-hidden />
        <h1 className="text-base font-semibold text-heading">Starting the orchestrator…</h1>
      </div>
      <p className="text-xs text-muted">
        {orchestrator.detail || `It is ${orchestrator.state || "starting"}. This page opens Chat as soon as it is ready.`}
      </p>
      {showDialog && screen.data?.dialog?.present && (
        <>
          <p className="text-xs text-body">Claude is asking something before it starts. Answer it here:</p>
          <DialogCard
            target={orchestrator.pane_id}
            agentName="orchestrator"
            screen={screen.data}
            onOpenTerminal={() => void navigate({ to: "/fleet", search: { agent: orchestrator.pane_id, view: "terminal" } })}
          />
        </>
      )}
      {showDialog && screen.data && !screen.data.dialog?.present && (
        <Button icon={SquareTerminal} onClick={() => void navigate({ to: "/fleet", search: { agent: orchestrator.pane_id, view: "terminal" } })}>
          Open its terminal
        </Button>
      )}
    </div>
  );
}
