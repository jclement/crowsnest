// VoiceJob: give a job by voice from the Now view's job box (R4 Fable 3.5, Codex P3/V1). Hold the
// mic (or tap to lock it on, tap again to stop), speak, see the transcript; crowsnest works out the
// project ("Website: fix the checkout" names it; naming two, or none with none chosen, asks), reads
// back "Website: fix the checkout. Create the job?" on screen (and aloud with speak mode on), and
// creates nothing until Create is pressed. The job is then durable at once (a job id, on the board,
// supervised), with Undo for a few seconds, which cancels it. Nothing is auto-sent: a misheard
// repository or a dropped "not" is caught at the readback, not after an agent started on it.
import { Link } from "@tanstack/react-router";
import { Check, Loader2, Mic, Square, Undo2, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { cn } from "../../lib/cn";
import type { ProjectView } from "../../lib/projects";
import { useCreateJob, useJobAction, useMe } from "../../lib/queries";
import { playEarcon, speakLocally, stopLocalSpeech } from "../../lib/voice/earcons";
import { interpretJob, readback, type JobIntent } from "../../lib/voice/jobIntent";
import { usePushToTalk } from "../../lib/voice/recorder";
import { speakModeOn } from "../../lib/voice/speak";
import { insecureNotice, micSupport } from "../../lib/voice/support";
import { useToast } from "../ui/toast-context";

/** How long Undo stays offered after a spoken job is created. */
const UNDO_MS = 8_000;

interface VoiceJobProps {
  projects: ProjectView[];
  /** The box's current choice: a slug, "" for the orchestrator. */
  chosen: string;
}

interface Draft extends JobIntent {
  heard: string;
}

export function VoiceJob({ projects, chosen }: VoiceJobProps) {
  const voice = useMe().data?.features.voice ?? false;
  const toast = useToast();
  const [draft, setDraft] = useState<Draft | null>(null);
  const [created, setCreated] = useState<{ id: string; where: string } | null>(null);
  const choices = projects.map((project) => ({ slug: project.slug, name: project.name }));

  const onText = useCallback(
    async (text: string) => {
      if (!text.trim()) return;
      const intent = interpretJob(text, choices, chosen);
      setDraft({ ...intent, heard: text });
      setCreated(null);
      playEarcon("heard");
      if (!intent.ambiguous && speakModeOn()) speakLocally(readback(intent.brief, nameOf(projects, intent.slug)));
    },
    // choices is derived from projects on every render; projects is the real dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [projects, chosen],
  );
  const ptt = usePushToTalk(onText, { review: false });
  const { state, dismiss } = ptt;
  const errorMessage = state.phase === "error" ? state.message : null;
  useEffect(() => {
    if (errorMessage === null) return;
    playEarcon("error");
    toast.show({ tone: "danger", title: "Could not hear that", description: errorMessage });
    dismiss();
  }, [errorMessage, dismiss, toast]);

  if (!voice) return null;
  const recording = state.phase === "recording" || state.phase === "starting";
  const transcribing = state.phase === "uploading";

  const press = () => {
    const support = micSupport();
    if (support !== "ok") {
      toast.show({ tone: "warning", title: "No microphone here", description: support === "insecure" ? insecureNotice : "This browser cannot record audio." });
      return;
    }
    stopLocalSpeech(); // pressing talk stops a readback: the cheap barge-in
    ptt.press("pointer");
  };

  return (
    <div className="flex flex-col gap-2 border-b border-border p-3" data-testid="voice-job">
      <button
        type="button"
        aria-pressed={recording}
        aria-label={recording ? "Listening: release (or tap) to finish" : "Give a job by voice (hold to talk)"}
        disabled={transcribing}
        onPointerDown={(event) => {
          event.preventDefault();
          press();
        }}
        onPointerUp={() => ptt.release()}
        onPointerCancel={() => ptt.release()}
        onKeyDown={(event) => {
          if ((event.key === "Enter" || event.key === " ") && !event.repeat) {
            event.preventDefault();
            if (recording) ptt.release();
            else {
              press();
              ptt.release(); // a key press is a tap: it locks recording on until the next one
            }
          }
        }}
        className={cn(
          "flex min-h-14 w-full touch-none select-none items-center justify-center gap-2 rounded-lg border text-sm font-medium transition-colors md:min-h-11",
          recording ? "border-danger bg-danger/10 text-danger" : "border-accent/60 bg-accent/5 text-accent hover:bg-accent/10",
        )}
      >
        {transcribing ? <Loader2 className="h-5 w-5 animate-spin" aria-hidden /> : recording ? <Square className="h-5 w-5" aria-hidden /> : <Mic className="h-5 w-5" aria-hidden />}
        {transcribing ? "Transcribing…" : recording ? "Listening… release to finish" : "Hold to give a job by voice"}
      </button>
      {recording && (
        <p role="status" className="flex items-center gap-1.5 text-xs text-danger">
          <span className="h-2 w-2 animate-pulse rounded-full bg-danger" aria-hidden />
          Recording. Say the project first: “Website: fix the checkout”.
        </p>
      )}
      {draft && (
        <Confirm
          draft={draft}
          projects={projects}
          onChange={setDraft}
          onCancel={() => {
            stopLocalSpeech();
            setDraft(null);
          }}
          onCreated={(id, where) => {
            setDraft(null);
            setCreated({ id, where });
          }}
        />
      )}
      {created && <CreatedLine id={created.id} where={created.where} onGone={() => setCreated(null)} />}
    </div>
  );
}

function nameOf(projects: ProjectView[], slug: string | null): string | null {
  return slug ? (projects.find((project) => project.slug === slug)?.name ?? slug) : null;
}

function Confirm({ draft, projects, onChange, onCancel, onCreated }: { draft: Draft; projects: ProjectView[]; onChange: (draft: Draft) => void; onCancel: () => void; onCreated: (id: string, where: string) => void }) {
  const create = useCreateJob();
  const toast = useToast();
  const project = draft.slug ? projects.find((candidate) => candidate.slug === draft.slug) : undefined;
  const where = project?.name ?? "the orchestrator";
  const pick = (slug: string | null) => onChange({ ...draft, slug, ambiguous: false });
  const shown = draft.ambiguous && draft.candidates.length > 0 ? projects.filter((candidate) => draft.candidates.includes(candidate.slug)) : projects;

  const submit = () => {
    const brief = draft.brief.trim();
    if (!brief || draft.ambiguous) return;
    create.mutateAsync(project ? { brief, project_id: project.id, to: "lead" } : { brief }).then(
      (job) => {
        playEarcon("sent");
        onCreated(job.id, where);
      },
      (failure: unknown) => {
        playEarcon("error");
        toast.show({ tone: "danger", title: "The job was not created", description: failure instanceof Error ? failure.message : String(failure) });
      },
    );
  };

  return (
    <section aria-label="Confirm the spoken job" className="flex flex-col gap-2 rounded-md border border-accent/50 bg-surface-raised p-2.5">
      <label className="flex flex-col gap-0.5 text-2xs font-medium text-muted">
        Heard (edit if it is wrong)
        <textarea
          value={draft.brief}
          onChange={(event) => onChange({ ...draft, brief: event.target.value })}
          rows={2}
          className="w-full resize-y rounded border border-border-strong bg-surface px-2 py-1.5 text-base text-heading md:text-xs"
        />
      </label>
      {draft.ambiguous && (
        <p role="alert" className="text-xs font-medium text-warning">
          {draft.candidates.length > 1 ? "That named more than one project." : "Which project is this for?"} Pick one:
        </p>
      )}
      <div role="group" aria-label="Project" className="flex flex-wrap gap-1.5">
        {!draft.ambiguous || draft.candidates.length === 0 ? <ProjectChip label="The orchestrator" active={!draft.ambiguous && !draft.slug} onPress={() => pick(null)} /> : null}
        {shown.map((candidate) => (
          <ProjectChip key={candidate.slug} label={candidate.name} active={!draft.ambiguous && draft.slug === candidate.slug} onPress={() => pick(candidate.slug)} />
        ))}
      </div>
      {!draft.ambiguous && (
        <p className="text-sm font-medium text-heading" data-testid="readback">
          {readback(draft.brief, project?.name ?? null)}
        </p>
      )}
      <div className="grid grid-cols-2 gap-1.5">
        <button
          type="button"
          onClick={submit}
          disabled={draft.ambiguous || !draft.brief.trim() || create.isPending}
          className="flex min-h-12 items-center justify-center gap-1.5 rounded-md bg-accent text-sm font-medium text-accent-fg disabled:opacity-50 md:min-h-9"
        >
          <Check className="h-4 w-4" aria-hidden />
          Create the job
        </button>
        <button type="button" onClick={onCancel} className="flex min-h-12 items-center justify-center gap-1.5 rounded-md border border-border-strong text-sm text-heading md:min-h-9">
          <X className="h-4 w-4" aria-hidden />
          Cancel
        </button>
      </div>
    </section>
  );
}

function ProjectChip({ label, active, onPress }: { label: string; active: boolean; onPress: () => void }) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onPress}
      className={cn("min-h-11 rounded-full border px-3 text-xs md:min-h-7", active ? "border-accent bg-accent text-accent-fg" : "border-border-strong bg-surface text-heading hover:bg-hover")}
    >
      {label}
    </button>
  );
}

