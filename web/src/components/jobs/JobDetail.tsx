// JobDetail: one job in full (DESIGN.md 12): what it is and where it runs, why it is stuck and
// the one action that fits, the result with what was verified and what was not, its questions
// (answerable right here with the Inbox's components), a message box to its agent, the
// timeline, and the agent's Conversation and Terminal (the Fleet view's slots).
import { useNavigate } from "@tanstack/react-router";
import { Ban, CheckCircle2, CircleSlash, MessageSquare, RefreshCw, SquareTerminal, Zap } from "lucide-react";
import { useState } from "react";
import { relativeTime } from "../../lib/format";
import { actionFor, actionLabels, isFinished, jobAnswerText, retryLabel, stateChip, type JobDetail as Detail, type JobEvent } from "../../lib/jobs";
import { useFleet, useJob, useJobAction } from "../../lib/queries";
import { AgentConversation } from "../agent/AgentConversation";
import { AgentTerminal } from "../agent/AgentTerminal";
import { QuestionDetail } from "../inbox/QuestionDetail";
import { JobFiles, JobMessageBox } from "./JobMessageBox";
import { Markdown } from "../Markdown";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Chip } from "../ui/Chip";
import { EmptyState } from "../ui/EmptyState";
import { SkeletonRows } from "../ui/Skeleton";
import { TabPanel, Tabs } from "../ui/Tabs";
import { useToast } from "../ui/toast-context";

type DetailTab = "overview" | "conversation" | "terminal";

export function JobDetail({ id }: { id: string }) {
  const query = useJob(id);
  const [tab, setTab] = useState<DetailTab>("overview");
  if (query.isPending) return <SkeletonRows rows={8} label="Loading the job" />;
  if (query.isError) {
    return (
      <Banner tone="danger" role="alert" action={<Button compact icon={RefreshCw} onClick={() => void query.refetch()}>Retry</Button>}>
        Could not load {id}: {query.error.message}
      </Banner>
    );
  }
  const job = query.data;
  return (
    <article aria-label={job.title} className="flex min-h-full max-w-4xl flex-col gap-3 p-3 md:p-4">
      <JobHeader job={job} />
      <Tabs<DetailTab>
        id={`job-${job.id}`}
        label="Job views"
        value={tab}
        onChange={setTab}
        items={[
          { value: "overview", label: "Overview" },
          { value: "conversation", label: "Conversation", icon: MessageSquare },
          { value: "terminal", label: "Terminal", icon: SquareTerminal },
        ]}
      />
      <TabPanel id={`job-${job.id}`} value={tab} className="flex min-h-0 flex-1 flex-col gap-3">
        {tab === "overview" && <Overview job={job} openTerminal={() => setTab("terminal")} />}
        {tab === "conversation" && <AgentPanel job={job} kind="conversation" />}
        {tab === "terminal" && <AgentPanel job={job} kind="terminal" />}
      </TabPanel>
    </article>
  );
}

function JobHeader({ job }: { job: Detail }) {
  const chip = stateChip(job);
  return (
    <header className="flex flex-col gap-1.5">
      <h2 className="break-words text-base font-semibold leading-snug text-heading">{job.title}</h2>
      <div className="flex flex-wrap items-center gap-1.5">
        <Chip tone={chip.tone}>{chip.label}</Chip>
        <Chip mono>{job.id}</Chip>
        <Chip>{job.role}</Chip>
        {job.engine !== "claude" && <Chip>{job.engine}</Chip>}
        {job.model && <Chip mono>{job.model}</Chip>}
        {job.agent_name && <Chip mono>{job.agent_name}</Chip>}
        {job.parent_id && <Chip mono title="Delegated by">from {job.parent_id}</Chip>}
        {job.nudges > 0 && <Chip tone="warning">{job.nudges} nudges</Chip>}
        {job.restarts > 0 && <Chip tone="warning">{job.restarts} restarts</Chip>}
      </div>
      <p className="break-all font-mono text-2xs text-muted">
        {job.work_dir || job.dir || "orchestrator"}
        {job.worktree_branch && ` · ${job.worktree_branch}`}
      </p>
      {!isFinished(job.state) && job.doing_now && (
        <p className="text-sm text-body">
          <span className="text-muted">Doing now: </span>
          {job.doing_now}
          {job.last_activity_at && <span className="text-2xs text-muted"> · active {relativeTime(job.last_activity_at)}</span>}
        </p>
      )}
    </header>
  );
}

