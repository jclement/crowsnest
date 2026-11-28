// The decision packet (`/decide?item=<attention item id>`; R4 Codex P1/P2, Fable 3.2/3.3). One
// screen per decision that reads top to bottom: where it belongs (project, job, agent, how long it
// has waited), what is asked, what each choice does in a line, the agent's recommendation, then
// the evidence (the command or the diff with a file list, the question's body, the agent's last
// message), and a sticky answer bar at the bottom within thumb reach. A push, the tray, Now and
// the Inbox all link here with the queue's item id. A decision that is already settled (answered
// elsewhere, superseded, expired) says so instead of failing on submit; the note is kept in
// localStorage (lib/decision.ts) so rotation and backgrounding never lose it.
import { Link, useNavigate, useRouter, useSearch } from "@tanstack/react-router";
import { ArrowLeft, CheckCircle2, ChevronRight, Clock, ExternalLink, Inbox, RefreshCw, ShieldCheck, Star } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { AttachmentList } from "../components/attach/AttachmentList";
import { CallBody } from "../components/approvals/ApprovalCard";
import { AnswerBar } from "../components/decide/AnswerBar";
import { DiffNavigator } from "../components/decide/DiffNavigator";
import { ScreenText } from "../components/fleet/DialogCard";
import { Outcome } from "../components/inbox/QuestionDetail";
import { Markdown } from "../components/Markdown";
import { Banner } from "../components/ui/Banner";
import { Button } from "../components/ui/Button";
import { Chip } from "../components/ui/Chip";
import { SkeletonRows } from "../components/ui/Skeleton";
import type { Approval } from "../lib/approvals";
import { riskLabels } from "../lib/approvals";
import { shownFromAttachment } from "../lib/attachments";
import { activeItems, kindLabel, labelTone, type AttentionItem } from "../lib/attention";
import { useAgentMessages } from "../lib/conversation-queries";
import {
  approvalBrief,
  ADVANCE_GUARD_MS,
  approvalStale,
  dialogBrief,
  lastAssistantText,
  parseItemId,
  questionBrief,
  questionStale,
  recentAnswer,
  type DecisionBrief,
} from "../lib/decision";
import { relativeTime } from "../lib/format";
import { quotedReply } from "../lib/protocol";
import { useAgentScreen, useApproval, useAttention, useQuestion } from "../lib/queries";
import type { Question } from "../lib/questions";
import type { AgentScreen } from "../lib/types";

export function DecideView() {
  const { item: itemId } = useSearch({ strict: false }) as { item?: string };
  const parsed = parseItemId(itemId);
  const attention = useAttention();
  const item = attention.data?.items.find((candidate) => candidate.id === itemId);
  if (!parsed || !itemId) return <NotADecision />;
  switch (parsed.kind) {
    case "question":
      return <QuestionPacket key={itemId} id={parsed.ref} itemId={itemId} item={item} />;
    case "approval":
      return <ApprovalPacket key={itemId} id={parsed.ref} itemId={itemId} item={item} />;
    case "dialog":
      return <DialogPacket key={itemId} paneId={parsed.ref} itemId={itemId} item={item} loaded={!attention.isPending} />;
  }
}

// ---- Per kind --------------------------------------------------------------------------------

function QuestionPacket({ id, itemId, item }: { id: string; itemId: string; item?: AttentionItem }) {
  const query = useQuestion(id);
  if (query.isPending) return <Loading what={id} paused={query.fetchStatus === "paused"} retry={() => void query.refetch()} />;
  if (query.isError) return <LoadError what={id} message={query.error.message} retry={() => void query.refetch()} />;
  const question = query.data;
  const brief = questionBrief(question, item?.urgency === "blocking");
  return (
    <Packet
      itemId={itemId}
      item={item}
      brief={brief}
      jobId={question.job_id ?? item?.job_id}
      since={question.created_at}
      stale={questionStale(question)}
      paneId={question.source_pane}
      bar={(next, armedAt) => <AnswerBar itemId={itemId} jobId={question.job_id ?? item?.job_id} target={{ kind: "question", question }} onAnswered={next} armedAt={armedAt} />}
    >
      {/* Settled: what was answered and how far it got (stored, held, received: DESIGN.md 6.19). */}
      {question.status !== "open" && <Outcome question={question} />}
      <QuestionEvidence question={question} />
    </Packet>
  );
}

