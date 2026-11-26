// The house empty state: icon, message, and an optional call to action.
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

interface EmptyStateProps {
  icon: LucideIcon;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}

export function EmptyState({ icon: Icon, title, children, action }: EmptyStateProps) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center">
      <Icon className="h-8 w-8 text-muted" aria-hidden />
      <h1 className="text-sm font-semibold text-heading">{title}</h1>
      {children && <p className="max-w-sm text-xs text-muted">{children}</p>}
      {action}
    </div>
  );
}
