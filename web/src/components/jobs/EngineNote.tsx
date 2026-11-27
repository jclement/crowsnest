// EngineNote: under an engine picker, the plain note of what Codex does not get from crowsnest
// (lib/engines.ts). Nothing for Claude, the engine crowsnest supervises in full.
import { Info } from "lucide-react";
import { codexNote } from "../../lib/engines";

export function EngineNote({ engine, className }: { engine: string; className?: string }) {
  if (engine !== "codex") return null;
  return (
    <p role="note" aria-label="What Codex does not get" className={`flex items-start gap-1.5 text-2xs font-normal text-muted ${className ?? ""}`}>
      <Info className="mt-px h-3.5 w-3.5 shrink-0" aria-hidden />
      {codexNote()}
    </p>
  );
}
