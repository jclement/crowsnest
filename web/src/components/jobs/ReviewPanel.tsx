// ReviewPanel: a finished job's Review tab (DESIGN.md 6.11). What the branch produced (commits
// ahead of its base, files and lines, whether the tree is clean and the branch pushed), the diff
// per file in the order the change is best understood (the agent's walkthrough's order, then
// schema → logic → API → UI → tests → docs, churn last and collapsed), every file collapsed and
// fetched only when opened, binary files named and never shown; beside it the agent's own account
// (result, evidence, walkthrough) and the review rounds; and the Land actions, each behind a
// confirmation that says exactly what will happen. Diff text is rendered as text nodes only.
import { AlertTriangle, ChevronRight, FileCode2, GitBranch, GitMerge, GitPullRequest, Trash2 } from "lucide-react";
import { useState } from "react";
import { cn } from "../../lib/cn";
import { landActions, landLabels, parseUnified, verdictText, type FileChange, type LandAction, type ReviewView } from "../../lib/landing";
import { landedWord, type JobDetail } from "../../lib/jobs";
import { useLand, useLandOverride, useReview, useReviewDiff, useTeardown } from "../../lib/queries";
import { Markdown } from "../Markdown";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { AcceptedMeans } from "./AcceptPanels";
import { Chip } from "../ui/Chip";
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
      <LandCard job={job} view={view} />
      <div className="grid grid-cols-[minmax(0,1fr)] gap-3 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        <div className="flex min-w-0 flex-col gap-3">
          <Summary view={view} />
          <Files jobId={job.id} view={view} />
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

// ---- Land ----------------------------------------------------------------------------------

