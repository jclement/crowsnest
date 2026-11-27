// The small "?" beside a view's title or a confusing card: opens that manual section in the help
// sheet over the page (PaletteProvider owns the sheet). `topic` is a page id and anchor,
// "concepts/supervisor#stuck-causes"; the manual tests check that every topic used in the app exists.
import { CircleHelp } from "lucide-react";
import { cn } from "../../lib/cn";
import { usePaletteMaybe } from "../palette/palette-context";

/**
 * `inline` (the default) draws it small inside a line of text or a card header, with a 44 px
 * touch target that overhangs (negative margin); `inline={false}` takes its full 44 px in a row
 * of buttons, so it never overlaps a neighbour.
 */
export function HelpButton({ topic, what, className, inline = true }: { topic: string; what: string; className?: string; inline?: boolean }) {
  const palette = usePaletteMaybe();
  if (!palette) return null;
  return (
    <button
      type="button"
      aria-label={`Help: ${what}`}
      title={`Help: ${what}`}
      data-help={topic}
      onClick={(event) => {
        // Inside a card that opens something on click, the "?" only opens help.
        event.stopPropagation();
        event.preventDefault();
        palette.openHelp(topic);
      }}
      className={cn(
        "inline-flex h-11 w-11 shrink-0 items-center justify-center rounded-full text-muted transition-colors hover:bg-hover hover:text-accent",
        inline ? "-m-2.5 md:m-0 md:h-6 md:w-6" : "md:h-8 md:w-8",
        className,
      )}
    >
      <CircleHelp className="h-4 w-4" aria-hidden />
    </button>
  );
}
