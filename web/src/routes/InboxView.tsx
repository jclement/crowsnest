// The Inbox (DESIGN.md sections 8, 12 and 22): everything that needs the human, in one list. The
// Open tab is the server's attention queue (lib/attention.ts): approvals, questions, terminal
// dialogs, stuck or failed jobs and standing agents at their terminal, de-duplicated and ordered
// by the server, each labelled with its project and job; snoozed items fold away below, notices
// follow. The History tab holds settled questions and approvals.
//
// The selection lives in the URL (?q=q_7, ?q=approval:ap_3 or ?q=dialog:<pane id>) so a link, a
// notification or a reload lands on it. From md the list and the selected item sit side by side; on a phone the item opens in a
// full-screen sheet.
import { useNavigate, useSearch } from "@tanstack/react-router";
import { Bell, BellOff, BellRing, Bot, FileDiff, Hourglass, Inbox, ListTodo, MessageSquareWarning, MessagesSquare, RefreshCw, ShieldAlert } from "lucide-react";
import { useState, type ReactElement } from "react";
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
import { activeItems, countsSentence, itemLink, kindLabel, labelOf, labelWords, snoozedItems, whereLine, type AttentionItem } from "../lib/attention";
import { useAgentScreen, useApproval, useApprovals, useAttention, useAttentionAction, useFleet, useQuestions } from "../lib/queries";
import { outcome, requester, riskLabels, type Approval } from "../lib/approvals";
import { ApprovalCard } from "../components/approvals/ApprovalCard";
import { answerSummary, kindLabels, type Question } from "../lib/questions";
import { HelpButton } from "../components/help/HelpButton";

type InboxTab = "open" | "history";

const DIALOG_PREFIX = "dialog:";
const APPROVAL_PREFIX = "approval:";

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
          <HelpButton topic="concepts/questions-approvals-dialogs" what="questions, approvals and dialogs" inline={false} />
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
  // The Open list is the attention queue (DESIGN.md 22), in the server's order and with its
  // count: the same items, the same number, as the badge, the tray and the Now view. Notices
  // (information, nothing to answer) follow in their own group.
  const attention = useAttention();
  const questions = useQuestions("open");
  const navigate = useNavigate();
  const unsnooze = useAttentionAction();
  const desktop = useMediaQuery(DESKTOP_QUERY);
  if (attention.isPending) return <SkeletonRows rows={5} label="Loading what needs you" />;
  if (attention.isError) return <LoadError message={attention.error.message} retry={() => void attention.refetch()} />;
  const items = activeItems(attention.data);
  const snoozed = snoozedItems(attention.data);
  const notices = (questions.data ?? []).filter((question) => question.kind === "notice");
  const open = (item: AttentionItem) => {
    if (item.kind === "approval" || item.kind === "question" || item.kind === "dialog") {
      // A phone opens the decision packet (answer bar in thumb reach, R4 Fable 3.3); the desktop
      // keeps its list beside the detail.
      if (!desktop) {
        void navigate({ to: "/decide", search: { item: item.id } });
        return;
      }
      onSelect(item.kind === "question" ? (item.question?.id ?? item.id) : item.id);
      return;
    }
    const link = itemLink(item);
    void navigate({ to: link.to, search: link.search });
  };
  if (items.length === 0 && snoozed.length === 0 && notices.length === 0) {
    return (
      <EmptyState icon={Inbox} title="Nothing needs you">
        When an agent asks a question, wants a tool call approved or stops at a terminal dialog, it lands here.
      </EmptyState>
    );
  }
  return (
    <>
      <p className="border-b border-border px-3 py-1.5 text-xs text-muted" data-attention-count={attention.data.counts.total}>
        {countsSentence(attention.data.counts).replace(/\.$/, "")}
      </p>
      <ul aria-label="Needs you" className="divide-y divide-border">
        {items.map((item) => (
          <AttentionRow key={item.id} item={item} selected={selected === selectionOf(item)} onOpen={() => open(item)} />
        ))}
      </ul>
      {snoozed.length > 0 && (
        <details className="border-t border-border">
          <summary className="cursor-pointer px-3 py-2 text-xs font-medium text-muted hover:bg-hover">Snoozed ({snoozed.length})</summary>
          <ul aria-label="Snoozed" className="divide-y divide-border">
            {snoozed.map((item) => (
              <li key={item.id} className="flex items-center gap-2 px-3 py-2">
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm text-body">{item.title}</span>
                  <span className="block truncate text-2xs text-muted">
                    {whereLine(item)} · back {relativeTime(item.snoozed_until ?? "")}
                  </span>
                </span>
                <Button compact variant="ghost" onClick={() => unsnooze.mutate({ action: "unsnooze", id: item.id })}>
                  Bring back
                </Button>
              </li>
            ))}
          </ul>
        </details>
      )}
      {notices.length > 0 && (
        <>
          <h3 className="border-t border-border px-3 pb-1 pt-2 text-2xs font-semibold uppercase tracking-wide text-muted">Notices</h3>
          <ul aria-label="Notices" className="divide-y divide-border">
            {notices.map((question) => (
              <QuestionRow key={question.id} question={question} selected={selected === question.id} onSelect={onSelect} />
            ))}
          </ul>
        </>
      )}
    </>
  );
}

