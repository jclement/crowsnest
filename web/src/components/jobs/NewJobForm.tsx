// NewJobForm: the one text box (DESIGN.md 6.2 and 12), and the one form a job is given with: Now's
// job box, and the Jobs view's New job sheet. "What needs doing?" is the brief; where,
// role, done-when and engine sit behind one disclosure. With no directory the job goes to the
// orchestrator. Cmd/Ctrl+Enter gives the job from the keyboard. Files (DESIGN.md 8.6) are pasted,
// dropped on the form or picked with the paperclip; the agent finds them listed in its brief.
//
// With a project chosen in the Jobs view's switcher (DESIGN.md 21.1) the job is that project's:
// "To the lead" (no directory; the project's lead holds it, as the orchestrator holds unfiled
// ones) or "As a job" (a job agent, the directory and defaults prefilled from the project; the
// server fills anything left unset from the project's defaults too).
import { EngineNote } from "./EngineNote";
import { ChevronDown, ChevronRight, FolderSearch, Send } from "lucide-react";
import { useCallback, useEffect, useId, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { onJobDraft, takeJobDraft } from "../../lib/jobDraft";
import { useCreateJob, useDirs, useRoles } from "../../lib/queries";
import type { Job } from "../../lib/jobs";
import { leadRunning, type ProjectView } from "../../lib/projects";
import { AttachButtons, AttachmentTray, DropOverlay } from "../attach/AttachmentTray";
import { DirPicker } from "../browse/DirPicker";
import { uploadHold, useAttachments, useFileDrop, usePasteToAttach } from "../attach/useAttachments";
import { Button } from "../ui/Button";
import { useToast } from "../ui/toast-context";

interface NewJobFormProps {
  /** Called with the job once it is created (the view selects it). */
  onCreated?: (job: Job) => void;
  /** The project the board shows: the job is given to it. */
  project?: ProjectView;
  /** Focus the brief on mount (the Jobs view's New job sheet). */
  autoFocus?: boolean;
}

type Destination = "lead" | "job";

const inputClass =
  "min-h-11 w-full rounded border border-border-strong bg-surface px-2 text-sm text-heading placeholder:text-muted focus-visible:outline-2 focus-visible:outline-accent md:min-h-8";

export function NewJobForm({ onCreated, project, autoFocus }: NewJobFormProps) {
  const [brief, setBrief] = useState("");
  const [open, setOpen] = useState(false);
  const [dir, setDir] = useState(project?.dir ?? "");
  const [role, setRole] = useState(project?.default_role ?? "");
  const [doneWhen, setDoneWhen] = useState("");
  const [engine, setEngine] = useState(project?.default_engine || "claude");
  const [destination, setDestination] = useState<Destination>("lead");
  // A different project prefills its own directory and defaults (the brief typed so far stays).
  const projectId = project?.id;
  const [prefilledFor, setPrefilledFor] = useState(projectId);
  if (prefilledFor !== projectId) {
    setPrefilledFor(projectId);
    setDir(project?.dir ?? "");
    setRole(project?.default_role ?? "");
    setEngine(project?.default_engine || "claude");
    setDestination("lead");
  }
  const create = useCreateJob();
  const toast = useToast();
  const ids = useId();
  const briefRef = useRef<HTMLTextAreaElement>(null);
  const formRef = useRef<HTMLFormElement>(null);
  const attachments = useAttachments();
  const insertText = useCallback((text: string) => {
    const box = briefRef.current;
    const start = box?.selectionStart ?? Number.MAX_SAFE_INTEGER;
    const end = box?.selectionEnd ?? start;
    setBrief((current) => current.slice(0, start) + text + current.slice(end));
  }, []);
  const onPaste = usePasteToAttach(attachments, insertText);
  const dropping = useFileDrop(formRef, attachments.add, attachments.enabled);
  const hold = uploadHold(attachments);

  // A draft from the command palette ("Open the full form"): fill the box, open the options, focus.
  useEffect(() => {
    const take = () => {
      const draft = takeJobDraft();
      if (!draft) return;
      setBrief(draft.brief);
      setOpen(true);
      requestAnimationFrame(() => {
        const box = briefRef.current;
        if (!box) return;
        box.focus();
        box.setSelectionRange(box.value.length, box.value.length);
      });
    };
    take();
    return onJobDraft(take);
  }, []);

  const submit = (event?: FormEvent) => {
    event?.preventDefault();
    if (!brief.trim() || hold || create.isPending) return;
    const files = attachments.ids.length > 0 ? { attachments: attachments.ids } : {};
    const request = !project
      ? { brief, dir: dir.trim() || undefined, role: role || undefined, done_when: doneWhen.trim() || undefined, engine, ...files }
      : destination === "lead"
        ? { brief, project_id: project.id, to: "lead" as const, done_when: doneWhen.trim() || undefined, ...files }
        : { brief, project_id: project.id, to: "job" as const, dir: dir.trim() || undefined, role: role || undefined, done_when: doneWhen.trim() || undefined, engine, ...files };
    create.mutate(request, {
        onSuccess: (job) => {
          setBrief("");
          setDoneWhen("");
          attachments.clear();
          const where = project && destination === "lead" ? `Given to ${project.name}'s lead` : job.dir ? `Starting an agent in ${job.dir}` : "Sent to the orchestrator";
          toast.show({ tone: "success", title: `Job ${job.id} given`, description: where });
          onCreated?.(job);
        },
        onError: (error) => toast.show({ tone: "danger", title: "The job was not created", description: error.message }),
    });
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) submit();
  };

  return (
    <form ref={formRef} onSubmit={submit} aria-label="New job" className="relative flex flex-col gap-2 border-b border-border p-3">
      <DropOverlay active={dropping} label="Drop files to give with the job" />
      <label htmlFor={`${ids}-brief`} className="sr-only">
        What needs doing?
      </label>
      <textarea
        ref={briefRef}
        id={`${ids}-brief`}
        value={brief}
        onChange={(event) => setBrief(event.target.value)}
        onKeyDown={onKeyDown}
        onPaste={onPaste}
        rows={brief.includes("\n") ? 4 : 2}
        placeholder="What needs doing?"
        autoFocus={autoFocus}
        className={`${inputClass} resize-y py-2`}
      />
      <AttachmentTray attachments={attachments} />
      {project && <DestinationToggle project={project} value={destination} onChange={setDestination} />}
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          aria-expanded={open}
          aria-controls={`${ids}-options`}
          onClick={() => setOpen(!open)}
          className="inline-flex min-h-11 items-center gap-1 rounded px-1 text-xs text-muted hover:text-heading md:min-h-7"
        >
          {open ? <ChevronDown className="h-3.5 w-3.5" aria-hidden /> : <ChevronRight className="h-3.5 w-3.5" aria-hidden />}
          {project && destination === "lead" ? `To ${project.name}'s lead${doneWhen.trim() ? " · done when…" : ""}` : optionsSummary(dir, role, engine)}
        </button>
        <span className="ml-auto flex items-center gap-2">
          {hold && <span className="text-2xs font-medium text-warning">{hold}</span>}
          <AttachButtons attachments={attachments} />
          <Button type="submit" variant="primary" icon={Send} disabled={!brief.trim() || !!hold || create.isPending}>
            {create.isPending ? "Giving…" : "Give job"}
          </Button>
        </span>
      </div>
      {open &&
        (project && destination === "lead" ? (
          <LeadOptions ids={ids} doneWhen={doneWhen} setDoneWhen={setDoneWhen} />
        ) : (
          <Options ids={ids} dir={dir} setDir={setDir} role={role} setRole={setRole} doneWhen={doneWhen} setDoneWhen={setDoneWhen} engine={engine} setEngine={setEngine} project={project} />
        ))}
    </form>
  );
}

