// ScheduleForm: a schedule made in the app (DESIGN.md 21.4), new or edited. The cadence is picked
// in words (every N minutes, hours or days, daily at a time, with every poke, on a change) and
// mapped to the task-file grammar (lib/schedule.ts cadenceToEvery); the target is the
// orchestrator, a project's lead (both as a poke) or a job of its own (the body is its brief, with
// a project or a directory, a role and an engine). The body is markdown, with a preview. A name
// cannot change after it is made (the server refuses a rename).
import { EngineNote } from "../jobs/EngineNote";
import { useId, useState, type FormEvent } from "react";
import type { ProjectView } from "../../lib/projects";
import { useDirs, useRoles } from "../../lib/queries";
import {
  cadenceLabels,
  cadenceProblem,
  cadenceToEvery,
  everyToCadence,
  MIN_MINUTES,
  nameProblem,
  targetOf,
  targetToInput,
  type Cadence,
  type CadenceKind,
  type ScheduleInput,
  type ScheduleTarget,
  type ScheduleTask,
} from "../../lib/schedule";
import { Markdown } from "../Markdown";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { TabPanel, Tabs } from "../ui/Tabs";

const inputClass =
  "min-h-11 w-full rounded border border-border-strong bg-surface px-2 text-sm text-heading placeholder:text-muted focus-visible:outline-2 focus-visible:outline-accent md:min-h-8";
const labelClass = "flex flex-col gap-1 text-2xs font-medium text-muted";

const targetLabels: Record<ScheduleTarget, string> = {
  orchestrator: "The orchestrator",
  lead: "A project's lead",
  job: "A job",
};

interface ScheduleFormProps {
  /** The app schedule being edited; a new one when absent. */
  task?: ScheduleTask;
  projects: ProjectView[];
  /** A new schedule starts on this project's lead. */
  defaultProjectId?: number;
  pending: boolean;
  error?: string | null;
  onSubmit: (input: ScheduleInput, enabled: boolean) => void;
  onCancel: () => void;
}

/** A sensible starting count when the unit changes. */
const defaultCounts: Partial<Record<CadenceKind, Cadence>> = {
  minutes: { kind: "minutes", count: 30 },
  hours: { kind: "hours", count: 1 },
  days: { kind: "days", count: 1 },
  daily: { kind: "daily", at: "17:00" },
  poke: { kind: "poke" },
  change: { kind: "change" },
};