/** The Inbox selection an item opens as (?q=…). */
function selectionOf(item: AttentionItem): string {
  return item.kind === "question" ? (item.question?.id ?? item.id) : item.id;
}

function attentionIcon(item: AttentionItem): ReactElement {
  switch (item.kind) {
    case "approval":
      return <ShieldAlert className={cn("mt-0.5 h-4 w-4 shrink-0", item.approval?.risk === "dangerous" ? "text-danger" : item.approval?.risk === "elevated" ? "text-warning" : "text-success")} aria-hidden />;
    case "question":
      return <MessagesSquare className={cn("mt-0.5 h-4 w-4 shrink-0", item.urgency === "blocking" ? "text-status-blocked" : "text-accent")} aria-hidden />;
    case "dialog":
      return <MessageSquareWarning className="mt-0.5 h-4 w-4 shrink-0 text-danger" aria-hidden />;
    case "agent":
      return <Bot className="mt-0.5 h-4 w-4 shrink-0 text-danger" aria-hidden />;
    case "job":
    case "wait":
      return <ListTodo className={cn("mt-0.5 h-4 w-4 shrink-0", item.job?.state === "failed" ? "text-danger" : "text-warning")} aria-hidden />;
    case "limit":
      return <Hourglass className="mt-0.5 h-4 w-4 shrink-0 text-warning" aria-hidden />;
  }
}

/** One attention item: its title, where it belongs (project and job), its kind and how long it has waited. */
function AttentionRow({ item, selected, onOpen }: { item: AttentionItem; selected: boolean; onOpen: () => void }) {
  return (
    <li data-attention-item={item.id}>
      <button type="button" onClick={onOpen} aria-current={selected || undefined} className={cn(rowClass, selected && "bg-selected")}>
        <span className="flex w-full items-start gap-2">
          {attentionIcon(item)}
          <span className={cn("min-w-0 flex-1 break-words text-sm font-medium text-heading", item.kind === "approval" && "font-mono text-xs")}>{item.title}</span>
          <span className="shrink-0 text-2xs text-muted">{relativeTime(item.since)}</span>
        </span>
        <span className="block w-full truncate text-xs text-body">{whereLine(item)}</span>
        <span className="flex flex-wrap items-center gap-1">
          <Chip tone={labelWords[labelOf(item)].tone}>{labelWords[labelOf(item)].text}</Chip>
          <Chip>{kindLabel(item)}</Chip>
          {item.agent && <Chip mono>{item.agent}</Chip>}
          {item.detail && <span className="min-w-0 truncate text-xs text-muted">{item.detail}</span>}
        </span>
      </button>
    </li>
  );
}

