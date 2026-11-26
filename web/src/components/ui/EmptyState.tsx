// The house empty state: icon, message, and an optional call to action.
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
      <div className="mb-1 flex h-10 w-10 items-center justify-center rounded-full border border-border bg-surface-raised">
        <Icon className="h-5 w-5 text-muted" aria-hidden />
      </div>
      <h2 className="text-sm font-semibold text-heading">{title}</h2>
      {children && <p className="max-w-sm text-xs leading-relaxed text-muted">{children}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}
