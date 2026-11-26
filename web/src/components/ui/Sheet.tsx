// Sheet: a dialog that is full-screen on mobile and a centred modal from md (house style: no
// centred modals on a phone). It renders into document.body, closes on Escape and on the
// backdrop, moves focus in on open and back to the opener on close, and locks page scroll.
import { X } from "lucide-react";
import { useEffect, useId, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { cn } from "../../lib/cn";
import { IconButton } from "./Button";

interface SheetProps {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  /** Extra header content beside the title (chips, actions). */
  headerExtra?: ReactNode;
  /** Width of the desktop modal. */
  size?: "md" | "lg";
  children: ReactNode;
}

export function Sheet({ open, onClose, title, headerExtra, size = "lg", children }: SheetProps) {
  const titleId = useId();
  const panelRef = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);
  useEffect(() => {
    onCloseRef.current = onClose;
  });

  useEffect(() => {
    if (!open) return;
    const opener = document.activeElement as HTMLElement | null;
    const { overflow } = document.body.style;
    document.body.style.overflow = "hidden";
    panelRef.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onCloseRef.current();
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = overflow;
      opener?.focus?.();
    };
  }, [open]);

  if (!open) return null;
  return createPortal(
    <div className="fixed inset-0 z-40 flex items-stretch justify-center md:items-center md:p-6">
      <div aria-hidden className="absolute inset-0 hidden bg-overlay md:block" onClick={onClose} />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        className={cn(
          "relative flex w-full animate-sheet-in flex-col bg-surface outline-none",
          "pb-[env(safe-area-inset-bottom)] pt-[env(safe-area-inset-top)]",
          "md:max-h-[85vh] md:rounded-lg md:border md:border-border md:pb-0 md:pt-0 md:shadow-2xl",
          size === "lg" ? "md:max-w-3xl" : "md:max-w-lg",
        )}
      >
        <header className="flex min-h-12 shrink-0 items-center gap-2 border-b border-border pl-3 pr-1">
          <h2 id={titleId} className="flex min-w-0 flex-1 items-center gap-2 text-sm font-semibold text-heading">
            {title}
          </h2>
          {headerExtra}
          <IconButton icon={X} label="Close" onClick={onClose} />
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto">{children}</div>
      </div>
    </div>,
    document.body,
  );
}
