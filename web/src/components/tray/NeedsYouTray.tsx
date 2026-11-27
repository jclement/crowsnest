// The global needs-you tray (DESIGN.md 8.7, 22): what needs the human, answerable in place, on
// every view but Now and the Inbox (which show the same queue in full). Its rows are the server's
// attention queue (lib/attention.ts): pending approvals (Allow once / Deny on the card), open
// questions (a confirm or choice question's options), terminal dialogs (the digit buttons), stuck
// or failed jobs and standing agents that need the terminal, most urgent and longest waiting
// first, each labelled with its project and job, each with "Snooze 10 min" (kept on the server).
//
// Desktop (md and up): a stack anchored bottom-right, above whatever it must not cover (the Chat
// composer, the terminal's last rows: lib/trayKeepOut.ts sets --tray-bottom); it shows the top
// three and folds into a pill. Phone: a slim bar in the page's flow above the bottom nav (so it
// covers nothing) that opens a sheet. It replaces nothing in the Inbox, which stays the full list
// and the history, and reuses the Inbox's cards.
//
// Keyboard: g then n focuses the tray (expanding it); Tab moves through it; Enter opens an item,
// and an open approval takes focus so y and n answer it. Nothing else in the app answers to y or n.
import { isRoutine } from "../../lib/decision";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { BellDot, BellOff, Bot, ChevronDown, ChevronUp, Clock, ListTodo, MessageSquareWarning, MessagesSquare, ShieldAlert, ShieldCheck, ShieldQuestion } from "lucide-react";
import { useCallback, useEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { ApiError } from "../../lib/api";
import { cn } from "../../lib/cn";
import { DESKTOP_QUERY } from "../../lib/media";
import { relativeTime } from "../../lib/format";
import { FOCUS_TRAY_EVENT, trayTitle, type TrayItem } from "../../lib/needsYou";
import { itemLink, kindLabel, plainPreview, whereLine } from "../../lib/attention";
import { useSnooze, useTrayItems } from "./useTrayItems";
import { useAgentScreen, useAnswerQuestion } from "../../lib/queries";
import type { Question } from "../../lib/questions";
import { onArrival, setTraySound, traySoundOn } from "../../lib/traySound";
import { ApprovalCard } from "../approvals/ApprovalCard";
import { DialogCard } from "../fleet/DialogCard";
import { Button, IconButton } from "../ui/Button";
import { Chip } from "../ui/Chip";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";


/** How many items the desktop stack shows before "Show all". */
const STACK = 3;

/** Views that show the whole queue themselves, so the tray would only repeat it (R4 Fable 1.5), and the decision packet, whose answer bar it would crowd. */
const QUEUE_VIEWS = new Set(["/", "/inbox", "/decide"]);

/** True on a view that already shows the queue. */
function useOnQueueView(): boolean {
  return useRouterState({ select: (state) => QUEUE_VIEWS.has(state.location.pathname) });
}

/** Ids that arrived since the tray first rendered, for the arrival animation (cleared after it). */
function useArrivals(items: TrayItem[]): Set<string> {
  const known = useRef<Set<string> | null>(null);
  const [fresh, setFresh] = useState<Set<string>>(new Set());
  useEffect(() => {
    const ids = new Set(items.map((item) => item.id));
    if (known.current === null) {
      known.current = ids; // what is there on load is not "arriving"
      return;
    }
    const arrived = [...ids].filter((id) => !known.current?.has(id));
    known.current = ids;
    if (arrived.length === 0) return;
    setFresh(new Set(arrived));
    const timer = window.setTimeout(() => setFresh(new Set()), 700);
    return () => window.clearTimeout(timer);
  }, [items]);
  return fresh;
}

// ---- Desktop -----------------------------------------------------------------------------------

export function TrayPanel() {
  const items = useTrayItems();
  const onQueueView = useOnQueueView();
  const fresh = useArrivals(items);
  const [folded, setFolded] = useState(() => sessionStorage.getItem("crowsnest-tray-folded") === "1");
  const [all, setAll] = useState(false);
  const [sound, setSound] = useState(traySoundOn);
  const [bump, setBump] = useState(false);
  const panelRef = useRef<HTMLElement>(null);

  const fold = (value: boolean) => {
    setFolded(value);
    try {
      sessionStorage.setItem("crowsnest-tray-folded", value ? "1" : "0");
    } catch {
      // ignore
    }
  };

  useEffect(
    () =>
      onArrival(() => {
        setBump(true);
        window.setTimeout(() => setBump(false), 700);
      }),
    [],
  );

  useEffect(() => {
    const focus = () => {
      fold(false);
      window.setTimeout(() => panelRef.current?.querySelector<HTMLElement>("[data-tray-row]")?.focus(), 0);
    };
    window.addEventListener(FOCUS_TRAY_EVENT, focus);
    return () => window.removeEventListener(FOCUS_TRAY_EVENT, focus);
  }, []);

  if (items.length === 0 || onQueueView) return null;
  const shown = all ? items : items.slice(0, STACK);
  const style = { bottom: "var(--tray-bottom, 1rem)" };

  if (folded) {
    return (
      <button
        type="button"
        data-tray="pill"
        onClick={() => fold(false)}
        style={style}
        className={cn(
          "fixed right-4 z-30 hidden items-center gap-2 rounded-full border border-border-strong bg-surface-raised px-3 py-1.5 text-sm font-medium text-heading shadow-lg hover:bg-hover md:inline-flex",
          bump && "animate-tray-in",
        )}
      >
        <TrayCount count={items.length} />
        Needs you<span className="sr-only">: show the tray</span>
        <ChevronUp className="h-4 w-4 text-muted" aria-hidden />
      </button>
    );
  }

  return (
    <aside
      ref={panelRef}
      aria-label="Needs you"
      data-tray="panel"
      style={style}
      className={cn(
        "fixed right-4 z-30 hidden w-[24rem] max-w-[calc(100vw-14rem)] flex-col overflow-hidden rounded-lg border border-border-strong bg-surface shadow-xl md:flex",
        bump && "animate-tray-in",
      )}
    >
      <header className="flex items-center gap-2 border-b border-border bg-surface-raised px-3 py-1.5">
        <TrayCount count={items.length} />
        <h2 className="text-sm font-semibold text-heading">Needs you</h2>
        <span className="ml-auto flex items-center">
          <IconButton
            icon={sound ? BellDot : BellOff}
            label={sound ? "Arrival sound on: turn it off" : "Arrival sound off: turn it on"}
            onClick={() => {
              setTraySound(!sound);
              setSound(!sound);
            }}
          />
          <IconButton icon={ChevronDown} label="Fold the tray" onClick={() => fold(true)} />
        </span>
      </header>
      <div className="max-h-[calc(100dvh-var(--tray-bottom,1rem)-7rem)] overflow-y-auto">
        <TrayList items={shown} fresh={fresh} />
        {items.length > STACK && (
          <button type="button" onClick={() => setAll(!all)} className="w-full border-t border-border px-3 py-2 text-left text-xs text-accent hover:bg-hover">
            {all ? "Show fewer" : `Show all ${items.length}`}
          </button>
        )}
      </div>
    </aside>
  );
}

// ---- Phone ----------------------------------------------------------------------------------

export function TrayBar() {
  const items = useTrayItems();
  const onQueueView = useOnQueueView();
  const fresh = useArrivals(items);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const focus = () => {
      if (!window.matchMedia(DESKTOP_QUERY).matches) setOpen(true); // the desktop panel takes it there
    };
    window.addEventListener(FOCUS_TRAY_EVENT, focus);
    return () => window.removeEventListener(FOCUS_TRAY_EVENT, focus);
  }, []);
  if (items.length === 0 || onQueueView) return null;
  const top = items[0];
  return (
    <>
      <button
        type="button"
        data-tray="bar"
        onClick={() => setOpen(true)}
        className={cn(
          "flex min-h-11 w-full shrink-0 items-center gap-2 border-t border-border bg-surface-raised px-3 text-left md:hidden",
          fresh.size > 0 && "animate-tray-pulse",
        )}
      >
        <TrayCount count={items.length} />
        <span className="sr-only">need you: open the tray. First: </span>
        <span className="min-w-0 flex-1 truncate text-sm text-heading">{top ? trayTitle(top) : ""}</span>
        <ChevronUp className="h-4 w-4 shrink-0 text-muted" aria-hidden />
      </button>
      <Sheet open={open} onClose={() => setOpen(false)} title={`Needs you (${items.length})`}>
        <TrayList items={items} fresh={fresh} onNavigate={() => setOpen(false)} />
      </Sheet>
    </>
  );
}