export function ScheduleForm({ task, projects, defaultProjectId, pending, error, onSubmit, onCancel }: ScheduleFormProps) {
  const ids = useId();
  const editing = !!task;
  const [name, setName] = useState(task?.name ?? "");
  const [cadence, setCadence] = useState<Cadence>(task ? everyToCadence(task.every, task.after) : { kind: "daily", at: "17:00" });
  const [target, setTarget] = useState<ScheduleTarget>(task ? targetOf(task) : defaultProjectId ? "lead" : "orchestrator");
  const [projectId, setProjectId] = useState<number>(task?.project_id ?? defaultProjectId ?? projects[0]?.id ?? 0);
  const [body, setBody] = useState(task?.body_md ?? "");
  const [dir, setDir] = useState(task?.dir ?? "");
  const [role, setRole] = useState(task?.role ?? "");
  const [engine, setEngine] = useState(task?.engine ?? "");
  const [enabled, setEnabled] = useState(task?.enabled ?? true);
  const [bodyTab, setBodyTab] = useState<"write" | "preview">("write");
  /** Problems show once a save was tried, not while the form is still being filled in. */
  const [tried, setTried] = useState(false);
  const roles = useRoles();
  const dirs = useDirs();

  const problems = [editing ? null : nameProblem(name), cadenceProblem(cadence), !body.trim() ? "Say what it should do." : null, target === "lead" && !projectId ? "Pick the project." : null].filter(
    (problem): problem is string => !!problem,
  );

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (pending) return;
    if (problems.length > 0) {
      setTried(true);
      return;
    }
    const { every, after } = cadenceToEvery(cadence);
    const job = target === "job";
    onSubmit(
      {
        name: name.trim(),
        every,
        after,
        ...targetToInput(target, projectId),
        body_md: body,
        dir: job ? dir.trim() : "",
        role: job ? role : "",
        engine: job ? engine : "",
      },
      enabled,
    );
  };

  const setKind = (kind: CadenceKind) => setCadence(defaultCounts[kind] ?? { kind: "poke" });

  return (
    <form onSubmit={submit} aria-label={editing ? `Edit ${task.name}` : "New schedule"} className="flex flex-col gap-3 p-4">
      <label className={labelClass}>
        Name
        {editing ? (
          <span className="flex min-h-11 items-center font-mono text-sm text-heading md:min-h-8">{task.name}</span>
        ) : (
          <input value={name} onChange={(event) => setName(event.target.value.toLowerCase())} placeholder="evening-review" required autoFocus className={`${inputClass} font-mono`} />
        )}
      </label>

      <fieldset className="flex flex-col gap-1">
        <legend className="mb-1 text-2xs font-medium text-muted">When</legend>
        <div className="flex flex-wrap gap-2">
          <select aria-label="Cadence" value={cadence.kind} onChange={(event) => setKind(event.target.value as CadenceKind)} className={`${inputClass} w-auto flex-1`}>
            {(Object.keys(cadenceLabels) as CadenceKind[]).map((kind) => (
              <option key={kind} value={kind}>
                {cadenceLabels[kind]}
              </option>
            ))}
          </select>
          {(cadence.kind === "minutes" || cadence.kind === "hours" || cadence.kind === "days") && (
            <input
              type="number"
              aria-label={cadence.kind === "minutes" ? "Minutes" : cadence.kind === "hours" ? "Hours" : "Days"}
              min={cadence.kind === "minutes" ? MIN_MINUTES : 1}
              max={9999}
              value={cadence.count}
              onChange={(event) => setCadence({ kind: cadence.kind, count: Number(event.target.value) })}
              className={`${inputClass} w-24`}
            />
          )}
          {cadence.kind === "daily" && (
            <input type="time" aria-label="Time of day" value={cadence.at} onChange={(event) => setCadence({ kind: "daily", at: event.target.value })} className={`${inputClass} w-32`} />
          )}
        </div>
        <span className="text-2xs text-muted">
          {cadence.kind === "poke"
            ? "Rides along with every poke the agent gets."
            : cadence.kind === "change"
              ? "Runs when the fleet or a job changes."
              : `As a task file says it: every: ${cadenceToEvery(cadence).every}${cadenceToEvery(cadence).after ? `, after: ${cadenceToEvery(cadence).after}` : ""}.`}
        </span>
      </fieldset>

      <fieldset className="flex flex-col gap-1">
        <legend className="mb-1 text-2xs font-medium text-muted">Who</legend>
        <div role="radiogroup" aria-label="Target" className="flex flex-wrap gap-x-4">
          {(Object.keys(targetLabels) as ScheduleTarget[]).map((value) => (
            <label key={value} className="flex min-h-11 items-center gap-2 text-sm text-body md:min-h-7">
              <input type="radio" name={`${ids}-target`} value={value} checked={target === value} onChange={() => setTarget(value)} />
              {targetLabels[value]}
            </label>
          ))}
        </div>
        {target !== "orchestrator" && (
          <label className={labelClass}>
            Project
            <select aria-label="Project" value={projectId} onChange={(event) => setProjectId(Number(event.target.value))} className={inputClass}>
              {target === "job" && <option value={0}>None (use the directory)</option>}
              {projects.map((project) => (
                <option key={project.id} value={project.id}>
                  {project.name}
                </option>
              ))}
            </select>
          </label>
        )}
      </fieldset>

      {target === "job" && (
        <div className="grid gap-3 sm:grid-cols-3">
          <label className={labelClass}>
            Directory {projectId ? "(the project's when empty)" : ""}
            <input value={dir} onChange={(event) => setDir(event.target.value)} list={`${ids}-dirs`} placeholder={projects.find((p) => p.id === projectId)?.dir ?? "~/src/project"} className={`${inputClass} font-mono`} />
            <datalist id={`${ids}-dirs`}>
              {(dirs.data ?? []).map((entry) => (
                <option key={entry.path} value={entry.path} />
              ))}
            </datalist>
          </label>
          <label className={labelClass}>
            Role
            <select value={role} onChange={(event) => setRole(event.target.value)} className={inputClass}>
              <option value="">Default</option>
              {(roles.data ?? []).map((info) => (
                <option key={info.name} value={info.name}>
                  {info.name}
                </option>
              ))}
            </select>
          </label>
          <label className={labelClass}>
            Engine
            <select value={engine} onChange={(event) => setEngine(event.target.value)} className={inputClass}>
              <option value="">Default</option>
              <option value="claude">Claude</option>
              <option value="codex">Codex</option>
            </select>
          </label>
          <EngineNote engine={engine} className="sm:col-span-3" />
        </div>
      )}

      <div className="flex flex-col gap-1">
        <div className="flex items-center justify-between gap-2">
          <span id={`${ids}-body`} className="text-2xs font-medium text-muted">
            {target === "job" ? "The job's brief (markdown)" : "What to tell the agent (markdown)"}
          </span>
          <Tabs id={`${ids}-bodytab`} label="Body" value={bodyTab} onChange={setBodyTab} items={[{ value: "write", label: "Write" }, { value: "preview", label: "Preview" }]} />
        </div>
        <TabPanel id={`${ids}-bodytab`} value={bodyTab}>
          {bodyTab === "write" ? (
            <textarea
              aria-labelledby={`${ids}-body`}
              value={body}
              onChange={(event) => setBody(event.target.value)}
              rows={6}
              placeholder={"# Evening review\n\nList what finished today and what is still open."}
              className={`${inputClass} resize-y py-2 font-mono text-xs`}
            />
          ) : body.trim() ? (
            <div className="rounded border border-border p-3">
              <Markdown className="text-sm">{body}</Markdown>
            </div>
          ) : (
            <p className="text-xs text-muted">Nothing written yet.</p>
          )}
        </TabPanel>
      </div>

      <label className="flex min-h-11 items-center gap-2 text-sm text-body md:min-h-7">
        <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} className="h-4 w-4 accent-[var(--color-accent)]" />
        Enabled
      </label>

      {(error || (tried && problems.length > 0)) && (
        <Banner tone={error ? "danger" : "info"} role={error ? "alert" : "status"} className="rounded">
          {error ?? problems.join(" ")}
        </Banner>
      )}
      <div className="flex justify-end gap-2">
        <Button variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" disabled={pending}>
          {pending ? "Saving…" : editing ? "Save" : "Create schedule"}
        </Button>
      </div>
    </form>
  );
}