function ApprovalPacket({ id, itemId, item }: { id: string; itemId: string; item?: AttentionItem }) {
  const query = useApproval(id);
  if (query.isPending) return <Loading what={id} paused={query.fetchStatus === "paused"} retry={() => void query.refetch()} />;
  if (query.isError) return <LoadError what={id} message={query.error.message} retry={() => void query.refetch()} />;
  const approval = query.data;
  return (
    <Packet
      itemId={itemId}
      item={item}
      brief={approvalBrief(approval)}
      jobId={approval.job_id}
      since={approval.created_at}
      stale={approvalStale(approval)}
      paneId={approval.pane_id}
      bar={(next, armedAt) => <AnswerBar itemId={itemId} jobId={approval.job_id} target={{ kind: "approval", approval }} onAnswered={next} armedAt={armedAt} />}
    >
      <ApprovalEvidence approval={approval} />
    </Packet>
  );
}

function DialogPacket({ paneId, itemId, item, loaded }: { paneId: string; itemId: string; item?: AttentionItem; loaded: boolean }) {
  const screen = useAgentScreen(paneId);
  if (screen.isPending) return <Loading what={paneId} paused={screen.fetchStatus === "paused"} retry={() => void screen.refetch()} />;
  if (screen.isError) return <LoadError what={paneId} message={screen.error.message} retry={() => void screen.refetch()} />;
  const data: AgentScreen = screen.data;
  const present = !!item || (!loaded && data.dialog.present);
  const who = data.name || item?.agent || "An agent";
  const trust = data.dialog.present ? data.dialog.trust : undefined;
  const title = trust ? `${who} asks to trust ${trust.folder || "its folder"}` : `${who} is stopped at a terminal dialog`;
  return (
    <Packet
      itemId={itemId}
      item={item}
      brief={dialogBrief(paneId, title, data.dialog.options, trust)}
      jobId={item?.job_id}
      since={item?.since}
      stale={present && data.dialog.present ? null : "The dialog is gone: it was answered or the agent moved on."}
      paneId={paneId}
      bar={(next, armedAt) => <AnswerBar itemId={itemId} jobId={item?.job_id} target={{ kind: "dialog", paneId, screen: data, agentName: data.name }} onAnswered={next} armedAt={armedAt} />}
    >
      <Section title="The screen">
        <ScreenText text={data.text} />
      </Section>
    </Packet>
  );
}

// ---- The page --------------------------------------------------------------------------------

interface PacketProps {
  itemId: string;
  item?: AttentionItem;
  brief: DecisionBrief;
  jobId?: string;
  since?: string;
  stale: string | null;
  paneId?: string;
  bar: (next: (answered: string) => void, armedAt: number) => ReactNode;
  children: ReactNode;
}

function Packet({ itemId, item, brief, jobId, since, stale, paneId, bar, children }: PacketProps) {
  const navigate = useNavigate();
  const attention = useAttention();
  // The decisions in the queue, in its order: this one's place ("3 of 16") and the next one.
  const decisions = activeItems(attention.data).filter((candidate) => candidate.kind === "question" || candidate.kind === "approval" || candidate.kind === "dialog");
  const index = decisions.findIndex((candidate) => candidate.id === itemId);
  const following = (index >= 0 ? [...decisions.slice(index + 1), ...decisions.slice(0, index)] : decisions).find((candidate) => candidate.id !== itemId);
  // Arriving straight after an answer: say what was answered, and keep this item's choices inert
  // for a moment so the tap that answered the last one cannot answer this one (R10 Opus B1).
  const [arrival] = useState(() => {
    const answered = recentAnswer();
    return { answered: answered?.title ?? null, armedAt: answered ? Date.now() + ADVANCE_GUARD_MS : 0 };
  });
  // After an answer, the next decision in the queue (else Now): a phone run through the queue
  // is answer, answer, answer, without going back to a list.
  const next = () => {
    void navigate(following ? { to: "/decide", search: { item: following.id } } : { to: "/" });
  };
  return (
    <div className="flex min-h-full flex-col" data-testid="decision-packet" data-item={itemId}>
      <article aria-labelledby="decision-ask" className="mx-auto flex w-full max-w-3xl flex-1 flex-col gap-3 p-3 md:p-4">
        {arrival.answered && (
          <p role="status" className="flex items-center gap-1.5 rounded border border-success/40 bg-success/10 px-2 py-1 text-xs text-heading" data-testid="answered-notice">
            <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-success" aria-hidden />
            <span className="min-w-0 truncate">
              {arrival.answered}. <span className="font-medium">Next up:</span>
            </span>
          </p>
        )}
        <Where item={item} jobId={jobId} since={since} kind={brief.kind} position={index >= 0 ? { at: index + 1, of: decisions.length } : null} onSkip={following ? () => void navigate({ to: "/decide", search: { item: following.id } }) : undefined} />
        <h1 id="decision-ask" className="break-words text-base font-semibold leading-snug text-heading">
          {brief.ask}
        </h1>
        {stale ? (
          <Banner tone="info" role="status" className="rounded" action={<StaleActions />}>
            {stale}
          </Banner>
        ) : (
          brief.blocking && <p className="text-xs font-medium text-warning">{brief.blocking}</p>
        )}
        {brief.choices.some((choice) => choice.said) ? (
          <Consequences brief={brief} />
        ) : (
          brief.recommendation && (
            <p className="flex items-start gap-1.5 rounded-md border border-accent/40 bg-accent/5 px-2.5 py-2 text-xs text-heading" data-testid="recommendation">
              {brief.kind === "approval" ? <ShieldCheck className="mt-px h-3.5 w-3.5 shrink-0 text-success" aria-hidden /> : <Star className="mt-px h-3.5 w-3.5 shrink-0 text-accent" aria-hidden />}
              <span>{brief.recommendation}</span>
            </p>
          )
        )}
        {children}
        {paneId && <LastMessage paneId={paneId} />}
      </article>
      {!stale && bar(next, arrival.armedAt)}
    </div>
  );
}