// ---- The list --------------------------------------------------------------------------------

function TrayCount({ count }: { count: number }) {
  return (
    <span className="inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-status-blocked px-1.5 text-2xs font-semibold tabular-nums text-white dark:text-surface">
      {count > 99 ? "99+" : count}
    </span>
  );
}

export function TrayList({ items, fresh, onNavigate }: { items: TrayItem[]; fresh: Set<string>; onNavigate?: () => void }) {
  const [openId, setOpenId] = useState<string | null>(null);
  return (
    <ul aria-label="Waiting on you" className="divide-y divide-border">
      {items.map((item) => (
        <TrayRow
          key={item.id}
          item={item}
          open={openId === item.id}
          fresh={fresh.has(item.id)}
          onToggle={() => setOpenId(openId === item.id ? null : item.id)}
          onClose={() => setOpenId(null)}
          onNavigate={onNavigate}
        />
      ))}
    </ul>
  );
}

function rowIcon(item: TrayItem): ReactNode {
  switch (item.kind) {
    case "approval": {
      const Icon = item.approval.risk === "dangerous" ? ShieldAlert : item.approval.risk === "elevated" ? ShieldQuestion : ShieldCheck;
      const tone = item.approval.risk === "dangerous" ? "text-danger" : item.approval.risk === "elevated" ? "text-warning" : "text-success";
      return <Icon className={cn("mt-0.5 h-4 w-4 shrink-0", tone)} aria-hidden />;
    }
    case "question":
      return <MessagesSquare className={cn("mt-0.5 h-4 w-4 shrink-0", item.question.urgency === "blocking" ? "text-status-blocked" : "text-accent")} aria-hidden />;
    case "dialog":
      return <MessageSquareWarning className="mt-0.5 h-4 w-4 shrink-0 text-danger" aria-hidden />;
    case "job":
      return <ListTodo className={cn("mt-0.5 h-4 w-4 shrink-0", item.job.state === "failed" ? "text-danger" : "text-warning")} aria-hidden />;
    case "agent":
      return <Bot className="mt-0.5 h-4 w-4 shrink-0 text-danger" aria-hidden />;
  }
}

