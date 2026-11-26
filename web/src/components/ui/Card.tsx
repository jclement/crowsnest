// Card: a bordered panel with an optional header row. Borders do the separating, not shadows.
import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

interface CardProps {
  title?: ReactNode;
  /** Right-aligned header content: chips, an icon button. */
  actions?: ReactNode;
  tone?: "default" | "danger" | "warning";
  className?: string;
  bodyClassName?: string;
  children: ReactNode;
  "aria-label"?: string;
}

const toneBorder = {
  default: "border-border",
  danger: "border-danger/50",
  warning: "border-warning/50",
};

export function Card({ title, actions, tone = "default", className, bodyClassName, children, ...rest }: CardProps) {
  return (
    <section aria-label={rest["aria-label"]} className={cn("min-w-0 rounded-md border bg-surface", toneBorder[tone], className)}>
      {(title || actions) && (
        <header
          className={cn(
            "flex min-h-9 items-center gap-2 border-b px-3 py-1.5",
            tone === "danger" ? "border-danger/30 bg-danger/5" : tone === "warning" ? "border-warning/30 bg-warning/5" : "border-border bg-surface-raised",
          )}
        >
          <div className="flex min-w-0 flex-1 items-center gap-2 text-xs font-semibold text-heading">{title}</div>
          {actions && <div className="flex shrink-0 items-center gap-1">{actions}</div>}
        </header>
      )}
      <div className={cn("p-3", bodyClassName)}>{children}</div>
    </section>
  );
}