function HistoryList({ selected, onSelect }: { selected?: string; onSelect: (id: string) => void }) {
  const questions = useQuestions("answered,expired,cancelled");
  const approvals = useApprovals("all");
  if (questions.isPending) return <SkeletonRows rows={5} label="Loading history" />;
  if (questions.isError) return <LoadError message={questions.error.message} retry={() => void questions.refetch()} />;
  const settledApprovals = (approvals.data ?? []).filter((approval) => approval.status !== "pending");
  if (questions.data.length === 0 && settledApprovals.length === 0) {
    return (
      <EmptyState icon={Inbox} title="No history yet">
        Answered, expired and cancelled questions, and settled approvals, are kept here.
      </EmptyState>
    );
  }
  type Entry = { at: string; node: ReactElement };
  const entries: Entry[] = [
    ...questions.data.map((question) => ({
      at: question.settled_at ?? question.created_at,
      node: <QuestionRow key={question.id} question={question} selected={selected === question.id} onSelect={onSelect} />,
    })),
    ...settledApprovals.map((approval) => ({
      at: approval.settled_at ?? approval.created_at,
      node: <ApprovalRow key={approval.id} approval={approval} selected={selected === APPROVAL_PREFIX + approval.id} onSelect={onSelect} />,
    })),
  ].sort((a, b) => b.at.localeCompare(a.at));
  return (
    <ul aria-label="History" className="divide-y divide-border">
      {entries.map((entry) => entry.node)}
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

function ApprovalRow({ approval, selected, onSelect }: { approval: Approval; selected: boolean; onSelect: (id: string) => void }) {
  const settled = approval.status !== "pending";
  return (
    <li>
      <button
        type="button"
        onClick={() => onSelect(APPROVAL_PREFIX + approval.id)}
        aria-current={selected || undefined}
        className={cn(rowClass, selected && "bg-selected")}
      >
        <span className="flex w-full items-start gap-2">
          <ShieldAlert className={cn("mt-0.5 h-4 w-4 shrink-0", approval.risk === "dangerous" ? "text-danger" : approval.risk === "elevated" ? "text-warning" : "text-success")} aria-hidden />
          <span className={cn("min-w-0 flex-1 break-words font-mono text-xs font-medium", settled ? "text-body" : "text-heading")}>{approval.summary}</span>
          <span className="shrink-0 text-2xs text-muted">{relativeTime(approval.settled_at ?? approval.created_at)}</span>
        </span>
        <span className="flex flex-wrap items-center gap-1">
          <Chip tone={approval.risk === "dangerous" ? "danger" : approval.risk === "elevated" ? "warning" : "success"}>{riskLabels[approval.risk]}</Chip>
          <Chip>Approval</Chip>
          <span className="min-w-0 truncate text-xs text-muted">{settled ? `→ ${outcome(approval)}` : requester(approval)}</span>
        </span>
      </button>
    </li>
  );
}

// ---- Selected item -------------------------------------------------------------------------

function SelectedItem({ id, onDone }: { id: string; onDone?: () => void }) {
  if (id.startsWith(APPROVAL_PREFIX)) return <ApprovalDetail id={id.slice(APPROVAL_PREFIX.length)} />;
  if (id.startsWith(DIALOG_PREFIX)) return <DialogDetail paneId={id.slice(DIALOG_PREFIX.length)} />;
  return <QuestionDetail key={id} id={id} onDone={onDone} />;
}

function ApprovalDetail({ id }: { id: string }) {
  const approval = useApproval(id);
  const navigate = useNavigate();
  if (approval.isError) {
    return (
      <EmptyState icon={Inbox} title="No such approval">
        It may have been cleared; settled approvals are in the History tab.
      </EmptyState>
    );
  }
  if (!approval.data) return <SkeletonRows rows={6} label="Loading the approval" />;
  const pane = approval.data.pane_id;
  return (
    <div className="p-3 md:p-4">
      <ApprovalCard
        key={approval.data.id + approval.data.status}
        approval={approval.data}
        onOpenTerminal={pane ? () => void navigate({ to: "/agents", search: { agent: pane } }) : undefined}
      />
    </div>
  );
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
        onOpenTerminal={() => void navigate({ to: "/agents", search: { agent: paneId } })}
      />
    </div>
  );
}

function SheetTitle({ id }: { id?: string }) {
  if (!id) return null;
  const label = id.startsWith(APPROVAL_PREFIX) ? `Approval ${id.slice(APPROVAL_PREFIX.length)}` : id.startsWith(DIALOG_PREFIX) ? "Terminal dialog" : `Question ${id}`;
  return <span className="truncate">{label}</span>;
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