function LandCard({ job, view }: { job: JobDetail; view: ReviewView }) {
  const [confirming, setConfirming] = useState<LandAction | null>(null);
  const [override, setOverride] = useState(false);
  const options = view.land;
  const plainLand = useLand(job.id, options.head);
  const overrideLand = useLandOverride(job.id, options.head);
  const teardown = useTeardown();
  const toast = useToast();
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
  // crowsnest's evidence does not support this revision: merge and push need the explicit override.
  const blocked = (action: LandAction) => !!options.evidence && action !== "discard";
  const land = confirming && blocked(confirming) ? overrideLand : plainLand;
  const run = (action: LandAction) =>
    land.mutate(action, {
      onSuccess: (result) => {
        setConfirming(null);
        toast.show(
          result.teardown_error
            ? { tone: "warning", title: `${landLabels[action]}: done, teardown incomplete`, description: result.teardown_error }
            : { tone: "success", title: `${landLabels[action]}: done`, description: result.detail },
        );
      },
      onError: (error) => toast.show({ tone: "danger", title: `${landLabels[action]} refused`, description: error.message }),
    });
  return (
    <Card title="Land" actions={<span className="font-mono text-2xs text-muted">{options.branch} → {options.base || "?"}</span>}>
      {options.request && (
        <Banner tone="warning">An agent asked to {options.request === "pr" ? "push it and open a pull request" : options.request + " it"}; answer in the Inbox, or land it here.</Banner>
      )}
      {options.evidence && (
        <Banner tone="warning">
          crowsnest's evidence does not support landing this revision: {options.evidence_why}. Run the check again (or ask an agent to rebase) in the
          Overview; landing it anyway needs an explicit override.
        </Banner>
      )}
      {job.accept_state && job.accept_state !== "unverified" && <AcceptedMeans evidence={job.evidence} />}
      {options.base_protected && (
        <p className="mb-2 text-xs text-warning">{options.base} is protected: Land never merges into it. Use the pull request.</p>
      )}
      <p className="mb-1 flex items-center gap-1 text-2xs text-muted">
        Merge, push and open a pull request, or discard
        <HelpButton topic="concepts/worktrees-and-landing#landing" what="landing" />
      </p>
      <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Land actions">
        {landActions.map((action) => (
          <Button
            key={action}
            compact
            variant={action === "discard" ? "danger" : action === "merge" ? "primary" : undefined}
            icon={actionIcons[action]}
            disabled={!options.allowed[action] || land.isPending}
            title={options.why?.[action]}
            onClick={() => {
              setOverride(false);
              setConfirming(action);
            }}
          >
            {landLabels[action]}
          </Button>
        ))}
      </div>
      {landActions.some((action) => options.why?.[action]) && (
        <ul className="mt-2 text-2xs text-muted">
          {landActions
            .filter((action) => options.why?.[action])
            .map((action) => (
              <li key={action}>
                {landLabels[action]}: {options.why?.[action]}
              </li>
            ))}
        </ul>
      )}
      {confirming && (
        <div role="alertdialog" aria-label={`Confirm ${landLabels[confirming]}`} className="mt-3 rounded border border-border bg-surface-raised p-3 text-sm">
          <p className="text-body">{options.confirm[confirming]}</p>
          {blocked(confirming) && (
            <label className="mt-2 flex items-start gap-2 text-xs text-warning">
              <input type="checkbox" checked={override} onChange={(event) => setOverride(event.target.checked)} className="mt-0.5" />
              <span>Land it anyway, over crowsnest's evidence ({options.evidence_why}).</span>
            </label>
          )}
          <div className="mt-2 flex gap-2">
            <Button
              compact
              variant={confirming === "discard" ? "danger" : "primary"}
              disabled={land.isPending || (blocked(confirming) && !override)}
              onClick={() => run(confirming)}
            >
              {land.isPending ? "Working…" : `Yes, ${landLabels[confirming].toLowerCase()}`}
            </Button>
            <Button compact variant="ghost" onClick={() => setConfirming(null)}>
              Not now
            </Button>
          </div>
        </div>
      )}
    </Card>
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
        <Chip mono>{produced.branch}</Chip>
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
              <span className="font-mono text-muted">{commit.hash.slice(0, 8)}</span> {commit.subject}
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

function Files({ jobId, view }: { jobId: string; view: ReviewView }) {
  const files = view.produced?.files ?? [];
  if (files.length === 0) return null;
  return (
    <Card title="Changes, in reading order" bodyClassName="p-0">
      <ol aria-label="Changed files" className="divide-y divide-border">
        {files.map((file) => (
          <FileRow key={file.path} jobId={jobId} file={file} />
        ))}
      </ol>
    </Card>
  );
}

function FileRow({ jobId, file }: { jobId: string; file: FileChange }) {
  const [open, setOpen] = useState(false);
  return (
    <li>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="flex min-h-9 w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-hover"
      >
        <ChevronRight className={cn("h-3.5 w-3.5 shrink-0 text-muted transition-transform", open && "rotate-90")} aria-hidden />
        <FileCode2 className="h-3.5 w-3.5 shrink-0 text-muted" aria-hidden />
        <span className="min-w-0 flex-1 truncate font-mono text-body">
          {file.old_path ? `${file.old_path} → ${file.path}` : file.path}
        </span>
        <span className="shrink-0 text-2xs text-muted">{file.group}</span>
        {file.binary ? (
          <span className="shrink-0 text-2xs text-muted">binary</span>
        ) : (
          <span className="shrink-0 font-mono text-2xs">
            <span className="text-success">+{file.added}</span> <span className="text-danger">−{file.deleted}</span>
          </span>
        )}
      </button>
      {open && <FileBody jobId={jobId} file={file} />}
    </li>
  );
}

function FileBody({ jobId, file }: { jobId: string; file: FileChange }) {
  const query = useReviewDiff(jobId, file.path, !file.binary);
  if (file.binary) return <p className="px-3 pb-2 text-xs text-muted">Binary file ({file.status}); not shown.</p>;
  if (query.isPending) return <SkeletonRows rows={3} label={`Loading ${file.path}`} />;
  if (query.isError) return <p className="px-3 pb-2 text-xs text-danger">Could not load the diff: {query.error.message}</p>;
  if (query.data.binary) return <p className="px-3 pb-2 text-xs text-muted">Binary file; not shown.</p>;
  const lines = parseUnified(query.data.diff);
  return (
    <div className="max-h-[32rem] overflow-auto border-t border-border bg-surface-sunken">
      <table className="w-full border-collapse font-mono text-2xs leading-5">
        <tbody>
          {lines.map((line, index) => (
            <tr
              key={index}
              className={cn(
                line.kind === "add" && "bg-success/10",
                line.kind === "del" && "bg-danger/10",
                (line.kind === "hunk" || line.kind === "meta") && "bg-surface-raised text-muted",
              )}
            >
              <td className="w-10 select-none px-1 text-right text-muted">{line.old}</td>
              <td className="w-10 select-none px-1 text-right text-muted">{line.new}</td>
              <td className={cn("w-4 select-none text-center", line.kind === "add" && "text-success", line.kind === "del" && "text-danger")}>
                {line.kind === "add" ? "+" : line.kind === "del" ? "−" : ""}
              </td>
              <td className="whitespace-pre pr-3 text-body">{line.text}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {query.data.cut && <p className="px-3 py-1 text-2xs text-warning">The diff was cut at 512 KB.</p>}
    </div>
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
