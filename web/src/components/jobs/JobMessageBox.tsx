// A job's message box and its files (DESIGN.md 6.2, 8.6), kept apart from JobDetail so the detail
// view stays one screen of layout. The box types the human's words (and names any files sent
// with them) into the job's agent; the files card lists what went to the agent and what it
// attached to its result.
import { Send } from "lucide-react";
import { useCallback, useRef, useState, type FormEvent } from "react";
import { ApiError } from "../../lib/api";
import { useOwnerAttachments } from "../../lib/attachment-queries";
import { shownFromAttachment, type Attachment } from "../../lib/attachments";
import type { JobDetail as Detail } from "../../lib/jobs";
import { useJobMessage } from "../../lib/queries";
import { AttachmentList } from "../attach/AttachmentList";
import { AttachButtons, AttachmentTray, DropOverlay } from "../attach/AttachmentTray";
import { uploadHold, useAttachments, useFileDrop, usePasteToAttach } from "../attach/useAttachments";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { useToast } from "../ui/toast-context";

export function JobMessageBox({ job }: { job: Detail }) {
  const [text, setText] = useState("");
  const send = useJobMessage(job.id);
  const toast = useToast();
  const formRef = useRef<HTMLFormElement>(null);
  const boxRef = useRef<HTMLTextAreaElement>(null);
  const attachments = useAttachments();
  const insertText = useCallback((pasted: string) => {
    const box = boxRef.current;
    const start = box?.selectionStart ?? Number.MAX_SAFE_INTEGER;
    const end = box?.selectionEnd ?? start;
    setText((current) => current.slice(0, start) + pasted + current.slice(end));
  }, []);
  const onPaste = usePasteToAttach(attachments, insertText);
  const dropping = useFileDrop(formRef, attachments.add, attachments.enabled);
  const hold = uploadHold(attachments);
  const hasContent = !!text.trim() || attachments.ids.length > 0;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!hasContent || hold || send.isPending) return;
    send.mutate(
      { text, attachments: attachments.ids },
      {
        onSuccess: (result) => {
          setText("");
          attachments.clear();
          toast.show({ tone: "success", title: result.queued ? "Queued for the agent" : "Sent to the agent" });
        },
        onError: (error) => {
          const draft = error instanceof ApiError && error.code === "DRAFT";
          toast.show({ tone: "danger", title: draft ? "Someone is typing in that terminal" : "Not sent", description: error.message });
        },
      },
    );
  };
  return (
    <form ref={formRef} onSubmit={submit} aria-label="Message the agent" className="relative flex flex-col gap-1.5">
      <DropOverlay active={dropping} />
      <AttachmentTray attachments={attachments} />
      <div className="flex items-end gap-2">
        <label className="min-w-0 flex-1">
          <span className="sr-only">Message to {job.agent_name || "the agent"}</span>
          <textarea
            ref={boxRef}
            value={text}
            onChange={(event) => setText(event.target.value)}
            onPaste={onPaste}
            rows={1}
            placeholder={`Message ${job.agent_name || "the agent"}…`}
            className="min-h-11 w-full resize-y rounded border border-border-strong bg-surface px-2 py-2 text-sm text-heading placeholder:text-muted md:min-h-8"
          />
        </label>
        <AttachButtons attachments={attachments} />
        <Button type="submit" icon={Send} disabled={!hasContent || !!hold || send.isPending}>
          Send
        </Button>
      </div>
      {hold && <p className="text-2xs font-medium text-warning">{hold}</p>}
    </form>
  );
}

const roleTitles: Record<string, string> = {
  result: "Attached to the result",
  brief: "Given with the brief",
  message: "Sent in messages",
  answer: "Sent with answers",
  prompt: "Sent",
};
const roleOrder = ["result", "brief", "message", "answer", "prompt"];

/** The job's files, grouped by what they were for. Nothing when there are none. */
export function JobFiles({ jobId }: { jobId: string }) {
  const files = useOwnerAttachments(`job:${jobId}`);
  const list = files.data ?? [];
  if (list.length === 0) return null;
  const groups = new Map<string, Attachment[]>();
  for (const attachment of list) groups.set(attachment.role ?? "", [...(groups.get(attachment.role ?? "") ?? []), attachment]);
  const roles = [...groups.keys()].sort((a, b) => roleOrder.indexOf(a) - roleOrder.indexOf(b));
  return (
    <Card title="Files">
      <div className="flex flex-col gap-2">
        {roles.map((role) => (
          <section key={role} className="flex flex-col gap-1">
            <h3 className="text-2xs font-medium text-muted">{roleTitles[role] ?? role}</h3>
            <AttachmentList files={(groups.get(role) ?? []).map(shownFromAttachment)} label={roleTitles[role] ?? "Files"} />
          </section>
        ))}
      </div>
    </Card>
  );
}
