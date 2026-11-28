// EngineNote: the one plain line a Codex job's card and detail carry (DESIGN.md 20.1): which of
// crowsnest's guarantees it runs without, from GET /engines. A Claude job runs without none, so it
// shows nothing.
import { useEngines } from "../../lib/queries";
import { engineInfo, withoutSentence } from "../../lib/engines";
import { cn } from "../../lib/cn";
import { HelpButton } from "../help/HelpButton";

export function EngineNote({ engine, compact, className }: { engine: string; compact?: boolean; className?: string }) {
  const table = useEngines();
  const sentence = withoutSentence(engineInfo(table.data, engine));
  if (!sentence) return null;
  if (compact) {
    // On a card it is a chip with the sentence as its title (R10, Fable polish: two lines of
    // caveat on every Codex card of the board); the detail keeps the sentence.
    return (
      <span title={sentence} className={cn("inline-flex w-fit items-center rounded border border-warning/40 px-1 text-2xs text-warning", className)}>
        <span className="sr-only">{sentence}</span>
        <span aria-hidden>Codex: fewer checks</span>
      </span>
    );
  }
  return (
    <p className={cn("flex items-start gap-1.5 text-xs text-warning", className)} data-testid="engine-note">
      <span className="min-w-0 flex-1">{sentence}</span>
      <HelpButton topic="reference/engines" what="what each engine gets" />
    </p>
  );
}
