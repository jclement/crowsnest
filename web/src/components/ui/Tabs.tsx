// Tabs: a segmented control with proper tab semantics (arrow keys move between tabs). The
// caller renders the active panel with <TabPanel>, passing the same id.
import type { LucideIcon } from "lucide-react";
import { useRef, type KeyboardEvent, type ReactNode } from "react";
import { cn } from "../../lib/cn";

export interface TabItem<T extends string> {
  value: T;
  label: string;
  icon?: LucideIcon;
}

interface TabsProps<T extends string> {
  /** Prefix for the tab and panel ids; must be unique on the page. */
  id: string;
  label: string;
  items: TabItem<T>[];
  value: T;
  onChange: (value: T) => void;
  className?: string;
}

export function Tabs<T extends string>({ id, label, items, value, onChange, className }: TabsProps<T>) {
  const listRef = useRef<HTMLDivElement>(null);

  const onKeyDown = (event: KeyboardEvent) => {
    const step = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
    if (!step) return;
    event.preventDefault();
    const index = items.findIndex((item) => item.value === value);
    const next = items[(index + step + items.length) % items.length];
    if (!next) return;
    onChange(next.value);
    listRef.current?.querySelector<HTMLButtonElement>(`#${CSS.escape(`${id}-tab-${next.value}`)}`)?.focus();
  };

  return (
    <div
      ref={listRef}
      role="tablist"
      aria-label={label}
      onKeyDown={onKeyDown}
      className={cn("inline-flex rounded-md border border-border bg-surface-sunken p-0.5", className)}
    >
      {items.map((item) => {
        const selected = item.value === value;
        return (
          <button
            key={item.value}
            id={`${id}-tab-${item.value}`}
            type="button"
            role="tab"
            aria-selected={selected}
            aria-controls={`${id}-panel`}
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(item.value)}
            className={cn(
              // Below sm the icons go and the padding narrows, so four tabs fit 390 px (R10 Opus F21).
              "inline-flex min-h-11 flex-1 items-center justify-center gap-1.5 whitespace-nowrap rounded px-2 text-xs font-medium transition-colors sm:px-3 md:min-h-7",
              selected ? "bg-surface text-heading shadow-sm ring-1 ring-border" : "text-muted hover:text-heading",
            )}
          >
            {item.icon && <item.icon className="hidden h-3.5 w-3.5 sm:block" aria-hidden />}
            {item.label}
          </button>
        );
      })}
    </div>
  );
}

export function TabPanel({ id, value, className, children }: { id: string; value: string; className?: string; children: ReactNode }) {
  return (
    <div role="tabpanel" id={`${id}-panel`} aria-labelledby={`${id}-tab-${value}`} className={className}>
      {children}
    </div>
  );
}
