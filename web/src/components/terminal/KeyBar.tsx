// KeyBar: the keys a phone keyboard lacks, in a strip that sits just above it: Esc, Tab, a
// sticky Ctrl, the arrows, Enter, Paste, and the font size. It must never take focus from the
// terminal, or the on-screen keyboard would close on every tap: the buttons cancel the
// pointerdown/mousedown that would move focus, and are out of the tab order.
import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp, ClipboardPaste, CornerDownLeft, type LucideIcon } from "lucide-react";
import type { MouseEvent, PointerEvent, ReactNode } from "react";
import { cn } from "../../lib/cn";
import type { BarKey } from "../../lib/terminalKeys";

interface KeyBarProps {
  /** A bar key was pressed (Ctrl, if on, is the parent's business). */
  onKey: (key: BarKey) => void;
  ctrl: boolean;
  onCtrlChange: (on: boolean) => void;
  onPaste: () => void;
  fontSize: number;
  onFontSize: (size: number) => void;
  className?: string;
}

/** Keeps focus where it is (in the terminal) when a bar button is touched or clicked. */
function keepFocus(event: PointerEvent | MouseEvent) {
  event.preventDefault();
}

export function KeyBar({ onKey, ctrl, onCtrlChange, onPaste, fontSize, onFontSize, className }: KeyBarProps) {
  return (
    <div
      role="toolbar"
      aria-label="Terminal keys"
      data-allow-overflow
      className={cn("flex shrink-0 overflow-x-auto border-t border-border bg-surface-raised [scrollbar-width:none]", className)}
    >
      <BarButton label="Escape" onPress={() => onKey("esc")}>
        Esc
      </BarButton>
      <BarButton label="Tab" onPress={() => onKey("tab")}>
        Tab
      </BarButton>
      <BarButton label="Control (applies to the next key)" pressed={ctrl} onPress={() => onCtrlChange(!ctrl)}>
        Ctrl
      </BarButton>
      <BarButton label="Left" icon={ArrowLeft} onPress={() => onKey("left")} />
      <BarButton label="Up" icon={ArrowUp} onPress={() => onKey("up")} />
      <BarButton label="Down" icon={ArrowDown} onPress={() => onKey("down")} />
      <BarButton label="Right" icon={ArrowRight} onPress={() => onKey("right")} />
      <BarButton label="Enter" icon={CornerDownLeft} onPress={() => onKey("enter")} />
      <BarButton label="Paste" icon={ClipboardPaste} onPress={onPaste} />
      <BarButton label="Smaller text" onPress={() => onFontSize(fontSize - 1)}>
        A−
      </BarButton>
      <BarButton label="Larger text" onPress={() => onFontSize(fontSize + 1)}>
        A+
      </BarButton>
    </div>
  );
}

interface BarButtonProps {
  label: string;
  icon?: LucideIcon;
  pressed?: boolean;
  onPress: () => void;
  children?: ReactNode;
}

function BarButton({ label, icon: Icon, pressed, onPress, children }: BarButtonProps) {
  return (
    <button
      type="button"
      tabIndex={-1}
      aria-label={label}
      title={label}
      aria-pressed={pressed}
      onPointerDown={keepFocus}
      onMouseDown={keepFocus}
      onClick={onPress}
      className={cn(
        "inline-flex h-11 min-w-11 flex-1 shrink-0 items-center justify-center border-r border-border px-2 font-mono text-xs text-heading last:border-r-0 active:bg-hover",
        pressed && "bg-accent text-accent-fg active:bg-accent",
      )}
    >
      {Icon ? <Icon className="h-4 w-4" aria-hidden /> : children}
    </button>
  );
}