/** "To the lead" or "As a job", for a project's job; with a word on whether the lead will start. */
function DestinationToggle({ project, value, onChange }: { project: ProjectView; value: Destination; onChange: (value: Destination) => void }) {
  const leadOff = !leadRunning(project.lead);
  return (
    <div className="flex flex-wrap items-center gap-2">
      <div role="radiogroup" aria-label={`Give it to ${project.name}`} className="inline-flex rounded-md border border-border bg-surface-sunken p-0.5">
        {(["lead", "job"] as const).map((option) => (
          <button
            key={option}
            type="button"
            role="radio"
            aria-checked={value === option}
            onClick={() => onChange(option)}
            className={`inline-flex min-h-11 items-center rounded px-3 text-xs font-medium md:min-h-7 ${value === option ? "bg-surface text-heading shadow-sm ring-1 ring-border" : "text-muted hover:text-heading"}`}
          >
            {option === "lead" ? "To the lead" : "As a job"}
          </button>
        ))}
      </div>
      <span className="text-2xs text-muted">
        {value === "lead"
          ? leadOff
            ? project.lead_start === "manual"
              ? "The lead is off; it holds the job once you start it."
              : "The lead is off; it starts for this job."
            : `${project.name}'s lead plans it and hands work out.`
          : `A job agent in ${project.dir}.`}
      </span>
    </div>
  );
}