// ---- Overview ------------------------------------------------------------------------------

function Overview({ job, openTerminal }: { job: Detail; openTerminal: () => void }) {
  const open = job.questions.filter((question) => question.status === "open");
  const settled = job.questions.filter((question) => question.status !== "open");
  return (
    <>
      <StuckBanner job={job} openTerminal={openTerminal} />
      {job.state === "blocked" && <Banner tone="warning">Blocked: {job.blocked_reason}</Banner>}
      {job.state === "done" && <Result job={job} />}
      {job.state === "failed" && (
        <Banner tone="danger" icon={CircleSlash}>
          Failed: {job.fail_reason}
        </Banner>
      )}
      {open.map((question) => (
        <Card key={question.id} title={`Question ${question.id}`} tone="danger" bodyClassName="p-0">
          <QuestionDetail id={question.id} />
        </Card>
      ))}
      {!isFinished(job.state) && job.pane_id && <JobMessageBox job={job} />}
      <Card title="Brief">
        <Markdown className="text-sm">{job.brief_md}</Markdown>
        {job.done_when && <p className="mt-2 text-xs text-muted">Done when: {job.done_when}</p>}
      </Card>
      <JobFiles jobId={job.id} />
      {settled.length > 0 && (
        <Card title="Answered questions">
          <ul className="flex flex-col gap-1 text-xs">
            {settled.map((question) => (
              <li key={question.id}>
                <span className="font-mono text-muted">{question.id}</span> {question.title} <span className="text-muted">→ {jobAnswerText(question)}</span>
              </li>
            ))}
          </ul>
        </Card>
      )}
      <Timeline events={job.timeline} />
      {!isFinished(job.state) && <CancelJob id={job.id} />}
    </>
  );
}

/** Why the job is stuck, in words, and the one action that fits it. */
function StuckBanner({ job, openTerminal }: { job: Detail; openTerminal: () => void }) {
  const action = actionFor(job);
  const run = useJobAction(job.id);
  const navigate = useNavigate();
  const toast = useToast();
  if (!action || (!job.stuck && job.state !== "needs_you")) return null;
  const retry = retryLabel(job);
  const press = () => {
    if (action === "terminal") return openTerminal();
    if (action === "inbox") return void navigate({ to: "/inbox", search: { q: job.questions.find((question) => question.status === "open")?.id } });
    // mutateAsync: a nudge that works clears the stuck flag, and the refetch removes this banner
    // before mutate's own callbacks would run (see DialogCard).
    run.mutateAsync(action === "retry" || action === "resume" ? "resume" : "nudge").then(
      () => toast.show({ tone: "success", title: actionLabels[action] + ": done" }),
      (error: unknown) => toast.show({ tone: "danger", title: actionLabels[action] + " failed", description: error instanceof Error ? error.message : String(error) }),
    );
  };
  return (
    <Banner
      tone={job.state === "needs_you" ? "danger" : "warning"}
      role="alert"
      action={
        <Button compact variant="warning" icon={action === "terminal" ? SquareTerminal : action === "nudge" ? Zap : RefreshCw} onClick={press} disabled={run.isPending}>
          {actionLabels[action]}
        </Button>
      }
    >
      <strong className="font-semibold">{job.cause_text}</strong>
      {job.cause_detail && <span>: {job.cause_detail}</span>}
      {retry && <span className="block text-xs">{retry} (attempt {job.retry_count + 1})</span>}
    </Banner>
  );
}

