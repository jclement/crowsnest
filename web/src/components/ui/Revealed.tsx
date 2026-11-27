// Revealed: text with what it hides made visible (R7-S3). Each invisible or direction-changing
// character is drawn as a chip naming its code point (the character itself is not rendered, so
// it cannot reorder what follows), and a word that mixes Latin with look-alike letters from
// another alphabet is underlined with a note. HiddenWarning is the one-line warning a view puts
// above such text. Both render text nodes only.
import { TriangleAlert } from "lucide-react";
import { cn } from "../../lib/cn";
import { describeHidden, hidesSomething, revealHidden, type HiddenReport } from "../../lib/hidden";

export function Revealed({ text }: { text: string }) {
  const pieces = revealHidden(text);
  if (pieces.length === 1 && pieces[0]?.kind === "text") return <>{text}</>;
  return (
    <>
      {pieces.map((piece, index) => {
        if (piece.kind === "invisible") {
          return (
            <span
              key={index}
              data-hidden-char={piece.code}
              title={`Invisible character ${piece.code}`}
              aria-label={`invisible character ${piece.code}`}
              className="mx-px rounded-sm bg-danger/20 px-0.5 font-mono text-[0.85em] text-danger"
            >
              ⟨{piece.code}⟩
            </span>
          );
        }
        if (piece.kind === "mixed") {
          return (
            <span key={index} data-mixed-script="" title="This word mixes Latin with look-alike letters from another alphabet" className="underline decoration-danger decoration-wavy">
              {piece.text}
            </span>
          );
        }
        return <span key={index}>{piece.text}</span>;
      })}
    </>
  );
}

export function HiddenWarning({ report, what, className }: { report: HiddenReport; what: string; className?: string }) {
  if (!hidesSomething(report)) return null;
  return (
    <p role="alert" data-testid="hidden-warning" className={cn("flex items-start gap-1 rounded border border-danger/40 bg-danger/10 px-2 py-1 font-sans text-2xs text-danger", className)}>
      <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden />
      <span>
        {what} contains {describeHidden(report)}. Text like this can read differently from what it does: read it with the markers shown.
      </span>
    </p>
  );
}
