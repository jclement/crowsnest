// AnswerForm: answering one open question, shaped by its kind (DESIGN.md 8.1). Confirm and single
// choice are large option buttons (one press answers, with the note typed above it); multi-select
// is checkboxes plus Submit; ack is one button; manual is free text; a notice is dismissed. The
// agent's recommended option is marked, never preselected: the human decides.
// With voice on, the note can be dictated. Files (a screenshot, a sketch: DESIGN.md 8.6) go with
// the answer by paste, drop or the paperclip; the asker gets them as files it can read.
import { Check, CheckCheck, Send, Star, X } from "lucide-react";
import { useCallback, useId, useRef, useState } from "react";
import { ApiError } from "../../lib/api";
import { cn } from "../../lib/cn";
import { buildAnswer, canSubmit, type Question, type QuestionOption } from "../../lib/questions";
import { useAnswerQuestion, useMe } from "../../lib/queries";
import { AttachButtons, AttachmentTray, DropOverlay } from "../attach/AttachmentTray";
import { uploadHold, useAttachments, useFileDrop, usePasteToAttach } from "../attach/useAttachments";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { useToast } from "../ui/toast-context";
import { DictateButton } from "../voice/VoiceButtons";

interface AnswerFormProps {
  question: Question;
  onAnswered?: () => void;
}

export function AnswerForm({ question, onAnswered }: AnswerFormProps) {
  const answer = useAnswerQuestion(question.id);
  const toast = useToast();
  const noteId = useId();
  const [picked, setPicked] = useState<string[]>([]);
  const [note, setNote] = useState("");
  const voice = useMe().data?.features.voice ?? false;
  const [error, setError] = useState<string | null>(null);
  const manual = question.kind === "manual";
  const formRef = useRef<HTMLFormElement>(null);
  const noteRef = useRef<HTMLTextAreaElement>(null);
  const attachments = useAttachments();
  const insertText = useCallback((pasted: string) => {
    const box = noteRef.current;
    const start = box?.selectionStart ?? Number.MAX_SAFE_INTEGER;
    const end = box?.selectionEnd ?? start;
    setNote((current) => current.slice(0, start) + pasted + current.slice(end));
  }, []);
  const onPaste = usePasteToAttach(attachments, insertText);
  const dropping = useFileDrop(formRef, attachments.add, attachments.enabled);
  const hold = uploadHold(attachments);

  const submit = (keys: string[]) => {
    if (!canSubmit(question, keys, note)) return;
    if (hold) {
      setError(hold);
      return;
    }
    setError(null);
    const request = { ...buildAnswer(question, keys, note), ...(attachments.ids.length > 0 ? { attachments: attachments.ids } : {}) };
    // mutateAsync rather than mutate's own callbacks: the hook awaits a refetch that replaces this
    // component (see DialogCard), and React Query drops an unmounted caller's callbacks (M9).
    answer.mutateAsync(request).then(
      () => {
        attachments.clear();
        toast.show({ tone: "success", title: `Answered ${question.id}`, description: question.title });
        onAnswered?.();
      },
      (failure: unknown) => {
        setError(failure instanceof ApiError && failure.status === 409 ? "Someone already answered this, or it expired." : failure instanceof Error ? failure.message : String(failure));
      },
    );
  };
  const toggle = (key: string) => setPicked((current) => (current.includes(key) ? current.filter((item) => item !== key) : [...current, key]));

  return (
    <form
      ref={formRef}
      aria-label="Answer"
      className="relative flex flex-col gap-3"
      onSubmit={(event) => {
        event.preventDefault();
        submit(picked);
      }}
    >
      <DropOverlay active={dropping} label="Drop files to send with the answer" />
      {error && (
        <Banner tone="danger" role="alert" className="rounded">
          {error}
        </Banner>
      )}
      <div className="flex flex-col gap-1">
        <div className="flex items-center justify-between gap-2">
          <label htmlFor={noteId} className="text-xs font-medium text-heading">
            {manual ? "Your answer" : "Note (optional; it outranks the choice)"}
          </label>
          <span className="flex items-center gap-1.5">
            {voice && <DictateButton onText={(spoken) => setNote((current) => (current.trim() ? `${current.trimEnd()} ${spoken}` : spoken))} />}
            <AttachButtons attachments={attachments} />
          </span>
        </div>
        <textarea
          ref={noteRef}
          id={noteId}
          value={note}
          onChange={(event) => setNote(event.target.value)}
          onPaste={onPaste}
          rows={manual ? 4 : 2}
          maxLength={8000}
          placeholder={manual ? "Write the answer the agent asked for" : "Add context, a condition, or a different idea"}
          className="w-full resize-y rounded border border-border-strong bg-surface px-2.5 py-2 text-sm text-heading placeholder:text-muted focus:border-accent focus:outline-none md:text-xs"
        />
        <AttachmentTray attachments={attachments} className="mt-1" />
        {hold && <p className="text-2xs font-medium text-warning">{hold}</p>}
      </div>

      {(question.kind === "confirm" || (question.kind === "choice" && !question.multi)) && (
        <ul aria-label="Options" className="flex flex-col gap-1.5">
          {question.options.map((option) => (
            <li key={option.key}>
              <OptionButton option={option} recommended={option.key === question.recommend} disabled={answer.isPending || !!hold} onPress={() => submit([option.key])} />
            </li>
          ))}
        </ul>
      )}

      {question.kind === "choice" && question.multi && (
        <fieldset className="flex flex-col gap-1.5">
          <legend className="mb-1 text-xs text-muted">Pick any number</legend>
          {question.options.map((option) => (
            <OptionCheckbox
              key={option.key}
              option={option}
              recommended={option.key === question.recommend}
              checked={picked.includes(option.key)}
              onToggle={() => toggle(option.key)}
            />
          ))}
          <Button type="submit" variant="primary" icon={Send} disabled={picked.length === 0 || answer.isPending} className="mt-1 self-start">
            Submit {picked.length > 0 ? `(${picked.length})` : ""}
          </Button>
        </fieldset>
      )}

      {question.kind === "ack" && (
        <Button type="submit" variant="primary" icon={CheckCheck} disabled={answer.isPending} className="self-start">
          Acknowledge
        </Button>
      )}
      {manual && (
        <Button type="submit" variant="primary" icon={Send} disabled={!note.trim() || answer.isPending} className="self-start">
          Send answer
        </Button>
      )}
      {question.kind === "notice" && (
        <Button type="submit" icon={X} disabled={answer.isPending} className="self-start">
          Dismiss
        </Button>
      )}
    </form>
  );
}

