// ReviewPanel: a job's Review tab (DESIGN.md 6.11, 6.21). What the branch produced (commits
// ahead of its base, files and lines, whether the tree is clean and the branch pushed), the live
// diff viewer (components/changes: syntax-highlighted, with Viewed ticks tied to each file's
// content, "N files not viewed" here and in Land's confirmation); beside it the agent's own account
// (result, evidence, walkthrough) and the review rounds; and the Land actions, each behind a
// confirmation that says exactly what will happen. Diff text is rendered as text nodes only.
// Line comments left in the viewer have their own card here: how many wait to be sent, when they
// were last sent, and "Send N comments to the agent"; with Settings' "Require all files viewed
// before Land" on, unsent ones are named in Land's confirmation (a warning, never a block).
import { AlertTriangle, Eye, GitBranch, GitMerge, GitPullRequest, Trash2 } from "lucide-react";
import { Link } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { cn } from "../../lib/cn";
import { confirmLines, landActions, landLabels, refusedForDirtyCheckout, verdictText, type LandAction, type ReviewView } from "../../lib/landing";
import { acceptChip } from "../../lib/accept";
import { isFinished, landedWord, type JobDetail } from "../../lib/jobs";
import { whereText } from "../../lib/comments";
import { useLand, useLandOverride, useProjects, useReview, useTeardown } from "../../lib/queries";
import { useDiffSummary } from "../../lib/changes-queries";
import { useComments } from "../../lib/comments-queries";
import { SendForm } from "../changes/Comments";
import { EmbeddedDiff } from "../changes/JobChanges";
import { Markdown } from "../Markdown";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { ChangesCard } from "./AcceptPanels";
import { Chip } from "../ui/Chip";
import { Revealed } from "../ui/Revealed";
import { SkeletonRows } from "../ui/Skeleton";
import { useToast } from "../ui/toast-context";
import { HelpButton } from "../help/HelpButton";

const actionIcons = { merge: GitMerge, pr: GitPullRequest, discard: Trash2 } as const;

export function ReviewPanel({ job }: { job: JobDetail }) {
  const query = useReview(job.id);
  if (query.isPending) return <SkeletonRows rows={6} label="Reading the worktree" />;
  if (query.isError) return <Banner tone="danger">Could not read the review: {query.error.message}</Banner>;
  const view = query.data;
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <ChangesCard job={job} />
      <LandCard job={job} view={view} />
      {view.produced && <EmbeddedDiff who={{ kind: "job", id: job.id }} />}
      {view.produced && !view.land.state && <ReviewComments job={job} />}
      <div className="grid grid-cols-[minmax(0,1fr)] gap-3 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        <div className="flex min-w-0 flex-col gap-3">
          <Summary view={view} />
        </div>
        <aside aria-label="The agent's account" className="flex min-w-0 flex-col gap-3">
          {view.reviews.length > 0 && <Reviews view={view} />}
          {view.walkthrough_md && (
            <Card title="Walkthrough">
              <Markdown className="text-sm">{view.walkthrough_md}</Markdown>
            </Card>
          )}
          <Card title="Result">
            {view.result_md ? <Markdown className="text-sm">{view.result_md}</Markdown> : <p className="text-xs text-muted">No result yet.</p>}
            <EvidenceList title="Verified" items={view.verified} tone="text-success" />
            <EvidenceList title="Not verified" items={view.not_verified} tone="text-warning" />
          </Card>
        </aside>
      </div>
    </div>
  );
}

function EvidenceList({ title, items, tone }: { title: string; items: string[]; tone: string }) {
  if (items.length === 0) return null;
  return (
    <section aria-label={title} className="mt-2">
      <h3 className={cn("text-xs font-semibold", tone)}>{title}</h3>
      <ul className="list-disc pl-4 text-xs text-body">
        {items.map((item) => (
          <li key={item}>{item}</li>
        ))}
      </ul>
    </section>
  );
}

// ---- Comments ------------------------------------------------------------------------------

/** The job's line comments: the count, when they were last sent, and Send (the list is the viewer's). */
function ReviewComments({ job }: { job: JobDetail }) {
  const who = { kind: "job", id: job.id } as const;
  const comments = useComments(who);
  if (!comments.data || comments.data.comments.length === 0) {
    return (
      <p className="flex items-center gap-1 text-2xs text-muted">
        Comment on a line, a range, a change or a file in the diff above (select a line number, then c), and send them to the agent as one message.
        <HelpButton topic="how-to/review-changes#comments" what="line comments" />
      </p>
    );
  }
  return (
    <Card title="Comments" actions={<HelpButton topic="how-to/review-changes#comments" what="line comments" />}>
      <SendForm who={who} list={comments.data} />
    </Card>
  );
}

