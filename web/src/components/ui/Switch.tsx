// Switch: an on/off toggle with switch semantics (role="switch", aria-checked), 44 px tall on a
// phone and dense from md. The Schedule's per-task switch and every per-agent "speak replies"
// switch use it, so they look and read the same.
import { cn } from "../../lib/cn";

interface SwitchProps {
  checked: boolean;
  onChange: (next: boolean) => void;
  /** The accessible name; also the tooltip. */
  label: string;
  /** Visible text beside the track (the label stays the accessible name). */
  text?: string;
  disabled?: boolean;
  className?: string;
}

export function Switch({ checked, onChange, label, text, disabled, className }: SwitchProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cn("inline-flex min-h-11 shrink-0 items-center gap-1.5 rounded text-xs text-body disabled:opacity-50 md:min-h-7", className)}
    >
      <span className={cn("relative inline-block h-5 w-9 shrink-0 rounded-full transition-colors", checked ? "bg-accent" : "bg-border-strong")}>
        <span className={cn("absolute top-0.5 h-4 w-4 rounded-full bg-surface shadow transition-all", checked ? "left-4.5" : "left-0.5")} />
      </span>
      {text && <span>{text}</span>}
    </button>
  );
}
