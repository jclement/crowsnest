// The Inbox (DESIGN.md sections 8 and 12): everything that needs the human, in one list. Two
// sources feed it: questions agents asked through crowsnest, and terminal dialogs (agents blocked
// on a permission or question prompt, from the fleet). Blocking questions and dialogs come first,
// then other open questions, notices last; the History tab holds settled questions.
//
// The selection lives in the URL (?q=q_7, or ?q=dialog:<pane id>) so a link or a reload lands on
// it. From md the list and the selected item sit side by side; on a phone the item opens in a
// full-screen sheet.
import { useNavigate, useSearch } from "@tanstack/react-router";
import { Bell, BellOff, BellRing, FileDiff, Inbox, MessageSquareWarning, RefreshCw } from "lucide-react";
import { useState } from "react";
import { DialogCard } from "../components/fleet/DialogCard";
import { QuestionDetail } from "../components/inbox/QuestionDetail";
import { Banner } from "../components/ui/Banner";
import { Button } from "../components/ui/Button";
import { Chip } from "../components/ui/Chip";
import { EmptyState } from "../components/ui/EmptyState";
import { Sheet } from "../components/ui/Sheet";
import { SkeletonRows } from "../components/ui/Skeleton";
import { TabPanel, Tabs } from "../components/ui/Tabs";
import { cn } from "../lib/cn";
import { relativeTime } from "../lib/format";
import { DESKTOP_QUERY, useMediaQuery } from "../lib/media";
import { notificationState, requestNotifications, type NotificationState } from "../lib/notifications";
import { useAgentScreen, useFleet, useQuestions } from "../lib/queries";
import { answerSummary, compareQuestions, kindLabels, type Question } from "../lib/questions";
import type { Agent } from "../lib/types";

type InboxTab = "open" | "history";

const DIALOG_PREFIX = "dialog:";

export function InboxView() {
  const { q: selected } = useSearch({ strict: false }) as { q?: string };
  const navigate = useNavigate();
  const desktop = useMediaQuery(DESKTOP_QUERY);
  const [tab, setTab] = useState<InboxTab>("open");
  const select = (id: string | undefined) => void navigate({ to: "/inbox", search: { q: id } });

  const detail = selected ? <SelectedItem id={selected} onDone={desktop ? undefined : () => select(undefined)} /> : null;

  return (
    <div className="flex h-full min-h-0 flex-col md:flex-row">
      <div className="flex min-h-0 flex-col border-border md:w-80 md:shrink-0 md:border-r lg:w-96">
        <header className="flex items-center gap-2 border-b border-border px-3 py-2">
          <Tabs<InboxTab>
            id="inbox-tabs"
            label="Inbox"
            value={tab}
            onChange={setTab}
            items={[
              { value: "open", label: "Open" },
              { value: "history", label: "History" },
            ]}
          />
          <NotificationToggle className="ml-auto" />
        </header>
        <TabPanel id="inbox-tabs" value={tab} className="min-h-0 flex-1 overflow-y-auto">
          {tab === "open" ? <OpenList selected={selected} onSelect={select} /> : <HistoryList selected={selected} onSelect={select} />}
        </TabPanel>
      </div>
      {desktop ? (
        <section aria-label="Selected item" className="min-h-0 min-w-0 flex-1 overflow-y-auto">
          {detail ?? (
            <EmptyState icon={Inbox} title="Pick something to answer">
              Questions and dialogs open here. Answers go straight back to the agent that asked.
            </EmptyState>
          )}
        </section>
      ) : (
        <Sheet open={!!selected} onClose={() => select(undefined)} title={<SheetTitle id={selected} />}>
          {detail}
        </Sheet>
      )}
    </div>
  );
}

// ---- Lists ---------------------------------------------------------------------------------

function OpenList({ selected, onSelect }: { selected?: string; onSelect: (id: string) => void }) {
  const questions = useQuestions("open");
  const fleet = useFleet();
  const dialogs = (fleet.data?.agents ?? []).filter((agent) => agent.needs_you);

  if (questions.isPending) return <SkeletonRows rows={5} label="Loading questions" />;
  if (questions.isError) return <LoadError message={questions.error.message} retry={() => void questions.refetch()} />;

  const sorted = [...questions.data].sort(compareQuestions);
  const blocking = sorted.filter((question) => question.urgency === "blocking" && question.kind !== "notice");
  const rest = sorted.filter((question) => !blocking.includes(question));
  if (sorted.length === 0 && dialogs.length === 0) {
    return (
      <EmptyState icon={Inbox} title="Nothing needs you">
        When an agent asks a question or stops at a terminal dialog, it lands here.
      </EmptyState>
    );
  }
  return (
    <ul aria-label="Needs you" className="divide-y divide-border">
      {blocking.map((question) => (
        <QuestionRow key={question.id} question={question} selected={selected === question.id} onSelect={onSelect} />
      ))}
      {dialogs.map((agent) => (
        <DialogRow key={agent.pane_id} agent={agent} selected={selected === DIALOG_PREFIX + agent.pane_id} onSelect={onSelect} />
      ))}
      {rest.map((question) => (
        <QuestionRow key={question.id} question={question} selected={selected === question.id} onSelect={onSelect} />
      ))}
    </ul>
  );
}

function HistoryList({ selected, onSelect }: { selected?: string; onSelect: (id: string) => void }) {
  const questions = useQuestions("answered,expired,cancelled");
  if (questions.isPending) return <SkeletonRows rows={5} label="Loading history" />;
  if (questions.isError) return <LoadError message={questions.error.message} retry={() => void questions.refetch()} />;
  if (questions.data.length === 0) {
    return (
      <EmptyState icon={Inbox} title="No history yet">
        Answered, expired and cancelled questions are kept here.
      </EmptyState>
    );
  }
  return (
    <ul aria-label="History" className="divide-y divide-border">
      {[...questions.data].sort(compareQuestions).map((question) => (
        <QuestionRow key={question.id} question={question} selected={selected === question.id} onSelect={onSelect} />
      ))}
    </ul>
  );
}