function OptionButton({ option, recommended, disabled, onPress }: { option: QuestionOption; recommended: boolean; disabled: boolean; onPress: () => void }) {
  return (
    <button
      type="button"
      onClick={onPress}
      disabled={disabled}
      aria-describedby={undefined}
      className={cn(
        "flex min-h-12 w-full items-start gap-2.5 rounded-md border px-3 py-2.5 text-left transition-colors disabled:opacity-50 md:min-h-10",
        recommended ? "border-accent/60 bg-accent/5 hover:bg-accent/10" : "border-border-strong bg-surface hover:border-accent hover:bg-hover",
      )}
    >
      <Check className={cn("mt-0.5 h-4 w-4 shrink-0", recommended ? "text-accent" : "text-muted")} aria-hidden />
      <span className="min-w-0 flex-1">
        <span className="block break-words text-sm font-medium text-heading">{option.label}</span>
        {option.detail && <span className="mt-0.5 block break-words text-xs text-muted">{option.detail}</span>}
      </span>
      {recommended && <RecommendedMark />}
    </button>
  );
}

function OptionCheckbox({ option, recommended, checked, onToggle }: { option: QuestionOption; recommended: boolean; checked: boolean; onToggle: () => void }) {
  return (
    <label
      className={cn(
        "flex min-h-12 cursor-pointer items-start gap-2.5 rounded-md border px-3 py-2.5 transition-colors md:min-h-10",
        checked ? "border-accent bg-selected" : "border-border-strong bg-surface hover:bg-hover",
      )}
    >
      <input type="checkbox" checked={checked} onChange={onToggle} className="mt-0.5 h-4 w-4 shrink-0 accent-[var(--color-accent)]" />
      <span className="min-w-0 flex-1">
        <span className="block break-words text-sm font-medium text-heading">{option.label}</span>
        {option.detail && <span className="mt-0.5 block break-words text-xs text-muted">{option.detail}</span>}
      </span>
      {recommended && <RecommendedMark />}
    </label>
  );
}

function RecommendedMark() {
  return (
    <span className="inline-flex shrink-0 items-center gap-1 rounded bg-accent/10 px-1.5 py-0.5 text-2xs font-medium text-accent">
      <Star className="h-3 w-3" aria-hidden />
      Recommended
    </span>
  );
}
