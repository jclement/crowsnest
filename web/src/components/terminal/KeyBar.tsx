// KeyBar: the keys a phone keyboard lacks, in a strip that sits just above it: Esc, Tab, a
// sticky Ctrl, the arrows, Enter, then Ctrl+C, Paste, Copy and the font size. It must never take
// focus from the terminal, or the on-screen keyboard would close on every tap: the buttons cancel
// the pointerdown/mousedown that would move focus, and are out of the tab order. A key drawn as
// text ("Esc", "A−") has that text at the start of its accessible name, so voice control can say
// what it sees (axe's label-content-name-mismatch, found in M9).
//
// Below sm (a phone held upright) the bar is two rows: the eight keys typed most, then the
// clipboard and size keys. One scrolling row of thirteen 44 px keys left Paste half visible and
// the font size off the right edge at 390 px (R4 Fable 3.2). From sm (landscape, a tablet) it is
// one row again, so the terminal keeps its height where height is scarce.
import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp, ClipboardCopy, ClipboardPaste, CornerDownLeft, type LucideIcon } from "lucide-react";
import type { MouseEvent, PointerEvent, ReactNode } from "react";
import { cn } from "../../lib/cn";
import type { BarKey } from "../../lib/terminalKeys";

interface KeyBarProps {
  /** A bar key was pressed (Ctrl, if on, is the parent's business). */
  onKey: (key: BarKey) => void;
  ctrl: boolean;
  onCtrlChange: (on: boolean) => void;
  onPaste: () => void;
  /** Copies the selection, or the visible screen when nothing is selected. */
  onCopy?: () => void;
  /** Sends Ctrl+C (interrupt) in one tap. */
  onInterrupt?: () => void;
  fontSize: number;
  onFontSize: (size: number) => void;
  className?: string;
}

/** Keeps focus where it is (in the terminal) when a bar button is touched or clicked. */
function keepFocus(event: PointerEvent | MouseEvent) {
  event.preventDefault();
}

export function KeyBar({ onKey, ctrl, onCtrlChange, onPaste, onCopy, onInterrupt, fontSize, onFontSize, className }: KeyBarProps) {
  return (
    <div
      role="toolbar"
      aria-label="Terminal keys"
      data-allow-overflow
      className={cn("flex shrink-0 flex-col border-t border-border bg-surface-raised pb-[env(safe-area-inset-bottom)] sm:flex-row", className)}
    >
      <div className="flex min-w-0 sm:flex-[8]">
        <BarButton label="Esc (Escape)" onPress={() => onKey("esc")}>
          Esc
        </BarButton>
        <BarButton label="Tab" onPress={() => onKey("tab")}>
          Tab
        </BarButton>
        <BarButton label="Ctrl (applies to the next key)" pressed={ctrl} onPress={() => onCtrlChange(!ctrl)}>
          Ctrl
        </BarButton>
        <BarButton label="Left" icon={ArrowLeft} onPress={() => onKey("left")} />
        <BarButton label="Up" icon={ArrowUp} onPress={() => onKey("up")} />
        <BarButton label="Down" icon={ArrowDown} onPress={() => onKey("down")} />
        <BarButton label="Right" icon={ArrowRight} onPress={() => onKey("right")} />
        <BarButton label="Enter" icon={CornerDownLeft} onPress={() => onKey("enter")} />
      </div>
      <div className="flex min-w-0 border-t border-border sm:flex-[5] sm:border-l sm:border-t-0">
        {onInterrupt && (
          <BarButton label="^C (Ctrl+C, interrupt)" onPress={onInterrupt}>
            ^C
          </BarButton>
        )}
        <BarButton label="Paste" icon={ClipboardPaste} onPress={onPaste} />
        {onCopy && <BarButton label="Copy (the selection, or the screen)" icon={ClipboardCopy} onPress={onCopy} />}
        <BarButton label="A− (smaller text)" onPress={() => onFontSize(fontSize - 1)}>
          A−
        </BarButton>
        <BarButton label="A+ (larger text)" onPress={() => onFontSize(fontSize + 1)}>
          A+
        </BarButton>
      </div>
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
        "inline-flex h-11 min-w-0 flex-1 items-center justify-center border-r border-border px-1 font-mono text-xs text-heading last:border-r-0 active:bg-hover",
        pressed && "bg-accent text-accent-fg active:bg-accent",
      )}
    >
      {Icon ? <Icon className="h-4 w-4" aria-hidden /> : children}
    </button>
  );
}
