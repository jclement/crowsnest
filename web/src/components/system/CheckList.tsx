// A list of doctor checks: one row each with a status mark (its own shape per status, so it
// reads without colour: a tick, an info dot, a triangle, a cross), the title, what was found and,
// for anything not fine, the fix and its command with a copy button. The System page and the
// first-run checklist both draw checks with it.
import { Check as CheckIcon, CircleAlert, Copy, Info, Minus, TriangleAlert, X } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "../../lib/cn";
import { statusLabels, type Check, type CheckStatus } from "../../lib/system";
import { useToast } from "../ui/toast-context";

const marks: Record<CheckStatus, { icon: typeof CheckIcon; className: string }> = {
  ok: { icon: CheckIcon, className: "bg-success/12 text-success" },
  info: { icon: Info, className: "bg-accent/10 text-accent" },
  warn: { icon: TriangleAlert, className: "bg-warning/12 text-warning" },
  fail: { icon: X, className: "bg-danger/12 text-danger" },
  skip: { icon: Minus, className: "bg-surface-sunken text-muted" },
};

/** The status mark of one check: an icon in a tinted circle, labelled for screen readers. */
export function StatusMark({ status, className }: { status: CheckStatus; className?: string }) {
  const { icon: Icon, className: tone } = marks[status];
  return (
    <span role="img" aria-label={statusLabels[status]} title={statusLabels[status]} data-status={status} className={cn("inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full", tone, className)}>
      <Icon className="h-3 w-3" strokeWidth={3} aria-hidden />
    </span>
  );
}

/** A command in a monospace box with a copy button. */
export function CommandLine({ command, className }: { command: string; className?: string }) {
  const toast = useToast();
  const copy = () => {
    void navigator.clipboard
      ?.writeText(command)
      .then(() => toast.show({ tone: "success", title: "Copied", description: command }))
      .catch(() => toast.show({ tone: "danger", title: "Could not copy", description: "Select the text and copy it by hand." }));
  };
  return (
    <div className={cn("flex min-w-0 items-center gap-1 rounded border border-border bg-surface-sunken pl-2", className)}>
      {/* Focusable: on a narrow screen it scrolls sideways, and the keyboard must reach that. */}
      <code tabIndex={0} className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap py-1.5 font-mono text-2xs text-heading">
        {command}
      </code>
      <button type="button" onClick={copy} aria-label={`Copy ${command}`} className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded text-muted hover:bg-hover hover:text-heading">
        <Copy className="h-3.5 w-3.5" aria-hidden />
      </button>
    </div>
  );
}

interface CheckRowProps {
  check: Check;
  /** Extra controls under the fix (the checklist's "Open a terminal"). */
  action?: ReactNode;
  /** Show the fix even for ok checks (never needed so far). */
  compact?: boolean;
}

export function CheckRow({ check, action, compact }: CheckRowProps) {
  const needsFix = check.status === "fail" || check.status === "warn" || check.status === "info";
  return (
    <li className="flex gap-2.5 py-2" data-check={check.id}>
      <StatusMark status={check.status} className="mt-0.5" />
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium text-heading">
          {check.title}
          {check.cost && (
            <span className="ml-2 inline-flex items-center gap-1 text-2xs font-normal text-warning">
              <CircleAlert className="h-3 w-3" aria-hidden />
              costs {check.cost}
            </span>
          )}
        </p>
        {check.detail && <p className="break-words text-xs text-muted">{check.detail}</p>}
        {needsFix && !compact && check.fix && <p className="mt-1 text-xs text-body">{check.fix}</p>}
        {needsFix && !compact && check.command && <CommandLine command={check.command} className="mt-1.5 max-w-xl" />}
        {action && <div className="mt-2 flex flex-wrap gap-2">{action}</div>}
      </div>
    </li>
  );
}

export function CheckList({ checks, renderAction }: { checks: Check[]; renderAction?: (check: Check) => ReactNode }) {
  return (
    <ul className="divide-y divide-border">
      {checks.map((check, index) => (
        <CheckRow key={check.id + index} check={check} action={renderAction?.(check)} />
      ))}
    </ul>
  );
}
