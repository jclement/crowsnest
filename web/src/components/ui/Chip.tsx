// Chip (a small label), StatusDot and StatusChip. Agent states must be told apart at a glance in
// both themes and without colour vision, so each state has its own shape as well as colour:
// working pulses, blocked is a diamond, done is a solid dot, idle a ring, unknown a dash.
import type { ReactNode } from "react";
import { cn } from "../../lib/cn";
import { statusHints, statusLabels } from "../../lib/status";
import type { AgentStatus } from "../../lib/types";

export type ChipTone = "neutral" | "accent" | "danger" | "warning" | "success";

const tones: Record<ChipTone, string> = {
  neutral: "bg-surface-sunken text-muted border-border",
  accent: "bg-accent/10 text-accent border-accent/25",
  danger: "bg-danger/10 text-danger border-danger/30",
  warning: "bg-warning/10 text-warning border-warning/35",
  success: "bg-success/10 text-success border-success/30",
};

interface ChipProps {
  tone?: ChipTone;
  mono?: boolean;
  title?: string;
  className?: string;
  children: ReactNode;
}

export function Chip({ tone = "neutral", mono, title, className, children }: ChipProps) {
  return (
    <span
      title={title}
      className={cn(
        "inline-flex h-5 shrink-0 items-center gap-1 whitespace-nowrap rounded border px-1.5 text-2xs font-medium",
        mono && "font-mono",
        tones[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}

const dotShape: Record<AgentStatus, string> = {
  working: "rounded-full bg-status-working",
  blocked: "rotate-45 rounded-[1px] bg-status-blocked scale-90",
  done: "rounded-full bg-status-done",
  idle: "rounded-full border-2 border-status-idle",
  unknown: "my-[3.5px] h-[3px]! rounded-full bg-status-unknown",
};

/** A 10 px state marker. It carries its state as an accessible label and a tooltip. */
export function StatusDot({ status, className }: { status: AgentStatus; className?: string }) {
  return (
    <span
      role="img"
      aria-label={statusLabels[status]}
      title={`${statusLabels[status]}: ${statusHints[status]}`}
      data-status={status}
      className={cn("relative inline-flex h-2.5 w-2.5 shrink-0", className)}
    >
      {status === "working" && (
        <span aria-hidden className="absolute inset-0 animate-pulse-ring rounded-full bg-status-working" />
      )}
      <span aria-hidden className={cn("relative h-2.5 w-2.5", dotShape[status])} />
    </span>
  );
}

const statusTone: Record<AgentStatus, ChipTone> = {
  blocked: "danger",
  working: "accent",
  done: "success",
  idle: "neutral",
  unknown: "neutral",
};

/** The state as a labelled chip, for headers where there is room for the word. */
export function StatusChip({ status }: { status: AgentStatus }) {
  return (
    <Chip tone={statusTone[status]} title={statusHints[status]}>
      <StatusDot status={status} className="scale-75" />
      {statusLabels[status]}
    </Chip>
  );
}

/**
 * The five states a piece of work can be in, as the whole app names them (DESIGN.md "Design
 * language"), plus the two quiet ones. Each has a colour token and a shape of its own: needs you
 * a diamond, stuck a triangle, running a pulsing dot, done a filled dot, held a square, idle a
 * ring, unknown a dash.
 */
export type WorkState = "needs" | "stuck" | "running" | "done" | "held" | "idle" | "unknown";

export const workStates: WorkState[] = ["needs", "stuck", "running", "done", "held", "idle", "unknown"];

export const workStateLabels: Record<WorkState, string> = {
  needs: "Needs you",
  stuck: "Stuck",
  running: "Running",
  done: "Done",
  held: "Held",
  idle: "Idle",
  unknown: "Unknown",
};

const stateShape: Record<WorkState, string> = {
  needs: "rotate-45 scale-90 rounded-[1px] bg-status-blocked",
  stuck: "bg-status-stuck [clip-path:polygon(50%_4%,100%_96%,0_96%)]",
  running: "rounded-full bg-status-working",
  done: "rounded-full bg-status-done",
  held: "scale-[0.8] rounded-[1px] bg-status-held",
  idle: "rounded-full border-2 border-status-idle",
  unknown: "my-[3.5px] h-[3px]! rounded-full bg-status-unknown",
};

/** A 10 px marker for a work state, labelled for assistive technology. */
export function StateMark({ state, label, className }: { state: WorkState; label?: string; className?: string }) {
  const name = label ?? workStateLabels[state];
  return (
    <span role="img" aria-label={name} title={name} data-state={state} className={cn("relative inline-flex h-2.5 w-2.5 shrink-0", className)}>
      {state === "running" && <span aria-hidden className="absolute inset-0 animate-pulse-ring rounded-full bg-status-working" />}
      <span aria-hidden className={cn("relative h-2.5 w-2.5", stateShape[state])} />
    </span>
  );
}