// ---- Land ----------------------------------------------------------------------------------

function LandCard({ job, view }: { job: JobDetail; view: ReviewView }) {
  const [confirming, setConfirming] = useState<LandAction | null>(null);
  const [override, setOverride] = useState(false);
  // A refusal stays in the card with the way out, not only in a toast that disappears (R10 Opus B7).
  const [refusal, setRefusal] = useState<{ action: LandAction; message: string } | null>(null);
  const confirmRef = useRef<HTMLDivElement>(null);
  const options = view.land;
  const plainLand = useLand(job.id, options.head);
  const finished = isFinished(job.state) || (job.state === "needs_you" && job.cause === "review");
  // What Land merges is the committed work: its files not yet marked viewed (a warning, unless
  // Settings require them all viewed; then the server refuses with UNVIEWED).
  const committed = useDiffSummary({ kind: "job", id: job.id }, "committed", "", finished && !options.state && !options.torn_down);
  const unviewed = committed.data ? committed.data.total - committed.data.viewed : 0;
  const comments = useComments({ kind: "job", id: job.id }, !options.state && !!view.require_viewed);
  const unsent = view.require_viewed ? (comments.data?.drafts ?? 0) : 0;
  const overrideLand = useLandOverride(job.id, options.head);
  const projects = useProjects();
  const project = job.project_id ? projects.data?.projects.find((candidate) => candidate.id === job.project_id) : undefined;
  const teardown = useTeardown();
  const toast = useToast();
  // The confirmation opens where the buttons were; bring "Yes, merge" into view on a phone.
  useEffect(() => {
    if (confirming) confirmRef.current?.scrollIntoView?.({ block: "nearest" });
  }, [confirming]);
  if (options.state) {
    return (
      <Card title="Landed">
        <p className="text-sm text-body">
          <strong className="font-semibold">{landedWord(options.state)}</strong>
          {options.detail && (
            <>
              :{" "}
              {options.detail.startsWith("https://") ? (
                <a href={options.detail} target="_blank" rel="noopener noreferrer" className="text-accent underline">
                  {options.detail}
                </a>
              ) : (
                options.detail
              )}
            </>
          )}
        </p>
        {!options.torn_down && (
          <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-warning">
            <AlertTriangle className="h-4 w-4" aria-hidden /> The worktree is still there (see the timeline for why).
            <Button
              compact
              disabled={teardown.isPending}
              onClick={() =>
                teardown.mutate(job.id, {
                  onSuccess: (result) =>
                    toast.show(result.torn_down ? { tone: "success", title: "Torn down" } : { tone: "warning", title: "Not torn down", description: result.teardown_error }),
                  onError: (error) => toast.show({ tone: "danger", title: "Teardown failed", description: error.message }),
                })
              }
            >
              Tear down again
            </Button>
          </div>
        )}
      </Card>
    );
  }
  // While the job works, Land is one line: nothing can land yet, and the diff belongs above the
  // fold (R10 Fable F1: three disabled buttons and three sentences explaining them came first).
  if (!finished) {
    return (
      <p className="flex min-w-0 items-center gap-1.5 text-xs text-muted" data-testid="land-pending">
        <GitMerge className="h-3.5 w-3.5 shrink-0" aria-hidden />
        <span className="min-w-0 truncate">
          Lands when it finishes: <span className="font-mono"><Revealed text={options.branch} /> → <Revealed text={options.base || "?"} /></span>
        </span>
        <HelpButton topic="concepts/worktrees-and-landing#landing" what="landing" />
      </p>
    );
  }
  // crowsnest's evidence does not support this revision: merge and push need the explicit override.
  const blocked = (action: LandAction) => !!options.evidence && action !== "discard";
  const land = confirming && blocked(confirming) ? overrideLand : plainLand;
  const run = (action: LandAction) => {
    setRefusal(null);
    land.mutate(action, {
      onSuccess: (result) => {
        setConfirming(null);
        toast.show(
          result.teardown_error
            ? { tone: "warning", title: `${landLabels[action]}: done, teardown incomplete`, description: result.teardown_error }
            : { tone: "success", title: `${landLabels[action]}: done`, description: result.detail },
        );
      },
      onError: (error) => {
        setConfirming(null);
        setRefusal({ action, message: error.message });
      },
    });
  };
  const open = (action: LandAction) => {
    setOverride(false);
    setRefusal(null);
    setConfirming(action);
  };
  const reasons = landActions.filter((action) => options.why?.[action]);
  return (
    <Card
      title="Land"
      actions={
        <span className="flex min-w-0 items-center gap-1">
          <span className="min-w-0 truncate font-mono text-2xs text-muted">
            <Revealed text={options.branch} /> → <Revealed text={options.base || "?"} />
          </span>
          {/* What Accepted proves and what it does not: behind the "?" (R10 Fable F9, Opus F13). */}
          <HelpButton topic="concepts/worktrees-and-landing#landing" what="landing" />
        </span>
      }
    >
      {options.request && (
        <Banner tone="warning">An agent asked to {options.request === "pr" ? "push it and open a pull request" : options.request + " it"}; answer in the Inbox, or land it here.</Banner>
      )}
      {options.evidence && (
        <Banner tone="warning">
          crowsnest's evidence does not support landing this revision: {options.evidence_why}. Run the check again (or ask an agent to rebase) in the
          Overview; landing it anyway needs an explicit override.
        </Banner>
      )}
      <LandFacts job={job} view={view} unviewed={unviewed} total={committed.data?.total} />
      {options.base_protected && (
        <p className="mb-2 text-xs text-warning">{options.base} is protected: Land never merges into it. Use the pull request.</p>
      )}
      {refusal && (
        <div role="alert" className="mb-2 flex flex-col gap-1.5 rounded border border-danger/50 bg-danger/5 p-2.5 text-xs text-danger" data-testid="land-refused">
          <p>
            <span className="font-semibold">{landLabels[refusal.action]} refused:</span> {refusal.message}
          </p>
          <span className="flex flex-wrap gap-2">
            {refusedForDirtyCheckout(refusal.message) && project && (
              <Link to="/projects/$slug" params={{ slug: project.slug }} className="inline-flex min-h-9 items-center rounded px-1 text-accent underline-offset-2 hover:underline md:min-h-6">
                Show the main checkout's changes
              </Link>
            )}
            <Button compact variant="secondary" disabled={land.isPending} onClick={() => open(refusal.action)}>
              Try again
            </Button>
          </span>
        </div>
      )}
      {confirming ? (
        <div ref={confirmRef} role="alertdialog" aria-label={`Confirm ${landLabels[confirming]}`} className="rounded border border-border-strong bg-surface-raised p-2.5 text-sm">
          <p className="font-semibold text-heading">{landLabels[confirming]}?</p>
          <ul className="mt-1 list-disc pl-4 text-xs text-body">
            {confirmLines(confirming, options.branch, options.base).map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
          {confirming !== "discard" && unviewed > 0 && (
            <p className="mt-1.5 text-xs text-warning">
              {unviewed} of {committed.data?.total} committed files are not marked viewed.
            </p>
          )}
          {confirming !== "discard" && unsent > 0 && (
            <p className="mt-1.5 text-xs text-warning">
              {unsent} comment{unsent === 1 ? " is" : "s are"} not sent to the agent yet:{" "}
              {comments.data?.comments
                .filter((comment) => comment.pending)
                .slice(0, 5)
                .map((comment) => `${comment.path} (${whereText(comment)})`)
                .join(", ")}
              {unsent > 5 ? ", …" : ""}. Landing does not send them.
            </p>
          )}
          {blocked(confirming) && (
            <label className="mt-1.5 flex items-start gap-2 text-xs text-warning">
              <input type="checkbox" checked={override} onChange={(event) => setOverride(event.target.checked)} className="mt-0.5" />
              <span>Land it anyway, over crowsnest's evidence ({options.evidence_why}).</span>
            </label>
          )}
          <details className="mt-1.5 text-2xs text-muted">
            <summary className="cursor-pointer">Exactly what happens</summary>
            <p className="mt-1 break-words">{options.confirm[confirming]}</p>
          </details>
          <div className="mt-2 grid grid-cols-2 gap-2 md:flex">
            <Button
              variant={confirming === "discard" ? "danger" : "primary"}
              disabled={land.isPending || (blocked(confirming) && !override)}
              onClick={() => run(confirming)}
            >
              {land.isPending ? "Working…" : `Yes, ${landLabels[confirming].toLowerCase()}`}
            </Button>
            <Button variant="secondary" onClick={() => setConfirming(null)}>
              Not now
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Land actions">
          {landActions
            .filter((action) => action !== "discard")
            .map((action) => (
              <Button
                key={action}
                compact
                variant={action === "merge" ? "primary" : undefined}
                icon={actionIcons[action]}
                disabled={!options.allowed[action] || land.isPending}
                title={options.why?.[action]}
                onClick={() => open(action)}
              >
                {landLabels[action]}
              </Button>
            ))}
          {/* Discard is quiet and apart: it is not the counterpart of Merge (R10 Fable F9, Opus F13). */}
          <button
            type="button"
            disabled={!options.allowed.discard || land.isPending}
            title={options.why?.discard}
            onClick={() => open("discard")}
            className="ml-auto inline-flex min-h-11 items-center gap-1 rounded px-2 text-xs text-danger hover:bg-danger/10 hover:underline disabled:opacity-50 md:min-h-7"
          >
            <Trash2 className="h-3.5 w-3.5" aria-hidden />
            {landLabels.discard}
          </button>
        </div>
      )}
      {!confirming && reasons.length > 0 && (
        <ul className="mt-2 text-2xs text-muted">
          {reasons.map((action) => (
            <li key={action}>
              {landLabels[action]}: {options.why?.[action]}
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

/**
 * The facts that decide a land, in one line: the reviewer's verdict, crowsnest's check and the
 * committed files not yet viewed ("committed": the diff viewer's own count covers uncommitted work
 * too, which Land does not merge; R10 Fable "counts disagree").
 */
function LandFacts({ job, view, unviewed, total }: { job: JobDetail; view: ReviewView; unviewed: number; total?: number }) {
  const verdict = view.reviews.at(-1);
  const check = acceptChip(job);
  if (!verdict && !check && unviewed === 0) return null;
  return (
    <p className="mb-2 flex flex-wrap items-center gap-1.5 text-xs">
      {verdict && <Chip tone={verdict.verdict === "approve" ? "success" : verdict.verdict === "" ? "accent" : "warning"}>Reviewer: {verdictText(verdict.verdict)}</Chip>}
      {check && (
        <Chip tone={check.tone} title={check.title}>
          Check: {check.label}
        </Chip>
      )}
      {unviewed > 0 && total !== undefined && (
        <span className="inline-flex items-center gap-1 text-warning">
          <Eye className="h-3.5 w-3.5" aria-hidden /> {unviewed} of {total} committed files not viewed
        </span>
      )}
    </p>
  );
}

// ---- What it produced ----------------------------------------------------------------------

function Summary({ view }: { view: ReviewView }) {
  if (view.inspect_error) return <Banner tone="warning">The worktree could not be read: {view.inspect_error}</Banner>;
  const produced = view.produced;
  if (!produced) return <Banner tone="info">The worktree is gone; the result and review are kept.</Banner>;
  return (
    <Card title="What it produced" actions={<GitBranch className="h-4 w-4 text-muted" aria-hidden />}>
      <div className="flex flex-wrap gap-1.5 text-xs">
        <Chip mono><Revealed text={produced.branch} /></Chip>
        <Chip>{produced.ahead} commit{produced.ahead === 1 ? "" : "s"} ahead of {produced.base || "base"}</Chip>
        {produced.behind > 0 && <Chip tone="warning">{produced.behind} behind</Chip>}
        <Chip>
          {produced.files.length} files <span className="text-success">+{produced.added}</span> <span className="text-danger">−{produced.deleted}</span>
        </Chip>
        <Chip tone={produced.clean ? "success" : "warning"}>{produced.clean ? "clean tree" : `${produced.dirty.length} uncommitted`}</Chip>
        <Chip tone={produced.pushed ? "success" : "neutral"}>{produced.pushed ? "pushed" : "not pushed"}</Chip>
      </div>
      {produced.commits.length > 0 && (
        <ul aria-label="Commits" className="mt-2 flex flex-col gap-0.5 text-xs">
          {produced.commits.map((commit) => (
            <li key={commit.hash} className="truncate">
              <span className="font-mono text-muted">{commit.hash.slice(0, 8)}</span> <Revealed text={commit.subject} />
            </li>
          ))}
        </ul>
      )}
      {!produced.clean && (
        <pre className="mt-2 overflow-x-auto rounded bg-surface-sunken p-2 font-mono text-2xs text-warning">{produced.dirty.join("\n")}</pre>
      )}
    </Card>
  );
}

// ---- Review rounds -------------------------------------------------------------------------

function Reviews({ view }: { view: ReviewView }) {
  return (
    <Card title="Review" actions={<HelpButton topic="concepts/acceptance#the-review-gate" what="the review gate" />}>
      <ol aria-label="Review rounds" className="flex flex-col gap-3">
        {view.reviews.map((round) => (
          <li key={round.job_id || `round-${round.round}`}>
            <div className="mb-1 flex flex-wrap items-center gap-1.5 text-xs">
              <span className="font-semibold text-heading">Round {round.round}</span>
              <Chip tone={round.verdict === "approve" ? "success" : round.verdict === "" ? "accent" : "warning"}>{verdictText(round.verdict)}</Chip>
              {round.job_id && <Chip mono>{round.job_id}</Chip>}
              {round.engine && <Chip>{round.engine}</Chip>}
            </div>
            {round.findings && <Markdown className="text-xs">{round.findings}</Markdown>}
          </li>
        ))}
      </ol>
    </Card>
  );
}