interface WhereProps {
  item?: AttentionItem;
  jobId?: string;
  since?: string;
  kind: string;
  /** This decision's place in the queue's decisions, or null when it has left the queue. */
  position: { at: number; of: number } | null;
  /** Go to the next decision without answering or snoozing this one; absent when it is the only one. */
  onSkip?: () => void;
}

function Where({ item, jobId, since, kind, position, onSkip }: WhereProps) {
  const router = useRouter();
  // Back goes where the person came from (the Inbox, a job, Now), and to Now on a cold start.
  const back = () => (window.history.length > 1 ? router.history.back() : void router.navigate({ to: "/" }));
  return (
    <nav aria-label="Where this belongs" className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-xs">
      <button type="button" onClick={back} className="inline-flex min-h-9 items-center gap-1 rounded px-1 text-muted hover:bg-hover hover:text-heading md:min-h-6" aria-label="Back">
        <ArrowLeft className="h-3.5 w-3.5" aria-hidden />
      </button>
      <Chip tone={item ? labelTone(item) : "accent"}>{item ? kindLabel(item) : kind}</Chip>
      <span className="font-medium text-heading">{item?.project?.name ?? "Unfiled"}</span>
      {jobId && (
        <Link to="/jobs" search={{ job: jobId }} className="inline-flex min-h-9 items-center font-mono text-accent hover:underline md:min-h-6">
          {jobId}
        </Link>
      )}
      {item?.job_title && <span className="min-w-0 truncate text-body">{item.job_title}</span>}
      <span className="ml-auto inline-flex items-center gap-1.5">
        {since && (
          <span className="inline-flex items-center gap-1 text-2xs text-muted" title={since}>
            <Clock className="h-3 w-3" aria-hidden />
            {relativeTime(since).replace(/ ago$/, "")}
          </span>
        )}
        {position && (
          <span className="font-mono text-2xs text-muted" data-testid="decision-position">
            {position.at} of {position.of}
          </span>
        )}
        {onSkip && (
          <button type="button" onClick={onSkip} className="inline-flex min-h-9 items-center gap-0.5 rounded px-1.5 text-xs text-accent hover:bg-hover md:min-h-6">
            Skip
            <ChevronRight className="h-3.5 w-3.5" aria-hidden />
          </button>
        )}
      </span>
    </nav>
  );
}

