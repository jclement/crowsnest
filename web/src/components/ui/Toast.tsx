// Toasts for mutation results. Three at most, each gone after a few seconds (failures stay
// longer). On a phone they sit at the top, under the header: at the bottom they covered the
// thumb's next target (the decision packet's answer bar, the job box's Give job, a row's
// buttons; R10 Opus F6, Fable F6). From md they sit at the bottom right, beside the content.
import { AlertTriangle, CheckCircle2, Info, X, XCircle } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { cn } from "../../lib/cn";
import { ToastContext, type ToastInput, type ToastTone } from "./toast-context";

interface ToastItem extends ToastInput {
  id: number;
}

const MAX_TOASTS = 3;
const TOAST_MS = 4_000;
const TOAST_FAILURE_MS = 8_000;

const toneIcon = { info: Info, success: CheckCircle2, warning: AlertTriangle, danger: XCircle };
const toneColour: Record<ToastTone, string> = {
  info: "text-accent",
  success: "text-success",
  warning: "text-warning",
  danger: "text-danger",
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const nextId = useRef(1);

  const dismiss = useCallback((id: number) => setToasts((current) => current.filter((toast) => toast.id !== id)), []);
  const show = useCallback((toast: ToastInput) => {
    const id = nextId.current++;
    setToasts((current) => [...current, { ...toast, id }].slice(-MAX_TOASTS));
  }, []);
  const api = useMemo(() => ({ show }), [show]);

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div
        aria-live="polite"
        className="pointer-events-none fixed inset-x-0 top-[calc(env(safe-area-inset-top)+3rem)] z-50 flex flex-col items-center gap-2 px-3 md:inset-x-auto md:top-auto md:bottom-4 md:right-4 md:items-end"
      >
        {toasts.map((toast) => (
          <Toast key={toast.id} toast={toast} onDismiss={() => dismiss(toast.id)} />
        ))}
      </div>
    </ToastContext.Provider>
  );
}

function Toast({ toast, onDismiss }: { toast: ToastItem; onDismiss: () => void }) {
  const tone = toast.tone ?? "info";
  const Icon = toneIcon[tone];
  useEffect(() => {
    const timer = setTimeout(onDismiss, tone === "danger" || tone === "warning" ? TOAST_FAILURE_MS : TOAST_MS);
    return () => clearTimeout(timer);
  }, [onDismiss, tone]);
  return (
    <div
      role={tone === "danger" ? "alert" : "status"}
      className="pointer-events-auto flex w-full max-w-sm animate-toast-in items-start gap-2 rounded-md border border-border bg-surface-raised py-2 pl-3 pr-1 shadow-lg"
    >
      <Icon className={cn("mt-0.5 h-4 w-4 shrink-0", toneColour[tone])} aria-hidden />
      <div className="min-w-0 flex-1 py-0.5">
        <p className="text-xs font-medium text-heading">{toast.title}</p>
        {toast.description && <p className="mt-0.5 text-xs text-muted">{toast.description}</p>}
        {toast.action && (
          <button
            type="button"
            onClick={() => {
              toast.action?.onClick();
              onDismiss();
            }}
            className="-ml-1 mt-0.5 inline-flex min-h-11 items-center rounded px-1 text-xs font-medium text-accent underline-offset-2 hover:underline md:min-h-6"
          >
            {toast.action.label}
          </button>
        )}
      </div>
      <button
        type="button"
        onClick={onDismiss}
        aria-label="Dismiss"
        className="inline-flex h-11 w-11 shrink-0 items-center justify-center rounded text-muted hover:bg-hover hover:text-heading md:h-8 md:w-8"
      >
        <X className="h-3.5 w-3.5" aria-hidden />
      </button>
    </div>
  );
}
