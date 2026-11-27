// JobCard: one row of the board (DESIGN.md 12): title, its one chip (what happens next and who
// owns it, lib/jobs.ts nextStep; R8 replaced the state chip plus the acceptance chip), the
// progress line, "active 2m ago", open questions, nudges when there were any, the estimated
// spend, and the agent. A stuck job says why in words, and a job waiting on a retry counts down
// to it. The job's detail keeps the full facts.
import { Bot, FolderKanban, MessageCircleQuestion, RotateCcw, Zap } from "lucide-react";
import { cn } from "../../lib/cn";
import { relativeTime } from "../../lib/format";
import { budgetChip } from "../../lib/accept";
import { cardLine, nextOwnerText, nextStep, retryLabel, type Job } from "../../lib/jobs";
import { LastMessage } from "./AcceptPanels";
import { SpendChip } from "../spend/SpendChip";
import { Chip } from "../ui/Chip";
import { viewedText, type DiffProgress } from "../../lib/changes";

interface JobCardProps {
  job: Job;
  selected?: boolean;
  onSelect: (id: string) => void;
  /** The project's name, for a chip (the board shows it when it lists every project). */
  projectName?: string;
  /** How much of its diff is marked viewed, once its diff was opened (DESIGN.md 6.21). */
  progress?: DiffProgress;
}

export function JobCard({ job, selected, onSelect, projectName, progress }: JobCardProps) {
  const chip = nextStep(job);
  const line = cardLine(job);
  const retry = retryLabel(job);
  // The budget's own chip, unless the one chip already says the budget holds it.
  const budget = chip.label === "Held: budget" ? null : budgetChip(job.budget);
  // A job held by the fleet's usage limit is one banner, not a red card each (6.17).
  const limitHeld = job.cause === "limit" && /usage limit/.test(job.cause_detail);
  const active = job.last_activity_at ?? job.started_at ?? job.created_at;
  return (
    <li>
      <button
        type="button"
        onClick={() => onSelect(job.id)}
        aria-current={selected || undefined}
        className={cn("flex w-full min-h-14 flex-col gap-1 px-3 py-2.5 text-left hover:bg-hover focus-visible:bg-hover", selected && "bg-selected")}
      >
        <span className="flex w-full items-start gap-2">
          <span className="min-w-0 flex-1 break-words text-sm font-medium text-heading">{job.title}</span>
          <Chip tone={chip.tone} title={`Next: ${chip.label}. ${nextOwnerText(chip.owner)}.`}>
            {chip.label}
          </Chip>
        </span>
        {line && <span className={cn("line-clamp-2 text-xs", (job.stuck && !limitHeld) || job.state === "needs_you" ? "text-warning" : "text-body")}>{line}</span>}
        {job.wake && job.state === "blocked" && (
          <span className="text-2xs text-muted">
            Waits on {job.wait_owner}: {job.wake}
          </span>
        )}
        {!limitHeld && <LastMessage job={job} clamp />}
        {progress && progress.total > 0 && (
          <span className="flex items-center gap-2 text-2xs text-muted">
            <span className="h-1 w-16 overflow-hidden rounded bg-surface-sunken" role="progressbar" aria-label="Files viewed" aria-valuemin={0} aria-valuemax={progress.total} aria-valuenow={progress.viewed}>
              <span className="block h-full bg-success" style={{ width: `${Math.round((progress.viewed / progress.total) * 100)}%` }} />
            </span>
            {viewedText(progress.viewed, progress.total)}
          </span>
        )}
        <span className="flex flex-wrap items-center gap-1 text-2xs text-muted">
          <span>{job.state === "queued" ? `queued ${relativeTime(job.created_at)}` : `active ${relativeTime(active)}`}</span>
          {job.open_questions > 0 && (
            <Chip tone="danger" title="Open questions">
              <MessageCircleQuestion className="h-3 w-3" aria-hidden />
              {job.open_questions} {job.open_questions === 1 ? "question" : "questions"}
            </Chip>
          )}
          {job.nudges > 0 && (
            <Chip tone="warning" title="Times crowsnest sent the agent back to work">
              <Zap className="h-3 w-3" aria-hidden />
              {job.nudges} {job.nudges === 1 ? "nudge" : "nudges"}
            </Chip>
          )}
          {retry && (
            <Chip tone="warning">
              <RotateCcw className="h-3 w-3" aria-hidden />
              {retry}
            </Chip>
          )}
          {projectName && (
            <Chip tone="accent" title="Project">
              <FolderKanban className="h-3 w-3" aria-hidden />
              {projectName}
            </Chip>
          )}
          {budget && (
            <Chip tone={budget.tone} title={budget.title}>
              {budget.label}
            </Chip>
          )}
          <SpendChip spend={job.spend} />
          {job.agent_name && (
            <Chip mono>
              <Bot className="h-3 w-3" aria-hidden />
              {job.agent_name}
            </Chip>
          )}
          <Chip mono>{job.id}</Chip>
        </span>
      </button>
    </li>
  );
}