function Consequences({ brief }: { brief: DecisionBrief }) {
  return (
    <section aria-label="What each choice does" className="rounded-md border border-border">
      <h2 className="border-b border-border bg-surface-raised px-2.5 py-1 text-2xs font-semibold uppercase tracking-wide text-muted">If you choose</h2>
      <ul className="divide-y divide-border">
        {brief.choices.map((choice) => (
          <li key={choice.key} className="flex flex-col gap-0.5 px-2.5 py-1.5">
            <span className="flex items-center gap-1.5 text-xs font-medium text-heading">
              {choice.label}
              {choice.recommended && <Star className="h-3 w-3 text-accent" aria-label="Recommended" />}
            </span>
            <span className="text-xs text-body">{choice.consequence}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

function QuestionEvidence({ question }: { question: Question }) {
  const context = Object.entries(question.context);
  return (
    <>
      {context.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {context.map(([key, value]) => (
            <Chip key={key} mono title={key}>
              <span className="text-muted">{key}</span> {value}
            </Chip>
          ))}
        </div>
      )}
      {question.body_md.trim() && (
        <Section title="What the agent wrote">
          <Markdown className="text-sm">{question.body_md}</Markdown>
        </Section>
      )}
      {question.files && question.files.length > 0 && (
        <Section title="The change">
          <DiffNavigator files={question.files} />
        </Section>
      )}
      {question.attachments && question.attachments.length > 0 && (
        <AttachmentList files={question.attachments.map(shownFromAttachment)} label="Files the agent attached" />
      )}
    </>
  );
}

function ApprovalEvidence({ approval }: { approval: Approval }) {
  return (
    <Section title="The call">
      <div className="flex flex-col gap-2">
        <p className="flex flex-wrap items-center gap-1 text-2xs text-muted">
          <Chip tone={approval.risk === "dangerous" ? "danger" : approval.risk === "elevated" ? "warning" : "neutral"}>{riskLabels[approval.risk]}</Chip>
          <Chip mono>{approval.tool_name}</Chip>
          {approval.risk_reasons.length > 0 && <span>{approval.risk_reasons.join("; ")}</span>}
        </p>
        <CallBody approval={approval} />
      </div>
    </Section>
  );
}

function LastMessage({ paneId }: { paneId: string }) {
  const messages = useAgentMessages(paneId);
  // Without the SAY line and crowsnest's quoted headers: the agent's words only (R10 Fable).
  const raw = lastAssistantText(messages.data?.messages);
  const text = raw ? quotedReply(raw) : "";
  if (!text) return null;
  return (
    <Section title="The agent's last message">
      <Markdown className="text-xs">{text}</Markdown>
    </Section>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section aria-label={title} className="flex flex-col gap-1.5">
      <h2 className="text-2xs font-semibold uppercase tracking-wide text-muted">{title}</h2>
      {children}
    </section>
  );
}

function StaleActions() {
  return (
    <span className="flex gap-1">
      <Link to="/inbox" className="inline-flex min-h-9 items-center gap-1 rounded px-2 text-xs text-accent hover:underline md:min-h-6">
        <Inbox className="h-3.5 w-3.5" aria-hidden />
        Inbox
      </Link>
      <Link to="/" className="inline-flex min-h-9 items-center gap-1 rounded px-2 text-xs text-accent hover:underline md:min-h-6">
        <ExternalLink className="h-3.5 w-3.5" aria-hidden />
        Now
      </Link>
    </span>
  );
}

/** After this long a decision still loading says so, with Retry: never a skeleton for good (R10 Opus B6). */
const SLOW_LOAD_MS = 5_000;

function Loading({ what, paused, retry }: { what: string; paused: boolean; retry: () => void }) {
  const [slow, setSlow] = useState(false);
  useEffect(() => {
    const timer = setTimeout(() => setSlow(true), SLOW_LOAD_MS);
    return () => clearTimeout(timer);
  }, []);
  if (paused || slow) {
    return (
      <div className="p-3">
        <Banner tone="warning" role="alert" action={<Button compact icon={RefreshCw} onClick={retry}>Retry</Button>}>
          {paused ? `Cannot load ${what}: this browser is offline. It loads when the connection is back.` : `Still loading ${what}: the server has not answered.`}
        </Banner>
      </div>
    );
  }
  return (
    <div className="p-3">
      <SkeletonRows rows={6} label="Loading the decision" />
    </div>
  );
}

function LoadError({ what, message, retry }: { what: string; message: string; retry: () => void }) {
  return (
    <div className="p-3">
      <Banner tone="danger" role="alert" action={<Button compact icon={RefreshCw} onClick={retry}>Retry</Button>}>
        Could not load {what}: {message}
      </Banner>
    </div>
  );
}

function NotADecision() {
  return (
    <div className="p-3">
      <Banner tone="info" role="status" action={<StaleActions />}>
        This link does not name a decision. Open Now or the Inbox to see what needs you.
      </Banner>
    </div>
  );
}
