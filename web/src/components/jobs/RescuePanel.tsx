// RescuePanel: getting a stuck agent moving without a terminal (R4 Fable 3.5/3.2, Codex P4). The
// things Jeff types into a 44-column phone terminal to un-stick an agent are buttons here, each
// explained in a few words: interrupt (Esc), continue, nudge, get it moving (retry now, restart a
// gone agent), restart on the same conversation, compact now, switch model, and the dialog's own
// numbered options when one is up, with the last screen as text to read first. The terminal stays
// one tap away for everything else.
//
// Everything goes through routes that already exist: Esc through POST /agents/{pane}/keys (an
// interrupt needs no screen hash), "/compact" and "/model …" typed as a plain prompt into the
// job's pane (only while the agent is idle, because Claude queues input typed during a turn and a
// queued slash command would arrive as text), and the job's own nudge, resume, message and
// restart-agent routes.
import { useNavigate } from "@tanstack/react-router";
import { ChevronDown, ChevronRight, Cpu, LifeBuoy, Minimize2, OctagonX, Play, RotateCcw, SquareTerminal, StepForward, Zap } from "lucide-react";
import { useState, type ReactNode } from "react";
import { ApiError, endpoints } from "../../lib/api";
import { cn } from "../../lib/cn";
import type { Job } from "../../lib/jobs";

/** What the panel needs of a job: a board card's job will do, not only the full detail. */
type RescueJob = Pick<Job, "id" | "pane_id" | "agent_name" | "stuck"> & Partial<Pick<Job, "cause" | "accept_state">>;

/**
 * What the job's cause (main's acceptance branch, DESIGN.md 6.15 to 6.19) means for rescue, in a
 * line, and whether pushing the agent can help at all: over its budget or held by the fleet's usage
 * limit, a nudge is refused or wasted; spinning wants an interrupt and a different instruction.
 */
function causeAdvice(job: RescueJob): { text: string; push: boolean } | null {
  switch (job.cause) {
    case "budget":
      return { text: "It reached its budget: raise it (above) to let it go on; a nudge or Continue would be refused.", push: false };
    case "limit":
      return { text: "Held by the usage limit: crowsnest resumes it at the reset by itself; Get it moving tries now.", push: false };
    case "spinning":
      return { text: "It is repeating itself: Interrupt, then send it a different instruction (the message box), or Restart.", push: true };
    default:
      return null;
  }
}
import { useAgentScreen, useFleet, useJobAction, useJobMessage, useRestartWithInstructions, useSendKeys } from "../../lib/queries";
import { DialogCard, ScreenText } from "../fleet/DialogCard";
import { useToast } from "../ui/toast-context";
import { HelpButton } from "../help/HelpButton";

/** The models "Switch model" offers; Claude Code's /model takes these aliases. */
const MODELS = ["sonnet", "opus", "haiku"] as const;

/** What "Continue" types: a plain push to carry on, with the job protocol's reminder. */
const CONTINUE_TEXT = "Continue where you left off. If you cannot, say why with job_update, or ask with the ask tool.";

interface RescuePanelProps {
  job: RescueJob;
  /** Open by default (a stuck job, or one that needs the human). */
  defaultOpen?: boolean;
}

export function RescuePanel({ job, defaultOpen }: RescuePanelProps) {
  const [open, setOpen] = useState(!!defaultOpen);
  const navigate = useNavigate();
  return (
    <section aria-label="Rescue" className="rounded-md border border-border" data-testid="rescue-panel">
      <div className="flex items-center pr-1">
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen((current) => !current)}
          className="flex min-h-11 min-w-0 flex-1 items-center gap-2 px-3 text-left text-sm font-medium text-heading hover:bg-hover md:min-h-9"
        >
          <LifeBuoy className="h-4 w-4 text-accent" aria-hidden />
          <span className="flex-1">Rescue: get the agent moving without a terminal</span>
          {open ? <ChevronDown className="h-4 w-4" aria-hidden /> : <ChevronRight className="h-4 w-4" aria-hidden />}
        </button>
        <HelpButton topic="how-to/rescue#the-rescue-panel" what="the rescue buttons" inline={false} />
      </div>
      {open && <RescueBody job={job} openTerminal={() => void navigate({ to: "/fleet", search: { agent: job.pane_id, view: "terminal" } })} />}
    </section>
  );
}

