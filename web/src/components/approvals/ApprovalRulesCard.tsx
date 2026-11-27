// ApprovalRulesCard: a project's approval rules on its page (DESIGN.md 8.7, Policy). Each rule
// reads as a sentence ("Allow Bash commands matching `go test *`"), marked when it also allows
// elevated calls, with when it was added, what it matched among the project's recent calls (and
// any dangerous call it matches, which is never auto-allowed), and Remove. The form below adds
// one: the kind, a pattern with an example per kind, "also when elevated" with its warning, and a
// live preview of what the pattern would have matched before it is saved (R5).
import { Plus, ShieldAlert, Trash2 } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { relativeTime } from "../../lib/format";
import {
  kindExamples,
  kindLabels,
  previewLine,
  useAddProjectRule,
  useProjectRules,
  useRemoveRule,
  useRulePreview,
  type ApprovalRule,
  type RuleKind,
  type RulePreview,
} from "../../lib/policy";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Chip } from "../ui/Chip";
import { SkeletonRows } from "../ui/Skeleton";
import { useToast } from "../ui/toast-context";

export function ApprovalRulesCard({ projectRef, archived }: { projectRef: string; archived?: boolean }) {
  const rules = useProjectRules(projectRef);
  const list = rules.data ?? [];
  return (
    <Card title="Approval rules" aria-label="Approval rules" bodyClassName="p-0">
      <p className="px-3 pt-2 text-2xs text-muted">
        Calls a rule matches are allowed at once, without asking, and journalled. A dangerous call is never allowed by a rule. Rules live in crowsnest, not in the
        agents&apos; settings.
      </p>
      {rules.isPending ? (
        <SkeletonRows rows={2} label="Loading the rules" />
      ) : rules.isError ? (
        <Banner tone="danger" role="alert" className="m-3 rounded">
          Could not load the rules: {rules.error.message}
        </Banner>
      ) : list.length === 0 ? (
        <p className="px-3 py-2 text-xs text-muted">No rules: every call that needs permission is asked.</p>
      ) : (
        <ul className="divide-y divide-border" aria-label="Rules">
          {list.map((rule) => (
            <RuleRow key={rule.id} rule={rule} />
          ))}
        </ul>
      )}
      {!archived && <AddRuleForm projectRef={projectRef} />}
    </Card>
  );
}

function RuleRow({ rule }: { rule: ApprovalRule }) {
  const remove = useRemoveRule();
  const toast = useToast();
  const onRemove = () =>
    remove
      .mutateAsync(rule.id)
      .then(() => toast.show({ tone: "info", title: "Rule removed", description: rule.sentence }))
      .catch((error: Error) => toast.show({ tone: "danger", title: "Could not remove the rule", description: error.message }));
  return (
    <li data-rule={rule.id} className="flex flex-wrap items-start gap-x-2 gap-y-1 px-3 py-2 text-xs">
      <div className="min-w-0 flex-1">
        <p className="break-words text-sm text-heading">
          <Sentence text={rule.sentence} />
          {rule.max_risk === "elevated" && (
            <Chip tone="warning" className="ml-1.5 align-middle">
              also when elevated
            </Chip>
          )}
        </p>
        <p className="text-2xs text-muted">
          {rule.id} · added {relativeTime(rule.created_at)}
          {rule.created_via === "demo" && " (demo)"}
          {rule.note && <> · {rule.note}</>}
        </p>
        {rule.recent && <PreviewLines preview={rule.recent} />}
      </div>
      <Button compact variant="ghost" icon={Trash2} onClick={() => void onRemove()} disabled={remove.isPending} aria-label={`Remove ${rule.id}: ${rule.sentence}`}>
        Remove
      </Button>
    </li>
  );
}

/** A rule sentence with its `backticked` pattern in mono. */
function Sentence({ text }: { text: string }) {
  const parts = text.split("`");
  return (
    <>
      {parts.map((part, index) =>
        index % 2 === 1 ? (
          <code key={index} className="rounded bg-surface-sunken px-1 font-mono text-xs">
            {part}
          </code>
        ) : (
          <span key={index}>{part}</span>
        ),
      )}
    </>
  );
}

