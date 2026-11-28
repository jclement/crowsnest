// Banner: a full-width strip for page-level state (connection lost, demo mode, a failed load).
import { AlertTriangle, CheckCircle2, Info, XCircle, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

export type BannerTone = "info" | "warning" | "danger" | "success";

const toneStyles: Record<BannerTone, { className: string; icon: LucideIcon }> = {
  info: { className: "bg-accent/10 text-accent border-accent/25", icon: Info },
  warning: { className: "bg-warning/10 text-warning border-warning/30", icon: AlertTriangle },
  danger: { className: "bg-danger/10 text-danger border-danger/30", icon: XCircle },
  success: { className: "bg-success/10 text-success border-success/30", icon: CheckCircle2 },
};

interface BannerProps {
  tone?: BannerTone;
  icon?: LucideIcon;
  action?: ReactNode;
  /** "status" for polite updates, "alert" for failures that need attention now. */
  role?: "status" | "alert";
  className?: string;
  children: ReactNode;
}

export function Banner({ tone = "info", icon, action, role = "status", className, children }: BannerProps) {
  const style = toneStyles[tone];
  const Icon = icon ?? style.icon;
  return (
    // Below sm the action goes under the text instead of squeezing it to a five-line column
    // beside a button (R10 Fable polish: the stuck, trust and first-run banners on a phone).
    <div role={role} className={cn("flex min-h-9 flex-wrap items-center gap-x-2 gap-y-1 border px-3 py-1 text-xs sm:flex-nowrap", style.className, className)}>
      <Icon className="h-4 w-4 shrink-0" aria-hidden />
      <div className="min-w-0 flex-1 basis-[calc(100%-1.5rem)] sm:basis-0">{children}</div>
      {action && <div className="ml-6 shrink-0 sm:ml-0">{action}</div>}
    </div>
  );
}