function RescueBody({ job, openTerminal }: { job: RescueJob; openTerminal: () => void }) {
  const toast = useToast();
  const fleet = useFleet();
  const screen = useAgentScreen(job.pane_id);
  const keys = useSendKeys(job.pane_id);
  const action = useJobAction(job.id);
  const message = useJobMessage(job.id);
  const restart = useRestartWithInstructions();
  const [busy, setBusy] = useState<string | null>(null);
  const [confirmRestart, setConfirmRestart] = useState(false);
  const [model, setModel] = useState<(typeof MODELS)[number]>("sonnet");
  const status = fleet.data?.agents.find((agent) => agent.pane_id === job.pane_id)?.status;
  const idle = status === "idle" || status === "done";

  const run = (name: string, work: () => Promise<unknown>, done: string) => {
    setBusy(name);
    work().then(
      () => toast.show({ tone: "success", title: done, description: `${job.id} · ${job.agent_name}` }),
      (failure: unknown) =>
        toast.show({ tone: "danger", title: `${name}: not done`, description: failure instanceof ApiError || failure instanceof Error ? failure.message : String(failure) }),
    ).finally(() => setBusy(null));
  };
  const typeCommand = (name: string, text: string, done: string) => run(name, () => endpoints.prompt(job.pane_id, { text }), done);
  const dialog = screen.data?.dialog;
  const advice = causeAdvice(job);
  const canPush = advice?.push ?? true;

  return (
    <div className="flex flex-col gap-3 border-t border-border p-3">
      {dialog?.present && screen.data && (
        <DialogCard target={job.pane_id} agentName={job.agent_name} screen={screen.data} onOpenTerminal={openTerminal} />
      )}
      {advice && (
        <p className="text-xs font-medium text-warning" data-testid="rescue-advice">
          {advice.text}
        </p>
      )}
      <ul className="grid grid-cols-1 gap-1.5 sm:grid-cols-2" aria-label="Rescue actions">
        <Action icon={OctagonX} label="Interrupt (Esc)" hint="Stops the current turn; the agent then waits for you." disabled={!!busy} onPress={() => run("Interrupt", () => keys.mutateAsync({ keys: ["esc"], screen_hash: "" }), "Sent Esc")} />
        <Action icon={StepForward} label="Continue" hint="Tells it to carry on where it left off." disabled={!!busy || !canPush} onPress={() => run("Continue", () => message.mutateAsync({ text: CONTINUE_TEXT }), "Told it to continue")} />
        <Action icon={Zap} label="Nudge" hint="Reminds it of the job and asks for a status line." disabled={!!busy || !canPush} onPress={() => run("Nudge", () => action.mutateAsync("nudge"), "Nudged")} />
        {job.stuck && (
          <Action icon={Play} label="Get it moving" hint="Retry now after a limit, restart a gone agent, else nudge." disabled={!!busy} onPress={() => run("Get it moving", () => action.mutateAsync("resume"), "Moving it")} />
        )}
        {confirmRestart ? (
          <li className="flex flex-col gap-1 rounded-md border border-warning/60 p-2 sm:col-span-2">
            <span className="text-xs text-heading">Restart {job.agent_name}'s Claude on the same conversation? A turn in progress is lost; the conversation is kept.</span>
            <span className="flex gap-1.5">
              <SmallButton onPress={() => { setConfirmRestart(false); run("Restart", () => restart.mutateAsync({ kind: "job", id: job.id }), "Restarting on the same conversation"); }}>Yes, restart</SmallButton>
              <SmallButton onPress={() => setConfirmRestart(false)}>Cancel</SmallButton>
            </span>
          </li>
        ) : (
          <Action icon={RotateCcw} label="Restart (same conversation)" hint="Starts its Claude again with --resume: for a hung or crashed agent." disabled={!!busy} onPress={() => setConfirmRestart(true)} />
        )}
        <Action
          icon={Minimize2}
          label="Compact now"
          hint={idle ? "Types /compact: shrinks its context so it can keep going." : "Only while it is idle (a slash command typed mid-turn arrives as text)."}
          disabled={!!busy || !idle}
          onPress={() => typeCommand("Compact now", "/compact", "Typed /compact")}
        />
        <li className="flex flex-col gap-1 rounded-md border border-border-strong bg-surface p-2">
          <span className="flex items-center gap-1.5 text-sm font-medium text-heading">
            <Cpu className="h-4 w-4 text-muted" aria-hidden />
            Switch model
          </span>
          <span className="text-2xs text-muted">For the rest of this conversation (a restart goes back to the job's model). Only while idle.</span>
          <span className="flex gap-1.5">
            <select
              aria-label="Model"
              value={model}
              onChange={(event) => setModel(event.target.value as (typeof MODELS)[number])}
              className="min-h-11 flex-1 rounded border border-border-strong bg-surface px-2 text-sm text-heading md:min-h-8 md:text-xs"
            >
              {MODELS.map((name) => (
                <option key={name} value={name}>
                  {name}
                </option>
              ))}
            </select>
            <SmallButton disabled={!!busy || !idle} onPress={() => typeCommand("Switch model", `/model ${model}`, `Switched to ${model}`)}>
              Switch
            </SmallButton>
          </span>
        </li>
        <Action icon={SquareTerminal} label="Open the terminal" hint="For anything else: the agent's own pane." onPress={openTerminal} />
      </ul>
      {screen.data && !dialog?.present && (
        <details className="text-xs">
          <summary className="flex min-h-11 cursor-pointer items-center text-muted md:min-h-7">The last screen, as text</summary>
          <ScreenText text={screen.data.text} />
        </details>
      )}
      <p className="text-2xs text-muted">
        Agent: {job.agent_name || "none"} · {status ?? "state unknown"}. To send a sentence, use the message box below.
      </p>
    </div>
  );
}

function Action({ icon: Icon, label, hint, disabled, onPress }: { icon: typeof Zap; label: string; hint: string; disabled?: boolean; onPress: () => void }) {
  return (
    <li>
      <button
        type="button"
        onClick={onPress}
        disabled={disabled}
        className="flex min-h-12 w-full items-start gap-2 rounded-md border border-border-strong bg-surface px-2.5 py-2 text-left hover:border-accent hover:bg-hover disabled:opacity-50 md:min-h-10"
      >
        <Icon className="mt-0.5 h-4 w-4 shrink-0 text-muted" aria-hidden />
        <span className="min-w-0">
          <span className="block text-sm font-medium text-heading">{label}</span>
          <span className="block text-2xs text-muted">{hint}</span>
        </span>
      </button>
    </li>
  );
}

function SmallButton({ children, onPress, disabled }: { children: ReactNode; onPress: () => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      onClick={onPress}
      disabled={disabled}
      className={cn("min-h-11 rounded border border-border-strong bg-surface px-3 text-sm text-heading hover:bg-hover disabled:opacity-50 md:min-h-8 md:text-xs")}
    >
      {children}
    </button>
  );
}