export function TrayRow({
  item,
  open,
  fresh,
  onToggle,
  onClose,
  onNavigate,
}: {
  item: TrayItem;
  open: boolean;
  fresh: boolean;
  onToggle: () => void;
  onClose: () => void;
  onNavigate?: () => void;
}) {
  const cardRef = useRef<HTMLElement>(null);
  const snooze = useSnooze();
  const bodyId = `tray-body-${item.id.replace(/[^A-Za-z0-9_-]/g, "-")}`;
  useEffect(() => {
    if (open && item.kind === "approval") cardRef.current?.focus();
  }, [open, item.kind]);
  const who = item.item.agent;
  return (
    <li className={cn(fresh && "animate-tray-pulse")} data-tray-item={item.id}>
      <div className="flex items-start gap-1 pr-1">
        <button
          type="button"
          data-tray-row
          aria-expanded={open}
          aria-controls={bodyId}
          onClick={onToggle}
          className="flex min-h-12 min-w-0 flex-1 items-start gap-2 px-3 py-2 text-left hover:bg-hover focus-visible:bg-hover"
        >
          {rowIcon(item)}
          <span className="min-w-0 flex-1">
            <span className="line-clamp-2 break-words text-sm font-medium text-heading">{trayTitle(item)}</span>
            <span className="block truncate pt-0.5 text-xs text-muted">
              <span className="text-body" data-where>
                {whereLine(item.item)}
              </span>
              {" · "}
              <span className={cn("font-medium", item.item.urgency === "blocking" ? "text-danger" : "text-body")}>{item.item.urgency === "blocking" ? "Blocking" : "Can wait"}</span>
              {" · "}
              {kindLabel(item.item)}
              {who && ` · ${who}`}
              {item.at && ` · waiting ${relativeTime(item.at)}`}
            </span>
          </span>
        </button>
        <IconButton icon={Clock} label={`Snooze for 10 minutes: ${trayTitle(item)}`} onClick={() => snooze(item.id)} className="mt-1 shrink-0" />
      </div>
      {open && (
        <div id={bodyId} className="px-3 pb-3">
          <RowBody item={item} cardRef={cardRef} onDone={onClose} onNavigate={onNavigate} />
        </div>
      )}
    </li>
  );
}

