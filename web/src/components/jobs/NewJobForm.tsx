// NewJobForm: the one text box (DESIGN.md 6.2 and 12). "What needs doing?" is the brief; where,
// role, done-when and engine sit behind one disclosure. With no directory the job goes to the
// orchestrator. Cmd/Ctrl+Enter gives the job from the keyboard.
import { ChevronDown, ChevronRight, Send } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { onJobDraft, takeJobDraft } from "../../lib/jobDraft";
import { useCreateJob, useDirs, useRoles } from "../../lib/queries";
import type { Job } from "../../lib/jobs";
import { Button } from "../ui/Button";
import { useToast } from "../ui/toast-context";

interface NewJobFormProps {
  /** Called with the job once it is created (the view selects it). */
  onCreated?: (job: Job) => void;
}

const inputClass =
  "min-h-11 w-full rounded border border-border-strong bg-surface px-2 text-sm text-heading placeholder:text-muted focus-visible:outline-2 focus-visible:outline-accent md:min-h-8";

export function NewJobForm({ onCreated }: NewJobFormProps) {
  const [brief, setBrief] = useState("");
  const [open, setOpen] = useState(false);
  const [dir, setDir] = useState("");
  const [role, setRole] = useState("");
  const [doneWhen, setDoneWhen] = useState("");
  const [engine, setEngine] = useState("claude");
  const create = useCreateJob();
  const toast = useToast();
  const ids = useId();
  const briefRef = useRef<HTMLTextAreaElement>(null);

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
    if (!brief.trim() || create.isPending) return;
    create.mutate(
      { brief, dir: dir.trim() || undefined, role: role || undefined, done_when: doneWhen.trim() || undefined, engine },
      {
        onSuccess: (job) => {
          setBrief("");
          setDoneWhen("");
          toast.show({ tone: "success", title: `Job ${job.id} given`, description: job.dir ? `Starting an agent in ${job.dir}` : "Sent to the orchestrator" });
          onCreated?.(job);
        },
        onError: (error) => toast.show({ tone: "danger", title: "The job was not created", description: error.message }),
      },
    );
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) submit();
  };

  return (
    <form onSubmit={submit} aria-label="New job" className="flex flex-col gap-2 border-b border-border p-3">
      <label htmlFor={`${ids}-brief`} className="sr-only">
        What needs doing?
      </label>
      <textarea
        ref={briefRef}
        id={`${ids}-brief`}
        value={brief}
        onChange={(event) => setBrief(event.target.value)}
        onKeyDown={onKeyDown}
        rows={brief.includes("\n") ? 4 : 2}
        placeholder="What needs doing?"
        className={`${inputClass} resize-y py-2`}
      />
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          aria-expanded={open}
          aria-controls={`${ids}-options`}
          onClick={() => setOpen(!open)}
          className="inline-flex min-h-11 items-center gap-1 rounded px-1 text-xs text-muted hover:text-heading md:min-h-7"
        >
          {open ? <ChevronDown className="h-3.5 w-3.5" aria-hidden /> : <ChevronRight className="h-3.5 w-3.5" aria-hidden />}
          {optionsSummary(dir, role, engine)}
        </button>
        <Button type="submit" variant="primary" icon={Send} className="ml-auto" disabled={!brief.trim() || create.isPending}>
          {create.isPending ? "Giving…" : "Give job"}
        </Button>
      </div>
      {open && <Options ids={ids} dir={dir} setDir={setDir} role={role} setRole={setRole} doneWhen={doneWhen} setDoneWhen={setDoneWhen} engine={engine} setEngine={setEngine} />}
    </form>
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
}

function Options({ ids, dir, setDir, role, setRole, doneWhen, setDoneWhen, engine, setEngine }: OptionsProps) {
  const dirs = useDirs();
  const roles = useRoles();
  const labelClass = "flex flex-col gap-1 text-2xs font-medium text-muted";
  return (
    <div id={`${ids}-options`} className="grid gap-2 sm:grid-cols-2">
      <label className={labelClass}>
        Where (a directory or repo; empty for the orchestrator)
        <input value={dir} onChange={(event) => setDir(event.target.value)} list={`${ids}-dirs`} placeholder="~/src/project" className={`${inputClass} font-mono`} />
        <datalist id={`${ids}-dirs`}>
          {(dirs.data ?? []).map((entry) => (
            <option key={entry.path} value={entry.path}>
              {entry.label}
            </option>
          ))}
        </datalist>
      </label>
      <label className={labelClass}>
        Role
        <select value={role} onChange={(event) => setRole(event.target.value)} className={inputClass}>
          <option value="">Default ({dir.trim() ? "builder" : "lead"})</option>
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
    </div>
  );
}