/** What a rule would have matched recently, with any dangerous match flagged. */
function PreviewLines({ preview }: { preview: RulePreview }) {
  return (
    <div className="flex flex-col gap-0.5 pt-0.5 text-2xs" data-testid="rule-preview">
      <p className="break-words text-body">{previewLine(preview)}</p>
      {preview.above_max_risk.length > 0 && (
        <p className="break-words text-warning">
          Also matches {preview.above_max_risk.length} elevated call{preview.above_max_risk.length === 1 ? "" : "s"}, still asked unless &ldquo;also when
          elevated&rdquo; is ticked.
        </p>
      )}
      {preview.dangerous.length > 0 && (
        <p className="flex items-start gap-1 break-words font-medium text-danger">
          <ShieldAlert className="mt-px h-3 w-3 shrink-0" aria-hidden />
          <span>
            Matches {preview.dangerous.length} dangerous call{preview.dangerous.length === 1 ? "" : "s"} ({preview.dangerous.map((match) => match.summary).join("; ")}): never
            auto-allowed.
          </span>
        </p>
      )}
    </div>
  );
}

/** The pattern after the human stopped typing for a moment, so the preview is not asked per key. */
function useSettled(value: string, delay = 300): string {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(value), delay);
    return () => window.clearTimeout(timer);
  }, [value, delay]);
  return settled;
}

function AddRuleForm({ projectRef }: { projectRef: string }) {
  const add = useAddProjectRule(projectRef);
  const toast = useToast();
  const [kind, setKind] = useState<RuleKind>("bash");
  const [pattern, setPattern] = useState("");
  const [elevated, setElevated] = useState(false);
  const [note, setNote] = useState("");
  const [error, setError] = useState<string | null>(null);
  const ids = { kind: useId(), pattern: useId(), elevated: useId(), note: useId() };
  const settledPattern = useSettled(pattern.trim());
  const maxRisk = elevated ? "elevated" : "low";
  const preview = useRulePreview(projectRef, { kind, pattern: settledPattern, max_risk: maxRisk }, settledPattern !== "");

  const submit = () => {
    setError(null);
    add.mutateAsync({ kind, pattern: pattern.trim(), max_risk: maxRisk, note: note.trim() || undefined }).then(
      (rule) => {
        toast.show({ tone: "success", title: "Rule added", description: rule.sentence });
        setPattern("");
        setNote("");
        setElevated(false);
      },
      (failure: Error) => setError(failure.message),
    );
  };
  const field = "min-h-11 rounded border border-border-strong bg-surface px-2 text-sm text-heading md:min-h-8 md:text-xs";
  return (
    <form
      aria-label="Add a rule"
      className="flex flex-col gap-2 border-t border-border px-3 py-2.5"
      onSubmit={(event) => {
        event.preventDefault();
        if (pattern.trim()) submit();
      }}
    >
      <div className="flex flex-wrap items-end gap-2">
        <label className="flex flex-col gap-0.5 text-2xs text-muted" htmlFor={ids.kind}>
          Kind
          <select id={ids.kind} value={kind} onChange={(event) => setKind(event.target.value as RuleKind)} className={field}>
            {(Object.keys(kindLabels) as RuleKind[]).map((value) => (
              <option key={value} value={value}>
                {kindLabels[value]}
              </option>
            ))}
          </select>
        </label>
        <label className="flex min-w-0 flex-1 basis-48 flex-col gap-0.5 text-2xs text-muted" htmlFor={ids.pattern}>
          Pattern
          <input
            id={ids.pattern}
            value={pattern}
            onChange={(event) => setPattern(event.target.value)}
            placeholder={kindExamples[kind].placeholder}
            maxLength={300}
            spellCheck={false}
            autoCapitalize="off"
            className={`${field} font-mono`}
          />
        </label>
      </div>
      <p className="text-2xs text-muted">{kindExamples[kind].hint}</p>
      <label htmlFor={ids.elevated} className="flex min-h-11 items-start gap-2 text-xs text-body md:min-h-0">
        <input id={ids.elevated} type="checkbox" checked={elevated} onChange={(event) => setElevated(event.target.checked)} className="mt-0.5 h-4 w-4 shrink-0" />
        <span>
          Also when elevated
          <span className="block text-2xs text-warning">Elevated calls push, install, reach the network or write outside the job. Tick only for a pattern you trust that far.</span>
        </span>
      </label>
      <label className="flex flex-col gap-0.5 text-2xs text-muted" htmlFor={ids.note}>
        Note (optional)
        <input id={ids.note} value={note} onChange={(event) => setNote(event.target.value)} maxLength={500} className={field} placeholder="why this is safe" />
      </label>
      {settledPattern !== "" &&
        (preview.isError ? (
          <p role="status" className="text-2xs text-danger">
            {preview.error.message}
          </p>
        ) : preview.data ? (
          <PreviewLines preview={preview.data} />
        ) : null)}
      {error && (
        <Banner tone="danger" role="alert" className="rounded">
          {error}
        </Banner>
      )}
      <div>
        <Button type="submit" variant="primary" icon={Plus} disabled={add.isPending || !pattern.trim()}>
          Add rule
        </Button>
      </div>
    </form>
  );
}
