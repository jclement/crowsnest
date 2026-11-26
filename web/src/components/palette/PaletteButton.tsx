// The header's way into the command palette: a compact "Search or run… ⌘K" field from sm, a
// search icon on a phone. Both open the palette inside the click, so a phone's keyboard comes up.
import { Search } from "lucide-react";
import { cn } from "../../lib/cn";
import { usePalette } from "./palette-context";

export function PaletteButton({ className }: { className?: string }) {
  const { openPalette, chord } = usePalette();
  return (
    <button
      type="button"
      onClick={openPalette}
      aria-label="Search or run a command"
      aria-keyshortcuts={chord === "⌘K" ? "Meta+K" : "Control+K"}
      title={`Search or run a command (${chord})`}
      className={cn(
        "inline-flex h-11 w-11 shrink-0 items-center justify-center rounded text-chrome-fg hover:bg-chrome-hover sm:h-7 sm:w-56 sm:justify-start sm:gap-2 sm:border sm:border-chrome-border sm:bg-chrome-hover/40 sm:px-2 sm:text-chrome-muted lg:w-72",
        className,
      )}
    >
      <Search className="h-4 w-4 shrink-0 sm:h-3.5 sm:w-3.5" aria-hidden />
      <span className="hidden truncate text-xs sm:inline">Search or run…</span>
      <kbd className="ml-auto hidden rounded border border-chrome-border px-1 font-mono text-2xs sm:inline">{chord}</kbd>
    </button>
  );
}
