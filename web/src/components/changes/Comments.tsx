// Line comments on a diff (DESIGN.md 6.21): the editor a note is written in, a note as it shows
// (where it is, draft / sent / outdated, the quoted code when it is not drawn beside its lines, the
// note, and what can be done with it), and the panel that lists a subject's notes and sends them to
// the agent as one message, with an optional overall note and a preview of exactly what will be
// typed. Quotes are code the agent wrote and notes go to its terminal, so both are shown with
// hidden characters revealed and a warning (R7-S3). Text nodes only.
import { MessageSquarePlus, Pencil, RotateCcw, Send, Trash2, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { ApiError } from "../../lib/api";
import { cn } from "../../lib/cn";
import { relativeTime } from "../../lib/format";
import { sendLabel, sentText, statusText, whereText, type CommentList, type DiffComment, type SendResult } from "../../lib/comments";
import { commentsApi, useCommentActions } from "../../lib/comments-queries";
import type { DiffWho } from "../../lib/changes";
import { findHidden } from "../../lib/hidden";
import { Button, IconButton } from "../ui/Button";
import { Chip } from "../ui/Chip";
import { HiddenWarning, Revealed } from "../ui/Revealed";
import { useToast } from "../ui/toast-context";

// ---- The editor ---------------------------------------------------------------------------------

export function CommentEditor({ initial = "", where, onSave, onCancel, busy, saveLabel = "Save comment" }: {
  initial?: string;
  /** What the note is on, for the label ("lines 12–14 of server/checkout.go"). */
  where: string;
  onSave: (body: string) => void;
  onCancel: () => void;
  busy?: boolean;
  saveLabel?: string;
}) {
  const [body, setBody] = useState(initial);
  const area = useRef<HTMLTextAreaElement>(null);
  useEffect(() => area.current?.focus(), []);
  const save = () => body.trim() && onSave(body);
  return (
    <form
      aria-label={`Comment on ${where}`}
      className="flex flex-col gap-1.5 rounded border border-accent/40 bg-surface-raised p-2 font-sans"
      onSubmit={(event) => {
        event.preventDefault();
        save();
      }}
    >
      <label className="text-2xs text-muted" htmlFor={undefined}>
        Ask for a change on <span className="font-medium text-body">{where}</span>
      </label>
      <textarea
        ref={area}
        value={body}
        onChange={(event) => setBody(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            save();
          } else if (event.key === "Escape") {
            event.preventDefault();
            event.stopPropagation();
            onCancel();
          }
        }}
        rows={3}
        aria-label="Comment"
        placeholder="What should the agent change here?"
        className="min-h-20 w-full resize-y rounded border border-border bg-surface p-2 text-sm text-body outline-none focus:border-accent"
      />
      <HiddenWarning report={findHidden(body)} what="This comment" />
      <div className="flex flex-wrap items-center gap-2">
        <Button compact variant="primary" type="submit" disabled={!body.trim() || busy}>
          {busy ? "Saving…" : saveLabel}
        </Button>
        <Button compact variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <span className="text-2xs text-muted">Saved as a draft; nothing goes to the agent until you send.</span>
      </div>
    </form>
  );
}

// ---- One note -----------------------------------------------------------------------------------

const statusTone = { draft: "accent", sent: "success", outdated: "warning", waiting: "neutral" } as const;

