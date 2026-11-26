// QuestionDetail: one question in full (DESIGN.md 8.1 and 12): title, context chips, the body
// through the one Markdown renderer (mermaid and code included), the files as a diff, and the
// answer form while it is open, or the outcome once it has settled. With voice on, an open
// question has "Read aloud".
import { Ban, Clock, RefreshCw } from "lucide-react";
import { relativeTime } from "../../lib/format";
import { answerSummary, kindLabels, statusLabels, type Question } from "../../lib/questions";
import { useCancelQuestion, useMe, useQuestion } from "../../lib/queries";
import { Markdown } from "../Markdown";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Chip } from "../ui/Chip";
import { SkeletonRows } from "../ui/Skeleton";
import { useToast } from "../ui/toast-context";
import { AnswerForm } from "./AnswerForm";
import { DiffView } from "./DiffView";
import { ReadAloudButton } from "../voice/VoiceButtons";

interface QuestionDetailProps {
  id: string;
  /** Called after an answer or cancel, e.g. to close the mobile sheet. */
  onDone?: () => void;
}

export function QuestionDetail({ id, onDone }: QuestionDetailProps) {
  const query = useQuestion(id);
  if (query.isPending) return <SkeletonRows rows={6} label="Loading the question" />;
  if (query.isError) {
    return (
      <Banner tone="danger" role="alert" action={<Button compact icon={RefreshCw} onClick={() => void query.refetch()}>Retry</Button>}>
        Could not load {id}: {query.error.message}
      </Banner>
    );
  }
  return <QuestionBody question={query.data} onDone={onDone} />;
}

export function QuestionBody({ question, onDone }: { question: Question; onDone?: () => void }) {
  const voice = useMe().data?.features.voice ?? false;
  return (
    <article aria-label={question.title} className="flex max-w-4xl flex-col gap-3 p-3 md:p-4">
      <div className="flex items-start gap-2">
        <h2 className="min-w-0 flex-1 break-words text-base font-semibold leading-snug text-heading">{question.title}</h2>
        {voice && question.status === "open" && <ReadAloudButton questionId={question.id} />}
      </div>
      <QuestionMeta question={question} />
      {question.body_md.trim() && <Markdown className="text-sm">{question.body_md}</Markdown>}
      {question.files && question.files.length > 0 && <DiffView files={question.files} />}
      {question.status === "open" ? (
        <>
          <AnswerForm question={question} onAnswered={onDone} />
          <CancelButton question={question} onDone={onDone} />
        </>
      ) : (
        <Outcome question={question} />
      )}
    </article>
  );
}

function QuestionMeta({ question }: { question: Question }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <Chip mono>{question.id}</Chip>
      <Chip tone="accent">{question.multi ? "Pick several" : kindLabels[question.kind]}</Chip>
      {question.urgency === "blocking" && <Chip tone="danger">Blocking</Chip>}
      {question.urgency === "low" && <Chip>Low</Chip>}
      {question.status !== "open" && <Chip tone={question.status === "answered" ? "success" : "warning"}>{statusLabels[question.status]}</Chip>}
      {Object.entries(question.context).map(([key, value]) => (
        <Chip key={key} mono title={key}>
          <span className="text-muted">{key}</span> {value}
        </Chip>
      ))}
      <span className="ml-auto inline-flex items-center gap-1 text-2xs text-muted" title={question.created_at}>
        <Clock className="h-3 w-3" aria-hidden />
        {relativeTime(question.created_at)}
        {question.status === "open" && <> · expires {relativeTime(question.expires_at)}</>}
      </span>
    </div>
  );
}

function Outcome({ question }: { question: Question }) {
  const delivered =
    question.delivery === "poll"
      ? "The agent was waiting and got the answer at once."
      : question.delivery === "type"
        ? question.delivered_at
          ? "Typed back to the agent."
          : question.delivery_error
            ? `Not delivered: ${question.delivery_error}`
            : "Waiting to type it back to the agent (it may be at a dialog)."
        : null;
  return (
    <section aria-label="Outcome" className="flex flex-col gap-1 rounded-md border border-border bg-surface-raised p-3">
      <p className="text-sm font-medium text-heading">{answerSummary(question)}</p>
      {question.answer_note && question.kind !== "manual" && <p className="whitespace-pre-wrap text-xs text-body">Note: {question.answer_note}</p>}
      {question.status === "expired" && <p className="text-xs text-muted">Expiry is not consent; the agent was told there is no answer.</p>}
      {delivered && <p className="text-2xs text-muted">{delivered}</p>}
    </section>
  );
}

function CancelButton({ question, onDone }: { question: Question; onDone?: () => void }) {
  const cancel = useCancelQuestion(question.id);
  const toast = useToast();
  if (question.kind === "notice") return null;
  return (
    <Button
      compact
      variant="ghost"
      icon={Ban}
      disabled={cancel.isPending}
      className="self-start text-muted"
      onClick={() =>
        cancel.mutate(undefined, {
          onSuccess: () => {
            toast.show({ tone: "info", title: `Cancelled ${question.id}`, description: "The agent is told there is no answer." });
            onDone?.();
          },
          onError: (error) => toast.show({ tone: "danger", title: "Not cancelled", description: error.message }),
        })
      }
    >
      Cancel question
    </Button>
  );
}