/** A lead's job takes only a done-when: the lead decides where and how. */
function LeadOptions({ ids, doneWhen, setDoneWhen }: { ids: string; doneWhen: string; setDoneWhen: (value: string) => void }) {
  return (
    <div id={`${ids}-options`} className="grid gap-2">
      <label className="flex flex-col gap-1 text-2xs font-medium text-muted">
        Done when
        <input value={doneWhen} onChange={(event) => setDoneWhen(event.target.value)} placeholder="tests pass and the README says how" className={inputClass} />
      </label>
    </div>
  );
}

/** One line saying where the job will go, shown on the disclosure. */
function optionsSummary(dir: string, role: string, engine: string): string {
  const where = dir.trim() ? dir.trim().split("/").filter(Boolean).pop() : "the orchestrator";
  const parts = [`To ${where}`];
  if (role) parts.push(role);
  if (engine !== "claude") parts.push(engine);
  return parts.join(" · ");
}

interface OptionsProps {
  ids: string;
  dir: string;
  setDir: (value: string) => void;
  role: string;
  setRole: (value: string) => void;
  doneWhen: string;
  setDoneWhen: (value: string) => void;
  engine: string;
  setEngine: (value: string) => void;
  project?: ProjectView;
}

function Options({ ids, dir, setDir, role, setRole, doneWhen, setDoneWhen, engine, setEngine, project }: OptionsProps) {
  const dirs = useDirs();
  const roles = useRoles();
  const [browsing, setBrowsing] = useState(false);
  const labelClass = "flex flex-col gap-1 text-2xs font-medium text-muted";
  return (
    <div id={`${ids}-options`} className="grid gap-2 sm:grid-cols-2">
      <div className="flex flex-col gap-1">
        <label htmlFor={`${ids}-where`} className={labelClass}>
          {project ? `Where (empty for ${project.name}'s directory)` : "Where (a directory or repo; empty for the orchestrator)"}
        </label>
        <div className="flex gap-1">
          <input id={`${ids}-where`} value={dir} onChange={(event) => setDir(event.target.value)} list={`${ids}-dirs`} placeholder="~/src/project" className={`${inputClass} font-mono`} />
          <Button icon={FolderSearch} onClick={() => setBrowsing(true)} title="Browse for a folder" aria-label="Browse for a folder" className="shrink-0">
            <span className="hidden sm:inline">Browse…</span>
          </Button>
        </div>
        <datalist id={`${ids}-dirs`}>
          {(dirs.data ?? []).map((entry) => (
            <option key={entry.path} value={entry.path}>
              {entry.label}
            </option>
          ))}
        </datalist>
        <DirPicker open={browsing} onClose={() => setBrowsing(false)} initialPath={dir || project?.dir} title="Where should the job run?" purpose="job" onSelect={(path) => setDir(path)} />
      </div>
      <label className={labelClass}>
        Role
        <select value={role} onChange={(event) => setRole(event.target.value)} className={inputClass}>
          <option value="">Default ({project?.default_role || (dir.trim() || project ? "builder" : "lead")})</option>
          {(roles.data ?? []).map((info) => (
            <option key={info.name} value={info.name} title={info.summary}>
              {info.name}
            </option>
          ))}
        </select>
      </label>
      <label className={labelClass}>
        Done when
        <input value={doneWhen} onChange={(event) => setDoneWhen(event.target.value)} placeholder="tests pass and the README says how" className={inputClass} />
      </label>
      <label className={labelClass}>
        Engine
        <select value={engine} onChange={(event) => setEngine(event.target.value)} className={inputClass}>
          <option value="claude">Claude</option>
          <option value="codex">Codex</option>
        </select>
      </label>
      <EngineNote engine={engine} className="sm:col-span-2" />
    </div>
  );
}