function RowBody({ item, cardRef, onDone, onNavigate }: { item: TrayItem; cardRef: RefObject<HTMLElement | null>; onDone: () => void; onNavigate?: () => void }) {
  const navigate = useNavigate();
  const go = useCallback(
    (to: string, search: Record<string, string>) => {
      onNavigate?.();
      void navigate({ to, search });
    },
    [navigate, onNavigate],
  );
  switch (item.kind) {
    case "approval":
      return (
        <ApprovalCard
          ref={cardRef}
          approval={item.approval}
          compact
          onDecided={onDone}
          onOpenTerminal={item.approval.pane_id ? () => go("/fleet", { agent: item.approval.pane_id ?? "" }) : undefined}
        />
      );
    case "question":
      return <QuickAnswer question={item.question} onOpen={() => go("/decide", { item: `question:${item.question.id}` })} />;
    case "dialog":
      return <QuickDialog paneId={item.agent.pane_id} name={item.agent.name || item.agent.pane_id} onOpen={() => go("/fleet", { agent: item.agent.pane_id })} />;
    case "job":
      return (
        <div className="flex flex-col gap-2">
          <p className="text-xs text-body">{item.item.detail || item.job.cause_text || "This job is not moving."}</p>
          <Button compact onClick={() => go("/jobs", { job: item.job.id })}>
            Open the job
          </Button>
        </div>
      );
    case "agent": {
      const link = itemLink(item.item);
      return (
        <div className="flex flex-col gap-2">
          <p className="text-xs text-body">{item.standing.detail || item.item.title}</p>
          <Button compact onClick={() => go(link.to, link.search)}>
            Open its terminal
          </Button>
        </div>
      );
    }
  }
}

/**
 * A routine confirm or choice question's options as buttons; anything else (and every question
 * carrying a diff or a long body: R4 Codex P2, a consequential choice must not get a shorter path
 * than its evidence) opens the decision packet.
 */
function QuickAnswer({ question, onOpen }: { question: Question; onOpen: () => void }) {
  const answer = useAnswerQuestion(question.id);
  const toast = useToast();
  const quick = isRoutine(question);
  return (
    <div className="flex flex-col gap-2">
      {question.body_md && <p className="line-clamp-3 break-words text-xs text-body">{plainPreview(question.body_md)}</p>}
      {quick && (
        <ul aria-label="Options" className="flex flex-col gap-1.5">
          {question.options.map((option) => (
            <li key={option.key}>
              <button
                type="button"
                disabled={answer.isPending}
                onClick={() =>
                  answer.mutateAsync({ answer_value: option.key }).then(
                    () => toast.show({ tone: "success", title: "Answered", description: `${question.title} → ${option.label}` }),
                    (error: unknown) => toast.show({ tone: "danger", title: "Not answered", description: error instanceof ApiError ? error.message : String(error) }),
                  )
                }
                className="flex min-h-11 w-full items-center gap-2 rounded border border-border-strong bg-surface px-2.5 py-1.5 text-left text-sm text-heading hover:border-accent hover:bg-hover disabled:opacity-50 md:min-h-9 md:text-xs"
              >
                <span className="min-w-0 flex-1 break-words">{option.label}</span>
                {question.recommend === option.key && <Chip tone="accent">Recommended</Chip>}
              </button>
            </li>
          ))}
        </ul>
      )}
      <Button compact variant={quick ? "ghost" : "primary"} onClick={onOpen}>
        {quick ? "Review (add a note)" : "Review the decision"}
      </Button>
    </div>
  );
}

function QuickDialog({ paneId, name, onOpen }: { paneId: string; name: string; onOpen: () => void }) {
  const screen = useAgentScreen(paneId);
  if (!screen.data) return <p className="text-xs text-muted">Reading the screen…</p>;
  return <DialogCard target={paneId} agentName={name} screen={screen.data} onOpenTerminal={onOpen} />;
}
