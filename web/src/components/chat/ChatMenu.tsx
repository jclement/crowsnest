// ChatMenu: the Chat view's menu: the model the orchestrator runs on and its context size (tokens
// and a bar against the compaction threshold, DESIGN.md 7.9), Poke now (the result comes back as
// a toast: sent, or skipped and why), Compact now, the Journal and the Schedule, show or hide
// thinking, Restart (same conversation, --resume), and New conversation (fresh session). Both
// relaunches ask for confirmation first, in a Sheet, because they interrupt whatever the
// orchestrator is doing.
import { BellRing, Brain, CalendarClock, Cpu, Minimize2, MessageSquarePlus, MoreVertical, NotebookText, RotateCw } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { relaunchConfirmations, type RelaunchAction } from "../../lib/conversation";
import { useOrchestratorAction } from "../../lib/conversation-queries";
import { pokeToast } from "../../lib/journal";
import { useCompactNow, useOrchestratorContext, usePokeNow } from "../../lib/queries";
import { contextLine, contextPercent, type ContextInfo } from "../../lib/schedule";
import { JournalSheet } from "../journal/JournalPanel";
import { ScheduleSheet } from "../schedule/SchedulePanel";
import { Button, IconButton } from "../ui/Button";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";

type Action = RelaunchAction;
const confirmations = relaunchConfirmations;

interface ChatMenuProps {
  /** The orchestrator's model; empty or missing means Claude's own default. */
  model?: string;
  showThinking: boolean;
  onToggleThinking: () => void;
  /** Restart and New conversation are offered only when there is an orchestrator to relaunch. */
  canRelaunch: boolean;
}

export function ChatMenu({ model, showThinking, onToggleThinking, canRelaunch }: ChatMenuProps) {
  const [open, setOpen] = useState(false);
  const [confirming, setConfirming] = useState<Action | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const [journalOpen, setJournalOpen] = useState(false);
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const action = useOrchestratorAction();
  const poke = usePokeNow();
  const compact = useCompactNow();
  const context = useOrchestratorContext(open && canRelaunch);
  const toast = useToast();

  useEffect(() => {
    if (!open) return;
    const onPointer = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => event.key === "Escape" && setOpen(false);
    document.addEventListener("pointerdown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const run = (which: Action) => {
    action.mutate(which, {
      onSuccess: () => {
        setConfirming(null);
        toast.show({ tone: "success", title: which === "restart" ? "Orchestrator restarted" : "New conversation started" });
      },
      onError: (error) => toast.show({ tone: "danger", title: "That did not work", description: error.message }),
    });
  };

  const pokeNow = () => {
    poke.mutate(undefined, {
      onSuccess: (result) => toast.show(pokeToast(result)),
      onError: (error) => toast.show({ tone: "danger", title: "Could not poke", description: error.message }),
    });
  };

  const compactNow = () => {
    compact
      .mutateAsync()
      .then(() => toast.show({ tone: "success", title: "Compacting the orchestrator" }))
      .catch((error: Error) => toast.show({ tone: "warning", title: "Not compacted", description: error.message }));
  };

  const pick = (fn: () => void) => () => {
    setOpen(false);
    fn();
  };

  return (
    <div ref={rootRef} className="relative">
      <IconButton icon={MoreVertical} label="Conversation menu" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(!open)} />
      {open && (
        <div role="menu" className="absolute right-0 top-full z-30 mt-1 w-64 overflow-hidden rounded-md border border-border bg-surface py-1 text-body shadow-xl">
          <div className="flex items-center gap-2 border-b border-border px-3 pb-1.5 pt-1 text-xs" data-testid="orchestrator-model" title="Set with --model or CROWSNEST_MODEL">
            <Cpu className="h-4 w-4 text-muted" aria-hidden />
            <span className="text-muted">Model</span>
            <span className="ml-auto font-mono text-heading">{model || "Claude's default"}</span>
          </div>
          {canRelaunch && context.data && <ContextUsage info={context.data} />}
          {canRelaunch && (
            <MenuItem icon={<Minimize2 className="h-4 w-4 text-muted" aria-hidden />} onClick={pick(compactNow)} disabled={compact.isPending}>
              {compact.isPending ? "Compacting…" : "Compact now"}
            </MenuItem>
          )}
          {canRelaunch && (
            <MenuItem icon={<BellRing className="h-4 w-4 text-muted" aria-hidden />} onClick={pick(pokeNow)} disabled={poke.isPending}>
              {poke.isPending ? "Poking…" : "Poke now"}
            </MenuItem>
          )}
          <MenuItem icon={<NotebookText className="h-4 w-4 text-muted" aria-hidden />} onClick={pick(() => setJournalOpen(true))}>
            Journal
          </MenuItem>
          <MenuItem icon={<CalendarClock className="h-4 w-4 text-muted" aria-hidden />} onClick={pick(() => setScheduleOpen(true))}>
            Schedule
          </MenuItem>
          <MenuItem icon={<Brain className="h-4 w-4 text-muted" aria-hidden />} onClick={pick(onToggleThinking)}>
            {showThinking ? "Hide thinking" : "Show thinking"}
          </MenuItem>
          {canRelaunch && (
            <>
              <MenuItem icon={<RotateCw className="h-4 w-4 text-muted" aria-hidden />} onClick={pick(() => setConfirming("restart"))}>
                Restart
              </MenuItem>
              <MenuItem icon={<MessageSquarePlus className="h-4 w-4 text-muted" aria-hidden />} onClick={pick(() => setConfirming("new"))}>
                New conversation
              </MenuItem>
            </>
          )}
        </div>
      )}
      <JournalSheet open={journalOpen} onClose={() => setJournalOpen(false)} />
      <ScheduleSheet open={scheduleOpen} onClose={() => setScheduleOpen(false)} />
      <Sheet open={confirming !== null} onClose={() => setConfirming(null)} title={confirming ? confirmations[confirming].title : ""} size="md">
        {confirming && (
          <div className="flex flex-col gap-4 p-4">
            <p className="text-sm text-body">{confirmations[confirming].body}</p>
            <div className="flex justify-end gap-2">
              <Button variant="ghost" onClick={() => setConfirming(null)}>
                Cancel
              </Button>
              <Button variant="danger" onClick={() => run(confirming)} disabled={action.isPending}>
                {action.isPending ? "Working…" : confirmations[confirming].button}
              </Button>
            </div>
          </div>
        )}
      </Sheet>
    </div>
  );
}

function MenuItem({ icon, onClick, disabled, children }: { icon: ReactNode; onClick: () => void; disabled?: boolean; children: ReactNode }) {
  return (
    <button
      type="button"
      role="menuitem"
      onClick={onClick}
      disabled={disabled}
      className="flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover disabled:opacity-60 md:min-h-9"
    >
      {icon}
      {children}
    </button>
  );
}

/** The orchestrator's context size: a line of text and a bar, amber past 80 % of the threshold. */
export function ContextUsage({ info }: { info: ContextInfo }) {
  const percent = contextPercent(info);
  return (
    <div className="border-b border-border px-3 pb-2 pt-1" aria-label="Context usage">
      <div className="text-xs text-muted">Context: {contextLine(info)}</div>
      {percent !== null && (
        <div
          role="meter"
          aria-label="Context used"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={percent}
          className="mt-1 h-1.5 overflow-hidden rounded-full bg-surface-sunken"
        >
          <div className={`h-full ${percent >= 80 ? "bg-warning" : "bg-accent"}`} style={{ width: `${percent}%` }} />
        </div>
      )}
    </div>
  );
}