export function CommentCard({ comment, who, showPath, showQuote, className }: {
  comment: DiffComment;
  who: DiffWho;
  /** In the panel the file is named; beside its lines it is not. */
  showPath?: boolean;
  /** The quote: shown when the note is not drawn beside the lines it is about. */
  showQuote?: boolean;
  className?: string;
}) {
  const [editing, setEditing] = useState(false);
  const actions = useCommentActions(who);
  const toast = useToast();
  const fail = (title: string) => (error: Error) => toast.show({ tone: "danger", title, description: error.message });
  const hidden = useMemo(() => findHidden(comment.quote, comment.body), [comment.quote, comment.body]);
  const where = whereText(comment);
  if (editing) {
    return (
      <CommentEditor
        initial={comment.body}
        where={where}
        busy={actions.edit.isPending}
        onCancel={() => setEditing(false)}
        onSave={(body) => actions.edit.mutate({ id: comment.id, body }, { onSuccess: () => setEditing(false), onError: fail("Could not save the comment") })}
      />
    );
  }
  return (
    <article
      aria-label={`Comment on ${comment.path}, ${where}`}
      data-comment-status={comment.status}
      className={cn("flex flex-col gap-1 rounded border bg-surface-raised p-2 font-sans text-xs", comment.status === "outdated" ? "border-warning/40" : "border-border", className)}
    >
      <header className="flex flex-wrap items-center gap-1.5">
        {showPath && (
          <span className="min-w-0 break-all font-mono text-2xs text-heading">
            <Revealed text={comment.path} />
          </span>
        )}
        <span className="text-2xs text-muted">{where}</span>
        <Chip tone={statusTone[comment.status]}>{statusText[comment.status]}</Chip>
        {comment.status === "outdated" && comment.pending && <Chip tone="accent">goes with the next send</Chip>}
        {comment.uncertain && (
          <Chip tone="warning" title="crowsnest stopped while sending it: it may or may not have reached the agent. Send it again if the agent did not get it.">
            may not have arrived
          </Chip>
        )}
        {comment.sent_at && <span className="text-2xs text-muted">sent {new Date(comment.sent_at).toLocaleString()}</span>}
        <span className="ml-auto flex items-center">
          {comment.pending && <IconButton icon={Pencil} label={`Edit the comment on ${where}`} onClick={() => setEditing(true)} />}
          {comment.pending && (
            <IconButton icon={Trash2} label={`Delete the comment on ${where}`} onClick={() => actions.remove.mutate(comment.id, { onError: fail("Could not delete it") })} />
          )}
          {!comment.pending && !comment.waiting && (
            <Button compact variant="ghost" icon={RotateCcw} onClick={() => actions.resend.mutate(comment.id, { onError: fail("Could not re-send it") })}>
              Re-send
            </Button>
          )}
          {comment.status === "outdated" && (
            <Button compact variant="ghost" icon={X} onClick={() => actions.dismiss.mutate(comment.id, { onError: fail("Could not dismiss it") })}>
              Dismiss
            </Button>
          )}
        </span>
      </header>
      <HiddenWarning report={hidden} what="This comment or the code it quotes" />
      {comment.status === "outdated" && <p className="text-2xs text-warning">The file has changed since this was written; the code it was about:</p>}
      {(showQuote || comment.status === "outdated") && comment.quote && (
        <pre className="max-h-40 overflow-auto rounded bg-surface-sunken p-1.5 font-mono text-2xs text-body">
          <Revealed text={comment.quote} />
        </pre>
      )}
      <p className="whitespace-pre-wrap break-words text-sm text-body">
        <Revealed text={comment.body} />
      </p>
    </article>
  );
}

// ---- The panel: every note, and Send ------------------------------------------------------------

export function CommentsPanel({ who, list, onJump, className }: {
  who: DiffWho;
  list: CommentList;
  /** Shows a note's file in the viewer. */
  onJump?: (path: string) => void;
  className?: string;
}) {
  const byFile = useMemo(() => {
    const groups = new Map<string, DiffComment[]>();
    for (const comment of list.comments) groups.set(comment.path, [...(groups.get(comment.path) ?? []), comment]);
    return [...groups.entries()];
  }, [list.comments]);
  return (
    <div className={cn("flex flex-col gap-3", className)}>
      <SendForm who={who} list={list} />
      {byFile.length === 0 ? (
        <p className="text-sm text-muted">
          No comments yet. Select a line (or drag across a range) by its number in the diff, press <kbd>c</kbd> or Comment, and write what should change.
        </p>
      ) : (
        byFile.map(([path, comments]) => (
          <section key={path} aria-label={`Comments on ${path}`} className="flex flex-col gap-1.5">
            <h3 className="flex items-center gap-1 font-mono text-2xs text-heading">
              {onJump ? (
                <button type="button" className="break-all text-left underline decoration-dotted hover:text-accent" onClick={() => onJump(path)}>
                  <Revealed text={path} />
                </button>
              ) : (
                <Revealed text={path} />
              )}
            </h3>
            {comments.map((comment) => (
              <CommentCard key={comment.id} comment={comment} who={who} showQuote />
            ))}
          </section>
        ))
      )}
    </div>
  );
}

