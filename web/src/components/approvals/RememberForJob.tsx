// RememberForJob: the inline confirm behind "Allow and remember for this job" on an ApprovalCard
// (DESIGN.md 8.7, Policy). It shows the job rule the server proposed (for Bash the command's first
// two words and *, for an edit the file's directory, otherwise the tool), editable, and says in one
// sentence what it will do until the job ends. Never offered for a dangerous call. For an elevated
// call the server offers only the call itself (exact, R6-S1), and only for Bash (R7-S1: a tool rule
// would match any input): shown read-only, never a prefix.
import { BookmarkCheck } from "lucide-react";
import { useId, useState } from "react";
import type { Approval } from "../../lib/approvals";
import { kindExamples, type RuleKind } from "../../lib/policy";
import { Button } from "../ui/Button";

interface RememberForJobProps {
  approval: Approval;
  busy: boolean;
  onConfirm: (pattern: string) => void;
  onCancel: () => void;
}

const what: Record<RuleKind, (pattern: string) => string> = {
  bash: (pattern) => `Bash commands matching \`${pattern}\``,
  edit: (pattern) => (pattern === "." ? "edits anywhere in its working directory" : `edits under \`${pattern.replace(/\/+$/, "")}/\``),
  tool: (pattern) => (pattern.endsWith("*") ? `tools starting with \`${pattern.slice(0, -1)}\`` : `the tool \`${pattern}\``),
};

export function RememberForJob({ approval, busy, onConfirm, onCancel }: RememberForJobProps) {
  const suggestion = approval.remember ?? { kind: "tool" as const, pattern: approval.tool_name };
  const [pattern, setPattern] = useState(suggestion.pattern);
  const fieldId = useId();
  const trimmed = pattern.trim();
  const exact = suggestion.exact === true;
  return (
    <form
      aria-label="Remember for this job"
      className="flex flex-col gap-2 rounded border border-accent/40 bg-accent/5 p-2.5"
      onSubmit={(event) => {
        event.preventDefault();
        if (trimmed) onConfirm(trimmed);
      }}
    >
      <label htmlFor={fieldId} className="text-2xs text-muted">
        Pattern ({suggestion.kind}): {kindExamples[suggestion.kind].hint}
      </label>
      <input
        id={fieldId}
        value={pattern}
        onChange={(event) => setPattern(event.target.value)}
        readOnly={exact}
        maxLength={300}
        spellCheck={false}
        autoCapitalize="off"
        className="min-h-11 w-full rounded border border-border-strong bg-surface px-2 font-mono text-sm text-heading md:min-h-8 md:text-xs"
      />
      <p data-testid="remember-sentence" className="text-xs text-body">
        {exact ? (
          <>
            Until {approval.job_id} ends, crowsnest will allow exactly this command, word for word, without asking, whatever the scripts or files it runs say
            at the time. Every other elevated call (an unrecognised program, an interpreter, a different command, any other tool) and every dangerous call is
            still asked.
          </>
        ) : (
          <>
            Until {approval.job_id} ends, crowsnest will allow {what[suggestion.kind](trimmed || "…")} without asking, but only while each call is low
            risk. Elevated and dangerous calls are always asked.
          </>
        )}
      </p>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" icon={BookmarkCheck} disabled={busy || !trimmed}>
          Allow and remember
        </Button>
        <Button variant="secondary" disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
