// ChatMenu: the Chat view's menu: Poke now (the result comes back as a toast: sent, or skipped
// and why), the Journal, show or hide thinking, Restart (same conversation, --resume), and New
// conversation (fresh session). Both relaunches ask for confirmation first, in a Sheet, because
// they interrupt whatever the orchestrator is doing.
import { BellRing, Brain, MessageSquarePlus, MoreVertical, NotebookText, RotateCw } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useOrchestratorAction } from "../../lib/conversation-queries";
import { pokeToast } from "../../lib/journal";
import { usePokeNow } from "../../lib/queries";
import { JournalSheet } from "../journal/JournalPanel";
import { Button, IconButton } from "../ui/Button";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";

type Action = "restart" | "new";

const confirmations: Record<Action, { title: string; body: string; button: string }> = {
  restart: {
    title: "Restart the orchestrator?",
    body: "Claude is stopped (anything it is doing is interrupted) and started again on the same conversation.",
    button: "Restart",
  },
  new: {
    title: "Start a new conversation?",
    body: "Claude is stopped and started with an empty conversation. The current one stays in its transcript file but leaves this view.",
    button: "New conversation",
  },
};

interface ChatMenuProps {
  showThinking: boolean;
  onToggleThinking: () => void;
  /** Restart and New conversation are offered only when there is an orchestrator to relaunch. */
  canRelaunch: boolean;
}

export function ChatMenu({ showThinking, onToggleThinking, canRelaunch }: ChatMenuProps) {
  const [open, setOpen] = useState(false);
  const [confirming, setConfirming] = useState<Action | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const [journalOpen, setJournalOpen] = useState(false);
  const action = useOrchestratorAction();
  const poke = usePokeNow();
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

  const pick = (fn: () => void) => () => {
    setOpen(false);
    fn();
  };

  return (
    <div ref={rootRef} className="relative">
      <IconButton icon={MoreVertical} label="Conversation menu" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(!open)} />
      {open && (
        <div role="menu" className="absolute right-0 top-full z-30 mt-1 w-56 overflow-hidden rounded-md border border-border bg-surface py-1 text-body shadow-xl">
          {canRelaunch && (
            <MenuItem icon={<BellRing className="h-4 w-4 text-muted" aria-hidden />} onClick={pick(pokeNow)} disabled={poke.isPending}>
              {poke.isPending ? "Poking…" : "Poke now"}
            </MenuItem>
          )}
          <MenuItem icon={<NotebookText className="h-4 w-4 text-muted" aria-hidden />} onClick={pick(() => setJournalOpen(true))}>
            Journal
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