function Result({ job }: { job: Detail }) {
  return (
    <Card title="Result" actions={<CheckCircle2 className="h-4 w-4 text-success" aria-label="Done" />}>
      <Markdown className="text-sm">{job.result_md}</Markdown>
      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        <Evidence title="Verified" items={job.verified} empty="Nothing was verified." tone="text-success" />
        <Evidence title="Not verified" items={job.not_verified} empty="Nothing left unverified." tone="text-warning" />
      </div>
    </Card>
  );
}

function Evidence({ title, items, empty, tone }: { title: string; items: string[]; empty: string; tone: string }) {
  return (
    <section aria-label={title}>
      <h3 className={`mb-1 text-xs font-semibold ${tone}`}>{title}</h3>
      {items.length === 0 ? (
        <p className="text-xs text-muted">{empty}</p>
      ) : (
        <ul className="list-disc pl-4 text-xs text-body">
          {items.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      )}
    </section>
  );
}

const eventTones: Record<string, string> = {
  nudge: "text-warning",
  stuck: "text-warning",
  fail: "text-danger",
  done: "text-success",
  restart: "text-warning",
  retry: "text-warning",
  question: "text-accent",
};

function Timeline({ events }: { events: JobEvent[] }) {
  return (
    <Card title="Timeline" bodyClassName="p-0">
      <ol aria-label="Timeline" className="divide-y divide-border">
        {[...events].reverse().map((event) => (
          <li key={event.id} className="flex gap-2 px-3 py-1.5 text-xs">
            <time dateTime={event.at} title={new Date(event.at).toLocaleString()} className="w-16 shrink-0 text-2xs text-muted">
              {relativeTime(event.at)}
            </time>
            <span className={`w-16 shrink-0 font-mono text-2xs ${eventTones[event.kind] ?? "text-muted"}`}>{event.kind}</span>
            <span className="min-w-0 flex-1 break-words text-body">{event.summary}</span>
          </li>
        ))}
      </ol>
    </Card>
  );
}

function CancelJob({ id }: { id: string }) {
  const [confirming, setConfirming] = useState(false);
  const run = useJobAction(id);
  const toast = useToast();
  if (!confirming) {
    return (
      <Button compact variant="ghost" icon={Ban} className="self-start" onClick={() => setConfirming(true)}>
        Cancel job
      </Button>
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs text-body">
      Cancel {id}? Esc is sent to the agent; its pane stays open.
      <Button
        compact
        variant="danger"
        disabled={run.isPending}
        onClick={() => run.mutate("cancel", { onError: (error) => toast.show({ tone: "danger", title: "Not cancelled", description: error.message }) })}
      >
        Cancel the job
      </Button>
      <Button compact variant="ghost" onClick={() => setConfirming(false)}>
        Keep it
      </Button>
    </div>
  );
}

// ---- Conversation and terminal -----------------------------------------------------------

function AgentPanel({ job, kind }: { job: Detail; kind: "conversation" | "terminal" }) {
  const fleet = useFleet();
  const agent = fleet.data?.agents.find((entry) => entry.pane_id === job.pane_id);
  if (!job.pane_id) {
    return (
      <EmptyState icon={MessageSquare} title="No agent yet">
        This job has not been given an agent.
      </EmptyState>
    );
  }
  if (!agent) {
    if (fleet.isPending) return <SkeletonRows rows={4} label="Finding the agent" />;
    return (
      <EmptyState icon={MessageSquare} title="The agent is not running">
        {job.agent_name || job.pane_id} is not in the session any more.
      </EmptyState>
    );
  }
  return <div className="flex min-h-[28rem] flex-1 flex-col">{kind === "conversation" ? <AgentConversation agent={agent} /> : <AgentTerminal agent={agent} />}</div>;
}
