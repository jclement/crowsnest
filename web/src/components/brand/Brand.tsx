// The crowsnest mark and wordmark (DESIGN.md "Design language"). The mark is a lookout's nest on
// a mast above the horizon, with two arcs of sweep from the nest: someone up there is watching.
// Drawn inline, not as an <img>, so it takes the theme's colours: the mast and horizon in the
// current ink, the nest and the sweep in the accent. public/icons/crowsnest.svg is the same
// drawing in the house colours, for the favicon and the installed app.
import { cn } from "../../lib/cn";

/** The mark alone, sized by the caller (a 64-unit square). */
export function Mark({ className, accentClassName = "text-chrome-accent" }: { className?: string; accentClassName?: string }) {
  return (
    <svg viewBox="0 0 64 64" aria-hidden className={cn("shrink-0", className)} fill="none" strokeLinecap="round">
      <path d="M32 12v40" stroke="currentColor" strokeWidth="4" />
      <path d="M10 53h44" stroke="currentColor" strokeWidth="4" strokeOpacity="0.55" />
      <g className={accentClassName}>
        <path d="M19 19h26l-4 11H23z" fill="currentColor" />
        <path d="M44.5 10.5a14 14 0 0 1 6 9.5" stroke="currentColor" strokeWidth="3.5" />
        <path d="M50.5 4.5a22 22 0 0 1 9 15" stroke="currentColor" strokeWidth="3.5" strokeOpacity="0.55" />
      </g>
    </svg>
  );
}

/**
 * The wordmark: the mark, then "crowsnest" in the monospace face with the "nest" lit in the
 * accent, the way a prompt lights the part you are in. `compact` drops the word below sm.
 */
export function Wordmark({ className, compact }: { className?: string; compact?: boolean }) {
  return (
    <span className={cn("inline-flex items-center gap-1.5 text-chrome-fg", className)}>
      <Mark className="h-5 w-5" />
      <span className={cn("font-mono text-[13px] font-semibold tracking-tight", compact && "hidden sm:inline")}>
        crows<span className="text-chrome-accent">nest</span>
      </span>
    </span>
  );
}
