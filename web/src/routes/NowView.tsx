// Now, the home view (DESIGN.md 12 and 22; R4 Fable 1.2, Codex C1): the answer to "what needs me,
// what is each project doing, and what happened while I was away", without opening Chat.
//
//   1. Needs you: the attention queue (the badge's own items and number), each answerable in
//      place with the tray's row (an approval's card, a question's options, a dialog's digits).
//   2. Projects: one row per project (lead state, what it is doing in a line, running / waiting on
//      you / done today, spend today, last activity, who owns the work), then unfiled work and
//      the orchestrator. A row opens its project (unfiled: the board; the orchestrator: Chat);
//      beside a standing agent's row, "Open in Claude" when its Remote Control link is known.
//   3. Since you left: what finished, failed or was asked since this browser was last in use,
//      kept per browser on the server, with "Mark all seen".
//   4. Give a job, with the project it goes to.
//
// On a phone the sections stack in that order; from lg the queue and the projects sit side by
// side, so the first screen answers all three questions.
import { Link, useNavigate } from "@tanstack/react-router";
import { Bot, CheckCheck, CheckCircle2, ChevronRight, CircleAlert, Clock, FolderKanban, Gauge, HelpCircle, Inbox, XCircle } from "lucide-react";
import { useMemo, useState } from "react";
import { NewJobForm } from "../components/jobs/NewJobForm";
import { VoiceJob } from "../components/jobs/VoiceJob";
import { OpenInClaude } from "../components/remote/OpenInClaude";
import { TrayList } from "../components/tray/NeedsYouTray";
import { useTrayItems } from "../components/tray/useTrayItems";
import { Banner } from "../components/ui/Banner";
import { Button } from "../components/ui/Button";
import { Chip } from "../components/ui/Chip";
import { EmptyState } from "../components/ui/EmptyState";
import { SkeletonRows } from "../components/ui/Skeleton";
import { browserId, type NowRow, type SinceEvent } from "../lib/attention";
import { endpoints } from "../lib/api";
import { cn } from "../lib/cn";
import { relativeTime } from "../lib/format";
import { useOrchestrator } from "../lib/conversation-queries";
import { leadRunning } from "../lib/projects";
import { findProject, useAttention, useNow, useNowSince, useProjects } from "../lib/queries";
import type { RemoteControlFields } from "../lib/remote";
import { formatUSD } from "../lib/spend";
import { useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "../lib/api";

export function NowView() {
  return (
    <div className="mx-auto flex w-full max-w-7xl flex-col gap-4 p-3 md:p-4">
      <NowHeader />
      <div className="grid gap-4 lg:grid-cols-[minmax(0,5fr)_minmax(0,6fr)]">
        <QueueSection />
        <div className="flex min-w-0 flex-col gap-4">
          <ProjectsSection />
          <SinceSection />
        </div>
      </div>
      <GiveJobSection />
    </div>
  );
}

function NowHeader() {
  const attention = useAttention();
  const counts = attention.data?.counts;
  return (
    <header className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <h1 className="flex items-center gap-2 text-lg font-semibold text-heading">
        <Gauge className="h-5 w-5 text-muted" aria-hidden />
        Now
      </h1>
      {counts && (
        <p className="text-sm text-body" data-now-summary>
          {counts.total === 0 ? "Nothing needs you." : `${counts.total} need you: ${counts.blocking} blocking, ${counts.can_wait} can wait.`}
          {counts.snoozed > 0 && <span className="text-muted"> {counts.snoozed} snoozed.</span>}
        </p>
      )}
    </header>
  );
}

// ---- 1. Needs you -----------------------------------------------------------------------------

function QueueSection() {
  const attention = useAttention();
  const items = useTrayItems();
  return (
    <section aria-labelledby="now-queue" className="min-w-0 rounded-lg border border-border bg-surface">
      <SectionHeader id="now-queue" icon={Inbox} title="Needs you" count={attention.data?.counts.total}>
        <Link to="/inbox" className="inline-flex min-h-11 min-w-11 items-center justify-end text-xs text-accent hover:underline md:min-h-0 md:min-w-0">
          Inbox
        </Link>
      </SectionHeader>
      {attention.isPending ? (
        <SkeletonRows rows={4} label="Loading what needs you" />
      ) : attention.isError ? (
        <Banner tone="danger" role="alert">
          Could not load what needs you: {attention.error.message}
        </Banner>
      ) : items.length === 0 ? (
        <EmptyState icon={CheckCheck} title="Nothing needs you">
          Every agent is working, waiting on something else, or done.
        </EmptyState>
      ) : (
        <TrayList items={items} fresh={new Set()} />
      )}
    </section>
  );
}

// ---- 2. Projects ------------------------------------------------------------------------------

function ProjectsSection() {
  const now = useNow();
  return (
    <section aria-labelledby="now-projects" className="min-w-0 rounded-lg border border-border bg-surface">
      <SectionHeader id="now-projects" icon={FolderKanban} title="Projects">
        <Link to="/projects" className="inline-flex min-h-11 min-w-11 items-center justify-end text-xs text-accent hover:underline md:min-h-0 md:min-w-0">
          All projects
        </Link>
      </SectionHeader>
      {now.isPending ? (
        <SkeletonRows rows={4} label="Loading projects" />
      ) : now.isError ? (
        <Banner tone="danger" role="alert">
          Could not load the projects: {now.error.message}
        </Banner>
      ) : (
        <ul aria-label="Projects" className="divide-y divide-border">
          {now.data.rows.map((row) => (
            <ProjectRow key={row.kind + (row.project?.slug ?? "")} row={row} />
          ))}
        </ul>
      )}
    </section>
  );
}

/** Where a row opens: its project, the unfiled board, or Chat with the orchestrator. */
function rowTarget(row: NowRow): { to: string; params?: Record<string, string>; search?: Record<string, string> } {
  if (row.kind === "project" && row.project) return { to: "/projects/$slug", params: { slug: row.project.slug } };
  if (row.kind === "unfiled") return { to: "/jobs", search: { project: "unfiled" } };
  return { to: "/chat" };
}

function rowName(row: NowRow): string {
  if (row.kind === "project") return row.project?.name ?? "";
  return row.kind === "unfiled" ? "Unfiled" : "Orchestrator";
}

const leadTone: Record<string, "success" | "warning" | "danger" | "neutral" | "accent"> = {
  idle: "success",
  working: "accent",
  starting: "neutral",
  restarting: "warning",
  blocked: "danger",
  needs_terminal: "danger",
  failed: "danger",
  off: "neutral",
  stopped: "neutral",
};

/** The Remote Control fields of a row's standing agent: the orchestrator's, or its project's lead's. */
function useRowRemote(row: NowRow): RemoteControlFields | undefined {
  const orchestrator = useOrchestrator();
  const projects = useProjects();
  if (row.kind === "orchestrator") return orchestrator.data;
  if (row.kind !== "project") return undefined;
  const lead = findProject(projects.data?.projects, row.project?.slug)?.lead;
  return lead && leadRunning(lead) ? lead : undefined;
}

function ProjectRow({ row }: { row: NowRow }) {
  const navigate = useNavigate();
  const target = rowTarget(row);
  const name = rowName(row);
  const leadLabel = row.kind === "orchestrator" ? "orchestrator" : row.kind === "project" ? "lead" : null;
  const remote = useRowRemote(row);
  return (
    <li data-now-row={row.project?.slug ?? row.kind} className="flex items-start">
      <button
        type="button"
        onClick={() => void navigate({ to: target.to, params: target.params, search: target.search } as never)}
        className="flex min-h-14 min-w-0 flex-1 items-start gap-2 px-3 py-2.5 text-left hover:bg-hover focus-visible:bg-hover"
      >
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-center gap-1.5">
            <span className="text-sm font-semibold text-heading">{name}</span>
            {row.lead && leadLabel && (
              <Chip tone={leadTone[row.lead.state] ?? "neutral"}>
                {leadLabel} {row.lead.state.replace("_", " ")}
              </Chip>
            )}
            {row.counts.waiting > 0 && <Chip tone="danger">{row.counts.waiting} waiting on you</Chip>}
          </span>
          <span className="block truncate pt-0.5 text-xs text-body" data-doing>
            {row.doing}
          </span>
          <span className="flex flex-wrap items-center gap-x-3 gap-y-0.5 pt-1 text-2xs text-muted">
            {row.kind !== "orchestrator" && (
              <>
                <span>{row.counts.running} running</span>
                <span>{row.counts.done_today} done today</span>
                {row.counts.failed_today > 0 && <span className="text-danger">{row.counts.failed_today} failed today</span>}
              </>
            )}
            <span>{row.spend_today_usd === null ? "no spend today" : `${formatUSD(row.spend_today_usd)} est. today`}</span>
            {row.last_activity_at && <span>active {relativeTime(row.last_activity_at)}</span>}
          </span>
          <span className="block pt-1 text-2xs leading-snug text-muted" data-owner>
            {row.owner}
          </span>
        </span>
        <ChevronRight className="mt-1 h-4 w-4 shrink-0 text-muted" aria-hidden />
      </button>
      {/* Beside the row's button, not in it: a link inside a button is not allowed. */}
      <OpenInClaude status={remote} variant="icon" noFallback className="shrink-0 py-2.5 pr-2" />
    </li>
  );
}

// ---- 3. Since you left ------------------------------------------------------------------------

const sinceIcons: Record<SinceEvent["kind"], typeof CheckCircle2> = {
  done: CheckCircle2,
  failed: XCircle,
  cancelled: CircleAlert,
  asked: HelpCircle,
};

const sinceWords: Record<SinceEvent["kind"], string> = { done: "Finished", failed: "Failed", cancelled: "Cancelled", asked: "Asked" };

function SinceSection() {
  const browser = useMemo(() => browserId(), []);
  const since = useNowSince(browser);
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [marking, setMarking] = useState(false);
  const markSeen = async () => {
    setMarking(true);
    try {
      await endpoints.nowSeen(browser);
      await queryClient.invalidateQueries({ queryKey: queryKeys.nowSince(browser) });
    } finally {
      setMarking(false);
    }
  };
  const events = since.data?.events ?? [];
  const open = (event: SinceEvent) => {
    if (event.kind === "asked" && event.ref) void navigate({ to: "/inbox", search: { q: event.ref } });
    else if (event.job_id) void navigate({ to: "/jobs", search: { job: event.job_id } });
  };
  return (
    <details open className="group min-w-0 rounded-lg border border-border bg-surface" aria-labelledby="now-since">
      <summary className="flex cursor-pointer list-none items-center gap-2 border-b border-border px-3 py-2 [&::-webkit-details-marker]:hidden">
        <Clock className="h-4 w-4 text-muted" aria-hidden />
        <h2 id="now-since" className="text-sm font-semibold text-heading">
          Since you left
        </h2>
        {since.data && (
          <span className="text-xs text-muted">
            {since.data.first ? "first visit: the last 12 hours" : `since ${relativeTime(since.data.since)}`} · {events.length}
          </span>
        )}
        <ChevronRight className="ml-auto h-4 w-4 text-muted transition-transform group-open:rotate-90" aria-hidden />
      </summary>
      {since.isPending ? (
        <SkeletonRows rows={3} label="Loading what happened" />
      ) : events.length === 0 ? (
        <p className="px-3 py-3 text-xs text-muted">Nothing finished, failed or was asked since you were last here.</p>
      ) : (
        <>
          <ul aria-label="Since you left" className="divide-y divide-border">
            {events.map((event) => {
              const Icon = sinceIcons[event.kind];
              return (
                <li key={`${event.kind}:${event.job_id ?? ""}:${event.ref ?? ""}:${event.at}`}>
                  <button type="button" onClick={() => open(event)} className="flex w-full min-h-11 items-start gap-2 px-3 py-2 text-left hover:bg-hover">
                    <Icon
                      className={cn("mt-0.5 h-4 w-4 shrink-0", event.kind === "done" ? "text-success" : event.kind === "failed" ? "text-danger" : event.kind === "asked" ? "text-accent" : "text-warning")}
                      aria-hidden
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block break-words text-sm text-heading">{event.title}</span>
                      <span className="block truncate text-2xs text-muted">
                        {sinceWords[event.kind]} · {event.project?.name ?? "Unfiled"} · {relativeTime(event.at)}
                        {event.detail && event.kind !== "asked" ? ` · ${event.detail}` : ""}
                      </span>
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
          <div className="flex justify-end border-t border-border px-2 py-1">
            <Button compact variant="ghost" icon={CheckCheck} disabled={marking} onClick={() => void markSeen()}>
              Mark all seen
            </Button>
          </div>
        </>
      )}
    </details>
  );
}

// ---- 4. Give a job ----------------------------------------------------------------------------

function GiveJobSection() {
  const projects = useProjects();
  const [slug, setSlug] = useState("");
  const project = slug ? findProject(projects.data?.projects, slug) : undefined;
  const live = (projects.data?.projects ?? []).filter((candidate) => !candidate.archived);
  return (
    <section aria-labelledby="now-give" className="min-w-0 rounded-lg border border-border bg-surface">
      <SectionHeader id="now-give" icon={Bot} title="Give a job">
        <label className="flex items-center gap-1.5 text-xs text-muted">
          For
          <select
            aria-label="Who gets the job"
            value={slug}
            onChange={(event) => setSlug(event.target.value)}
            className="h-11 rounded border border-border-strong bg-surface px-1.5 text-xs text-heading md:h-7"
          >
            <option value="">The orchestrator (no project)</option>
            {live.map((candidate) => (
              <option key={candidate.slug} value={candidate.slug}>
                {candidate.name}
              </option>
            ))}
          </select>
        </label>
      </SectionHeader>
      <VoiceJob projects={live} chosen={slug} />
      <NewJobForm project={project} />
    </section>
  );
}

// ---- Shared -----------------------------------------------------------------------------------

function SectionHeader({ id, icon: Icon, title, count, children }: { id: string; icon: typeof Inbox; title: string; count?: number; children?: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2 border-b border-border px-3 py-2">
      <Icon className="h-4 w-4 text-muted" aria-hidden />
      <h2 id={id} className="text-sm font-semibold text-heading">
        {title}
      </h2>
      {count !== undefined && count > 0 && (
        <span className="inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-status-blocked px-1.5 text-2xs font-semibold tabular-nums text-white dark:text-surface">
          {count}
        </span>
      )}
      <span className="ml-auto flex items-center gap-2">{children}</span>
    </div>
  );
}
