// Button and IconButton: the only clickable controls views should need. Sizes follow the house
// rule of 44 px tap targets on mobile, shrinking to a dense 32 px from md.
import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { cn } from "../../lib/cn";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "warning";

const variants: Record<ButtonVariant, string> = {
  primary: "bg-accent text-accent-fg hover:brightness-110 border border-transparent",
  secondary: "bg-surface text-heading border border-border-strong hover:bg-hover",
  ghost: "bg-transparent text-body border border-transparent hover:bg-hover hover:text-heading",
  danger: "bg-danger text-white border border-transparent hover:brightness-110",
  warning: "bg-surface text-heading border border-warning/70 hover:bg-warning/10",
};

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  icon?: LucideIcon;
  /** Compact: 32 px on mobile too. Only for controls that are not primary tap targets. */
  compact?: boolean;
  children?: ReactNode;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "secondary", icon: Icon, compact, className, children, type = "button", ...props },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={cn(
        "inline-flex select-none items-center justify-center gap-1.5 rounded px-3 text-sm font-medium transition-colors disabled:pointer-events-none disabled:opacity-50",
        compact ? "h-8 text-xs" : "min-h-11 md:min-h-8",
        variants[variant],
        className,
      )}
      {...props}
    >
      {Icon && <Icon className="h-4 w-4 shrink-0" aria-hidden />}
      {children}
    </button>
  );
});

interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  icon: LucideIcon;
  /** Required: an icon alone has no accessible name. */
  label: string;
  /** "chrome" for the dark header, "default" elsewhere. */
  tone?: "default" | "chrome";
}

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { icon: Icon, label, tone = "default", className, type = "button", ...props },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      aria-label={label}
      title={label}
      className={cn(
        "inline-flex h-11 w-11 shrink-0 items-center justify-center rounded transition-colors disabled:pointer-events-none disabled:opacity-50",
        tone === "chrome"
          ? "text-chrome-muted hover:bg-chrome-hover hover:text-chrome-fg md:h-10 md:w-10"
          : "text-muted hover:bg-hover hover:text-heading md:h-8 md:w-8",
        className,
      )}
      {...props}
    >
      <Icon className="h-4 w-4" aria-hidden />
    </button>
  );
});