/** The overall note, the preview of the one message, and Send. */
export function SendForm({ who, list, compact }: { who: DiffWho; list: CommentList; compact?: boolean }) {
  const [overall, setOverall] = useState("");
  const [preview, setPreview] = useState<SendResult | null>(null);
  const [previewing, setPreviewing] = useState(false);
  const [draftInBox, setDraftInBox] = useState(false);
  const actions = useCommentActions(who);
  const toast = useToast();
  const target = who.kind === "job" ? "the agent" : who.kind === "project" ? "the lead" : "the orchestrator";
  const showPreview = async () => {
    setPreviewing(true);
    try {
      setPreview(await commentsApi.send(who, overall, true));
    } catch (error) {
      toast.show({ tone: "danger", title: "Could not compose the message", description: (error as Error).message });
    } finally {
      setPreviewing(false);
    }
  };
  const send = (force = false) =>
    actions.send.mutate(
      { overall, force },
      {
        onSuccess: (result) => {
          setOverall("");
          setPreview(null);
          setDraftInBox(false);
          toast.show({ tone: result.how === "waiting" ? "warning" : "success", title: "Comments sent", description: sentText(result) });
        },
        onError: (error) => {
          if (error instanceof ApiError && error.code === "DRAFT") setDraftInBox(true);
          toast.show({ tone: "danger", title: "Not sent", description: error.message });
        },
      },
    );
  const hidden = preview ? findHidden(preview.message) : null;
  return (
    <section aria-label="Send comments" className="flex flex-col gap-1.5 rounded border border-border bg-surface p-2">
      <div className="flex flex-wrap items-center gap-1.5 text-xs text-body">
        <MessageSquarePlus className="h-4 w-4 text-muted" aria-hidden />
        <span>
          {list.drafts} to send · {list.sent} sent{list.outdated > 0 ? ` · ${list.outdated} outdated` : ""}
          {(list.waiting ?? 0) > 0 ? ` · ${list.waiting} waiting to be typed` : ""}
        </span>
        {list.last_sent_at && (
          <time dateTime={list.last_sent_at} title={new Date(list.last_sent_at).toLocaleString()} className="text-2xs text-muted">
            Last sent {relativeTime(list.last_sent_at)}
          </time>
        )}
      </div>
      {list.drafts === 0 && (list.waiting ?? 0) > 0 && who.kind === "job" && (
        <p className="flex flex-wrap items-center gap-1.5 text-2xs text-muted">
          They are typed when the job may continue, never over a draft someone left in its input box.
          <Button compact variant="ghost" disabled={actions.send.isPending} onClick={() => send(true)}>
            Let them type over a draft
          </Button>
        </p>
      )}
      {list.drafts > 0 && (
        <>
          {!compact && (
            <textarea
              value={overall}
              onChange={(event) => {
                setOverall(event.target.value);
                setPreview(null);
              }}
              rows={2}
              aria-label="Overall note (optional)"
              placeholder="An overall note to go first (optional)"
              className="w-full resize-y rounded border border-border bg-surface-raised p-2 text-sm text-body outline-none focus:border-accent"
            />
          )}
          <div className="flex flex-wrap items-center gap-2">
            <Button compact variant="primary" icon={Send} disabled={actions.send.isPending} onClick={() => send(false)}>
              {actions.send.isPending ? "Sending…" : sendLabel(list.drafts).replace("the agent", target)}
            </Button>
            {draftInBox && (
              <Button compact onClick={() => send(true)}>
                Send anyway (after the draft in its input box)
              </Button>
            )}
            {!compact && (
              <Button compact variant="ghost" aria-pressed={!!preview} disabled={previewing} onClick={() => (preview ? setPreview(null) : void showPreview())}>
                {preview ? "Hide preview" : previewing ? "Composing…" : "Preview the message"}
              </Button>
            )}
          </div>
          {who.kind === "job" && list.job_state === "done" && (
            <p className="text-2xs text-muted">The job is finished: sending starts a fix job in its worktree with these comments, and the job is checked again when it is done.</p>
          )}
        </>
      )}
      {preview && (
        <div className="flex flex-col gap-1">
          {hidden && <HiddenWarning report={hidden} what="The message" />}
          <pre aria-label="The message the agent gets" className="max-h-72 overflow-auto whitespace-pre-wrap rounded bg-surface-sunken p-2 font-mono text-2xs text-body">
            <Revealed text={preview.message} />
          </pre>
        </div>
      )}
    </section>
  );
}
