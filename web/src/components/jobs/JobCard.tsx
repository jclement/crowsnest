// JobCard: one row of the board (DESIGN.md 12): title, state chip, the progress line, "active 2m
// ago", open questions, nudges when there were any, and the agent. A stuck job says why in words,
// and a job waiting on a retry counts down to it.
import { Bot, MessageCircleQuestion, RotateCcw, Zap } from "lucide-react";
import { cn } from "../../lib/cn";
import { relativeTime } from "../../lib/format";
import { cardLine, retryLabel, stateChip, type Job } from "../../lib/jobs";
import { Chip } from "../ui/Chip";

interface JobCardProps {
  job: Job;
  selected?: boolean;
  onSelect: (id: string) => void;
}

export function JobCard({ job, selected, onSelect }: JobCardProps) {
  const chip = stateChip(job);
  const line = cardLine(job);
  const retry = retryLabel(job);
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
          <Chip tone={chip.tone}>{chip.label}</Chip>
        </span>
        {line && <span className={cn("line-clamp-2 text-xs", job.stuck || job.state === "needs_you" ? "text-warning" : "text-body")}>{line}</span>}
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