/** "Created j_42 for Website" with Undo for a few seconds (Undo cancels the job). */
function CreatedLine({ id, where, onGone }: { id: string; where: string; onGone: () => void }) {
  const cancel = useJobAction(id);
  const toast = useToast();
  const [left, setLeft] = useState(Math.round(UNDO_MS / 1000));
  const gone = useRef(onGone);
  useEffect(() => {
    gone.current = onGone;
  }, [onGone]);
  useEffect(() => {
    const timer = window.setInterval(() => setLeft((value) => value - 1), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const expired = left <= 0;
  return (
    <p role="status" className="flex flex-wrap items-center gap-2 text-xs text-success" data-testid="voice-job-created">
      <Check className="h-4 w-4" aria-hidden />
      Created{" "}
      <Link to="/jobs" search={{ job: id }} className="font-mono text-accent hover:underline">
        {id}
      </Link>{" "}
      for {where}.
      {!expired && (
        <button
          type="button"
          onClick={() =>
            cancel.mutateAsync("cancel").then(
              () => {
                toast.show({ tone: "info", title: `Undone: ${id} cancelled` });
                gone.current();
              },
              (failure: unknown) => toast.show({ tone: "danger", title: "Not undone", description: failure instanceof Error ? failure.message : String(failure) }),
            )
          }
          className="inline-flex min-h-11 items-center gap-1 rounded border border-border-strong px-2 text-heading md:min-h-7"
        >
          <Undo2 className="h-3.5 w-3.5" aria-hidden />
          Undo ({left} s)
        </button>
      )}
    </p>
  );
}
