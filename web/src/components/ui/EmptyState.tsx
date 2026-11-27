// The house empty state: icon, message, and an optional call to action. The icon sits in the
// lookout's rings (the mark's sweep, as a still radar): an empty view is a quiet horizon, not an
// error.
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

interface EmptyStateProps {
  icon: LucideIcon;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
  className?: string;
}

export function EmptyState({ icon: Icon, title, children, action, className }: EmptyStateProps) {
  return (
    <div className={cn("flex h-full flex-col items-center justify-center gap-2 p-8 text-center", className)}>
      <div aria-hidden className="relative mb-2 flex h-12 w-12 items-center justify-center">
        <span className="absolute -inset-2.5 rounded-full border border-dashed border-border" />
        <span className="absolute -inset-2.5 rounded-full border border-transparent border-t-accent/70 [transform:rotate(35deg)]" />
        <span className="absolute inset-0 rounded-full border border-border bg-surface-raised" />
        <Icon className="relative h-5 w-5 text-muted" />
      </div>
      <h2 className="text-sm font-semibold text-heading">{title}</h2>
      {children && <p className="max-w-sm text-xs leading-relaxed text-muted">{children}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}