function LoadError({ message, retry }: { message: string; retry: () => void }) {
  return (
    <Banner tone="danger" role="alert" action={<Button compact icon={RefreshCw} onClick={retry}>Retry</Button>}>
      Could not load questions: {message}
    </Banner>
  );
}

const rowClass = "flex w-full min-h-14 flex-col gap-1 px-3 py-2.5 text-left hover:bg-hover focus-visible:bg-hover";

function QuestionRow({ question, selected, onSelect }: { question: Question; selected: boolean; onSelect: (id: string) => void }) {
  const settled = question.status !== "open";
  return (
    <li>
      <button type="button" onClick={() => onSelect(question.id)} aria-current={selected || undefined} className={cn(rowClass, selected && "bg-selected")}>
        <span className="flex w-full items-start gap-2">
          {question.urgency === "blocking" && !settled && <span aria-label="Blocking" className="mt-1.5 h-2 w-2 shrink-0 rotate-45 bg-status-blocked" />}
          <span className={cn("min-w-0 flex-1 break-words text-sm font-medium", settled ? "text-body" : "text-heading")}>{question.title}</span>
          <span className="shrink-0 text-2xs text-muted">{relativeTime(question.settled_at ?? question.created_at)}</span>
        </span>
        <span className="flex flex-wrap items-center gap-1">
          <Chip tone={question.kind === "notice" ? "neutral" : "accent"}>{question.multi ? "Pick several" : kindLabels[question.kind]}</Chip>
          {question.context.repo && <Chip mono>{question.context.repo}</Chip>}
          {question.file_count > 0 && (
            <Chip>
              <FileDiff className="h-3 w-3" aria-hidden />
              {question.file_count} {question.file_count === 1 ? "file" : "files"}
            </Chip>
          )}
          {settled && <span className="min-w-0 truncate text-xs text-muted">→ {answerSummary(question)}</span>}
        </span>
      </button>
    </li>
  );
}

function DialogRow({ agent, selected, onSelect }: { agent: Agent; selected: boolean; onSelect: (id: string) => void }) {
  return (
    <li>
      <button
        type="button"
        onClick={() => onSelect(DIALOG_PREFIX + agent.pane_id)}
        aria-current={selected || undefined}
        className={cn(rowClass, selected && "bg-selected")}
      >
        <span className="flex w-full items-start gap-2">
          <MessageSquareWarning className="mt-0.5 h-4 w-4 shrink-0 text-danger" aria-hidden />
          <span className="min-w-0 flex-1 break-words text-sm font-medium text-heading">{agent.name || agent.pane_id} is waiting at a dialog</span>
        </span>
        <span className="flex flex-wrap items-center gap-1">
          <Chip tone="danger">Terminal dialog</Chip>
          <Chip mono>{agent.workspace}</Chip>
          {agent.title && <span className="min-w-0 truncate text-xs text-muted">{agent.title}</span>}
        </span>
      </button>
    </li>
  );
}

// ---- Selected item -------------------------------------------------------------------------

function SelectedItem({ id, onDone }: { id: string; onDone?: () => void }) {
  if (id.startsWith(DIALOG_PREFIX)) return <DialogDetail paneId={id.slice(DIALOG_PREFIX.length)} />;
  return <QuestionDetail key={id} id={id} onDone={onDone} />;
}

function DialogDetail({ paneId }: { paneId: string }) {
  const fleet = useFleet();
  const screen = useAgentScreen(paneId);
  const navigate = useNavigate();
  const agent = fleet.data?.agents.find((entry) => entry.pane_id === paneId);
  if (!agent || !agent.needs_you) {
    return (
      <EmptyState icon={Inbox} title="That dialog is gone">
        The agent is no longer waiting at a dialog.
      </EmptyState>
    );
  }
  if (!screen.data) return <SkeletonRows rows={6} label="Loading the screen" />;
  return (
    <div className="p-3 md:p-4">
      <DialogCard
        target={paneId}
        agentName={agent.name || paneId}
        screen={screen.data}
        onOpenTerminal={() => void navigate({ to: "/fleet", search: { agent: paneId } })}
      />
    </div>
  );
}

function SheetTitle({ id }: { id?: string }) {
  if (!id) return null;
  return <span className="truncate">{id.startsWith(DIALOG_PREFIX) ? "Terminal dialog" : `Question ${id}`}</span>;
}

// ---- Notifications -------------------------------------------------------------------------

/** Asks for notification permission on a press (never on load), and shows the state after. */
function NotificationToggle({ className }: { className?: string }) {
  const [state, setState] = useState<NotificationState>(() => notificationState());
  if (state === "unsupported") return null;
  if (state === "granted") {
    return (
      <span className={cn("inline-flex items-center gap-1 text-2xs text-muted", className)} title="You get a browser notification when a question arrives while this tab is hidden">
        <BellRing className="h-3.5 w-3.5" aria-hidden />
        Notifying
      </span>
    );
  }
  if (state === "denied") {
    return (
      <span className={cn("inline-flex items-center gap-1 text-2xs text-muted", className)} title="Notifications are blocked for this site in the browser's settings">
        <BellOff className="h-3.5 w-3.5" aria-hidden />
        Blocked
      </span>
    );
  }
  return (
    <Button compact variant="ghost" icon={Bell} className={className} onClick={() => void requestNotifications().then(setState)}>
      Notify me
    </Button>
  );
}
