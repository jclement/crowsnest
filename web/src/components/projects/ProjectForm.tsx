// ProjectForm: a project's fields, for "New project" and "Edit project" (DESIGN.md 21.1). The
// directory comes from the folder picker (Browse…, DESIGN.md 4.23), the remembered ones (GET
// /dirs, tappable on a phone, where a datalist is poor) or free text; on a new project the name
// follows the directory's basename until it is typed over, and the folder's facts (GET
// /browse/facts, for a picked or a typed path) prefill the check command and say what to know:
// its remote host (Land opens pull requests only on GitHub), not a repository, overlaps. The defaults its jobs get (role, engine, model, isolation, permission mode) and when
// its lead starts sit behind one disclosure. The server checks the rest (the directory exists, is
// not $HOME, does not overlap another project) and its message is shown as it comes.
import { Check, ChevronDown, ChevronRight, FolderOpen, FolderSearch } from "lucide-react";
import { useEffect, useId, useState, type FormEvent } from "react";
import { factsSummary, folderWarnings, hostNote, isPathInput, type FolderFacts } from "../../lib/browse";
import { leadStartLabels, permissionModes, suggestName, type AgentStartFinding, type LeadStart, type Project, type ProjectInput } from "../../lib/projects";
import { useDirs, useEngines, useFolderFacts, useRoles } from "../../lib/queries";
import { engineInfo, withoutSentence } from "../../lib/engines";
import { DirPicker } from "../browse/DirPicker";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { AgentStartList } from "./AgentStartList";

export const inputClass =
  "min-h-11 w-full rounded border border-border-strong bg-surface px-2 text-sm text-heading placeholder:text-muted focus-visible:outline-2 focus-visible:outline-accent md:min-h-8";
const labelClass = "flex flex-col gap-1 text-2xs font-medium text-muted";

/** How many remembered directories are offered as buttons. */
const DIR_CHOICES = 6;

/** How long a typed directory must rest before its facts are fetched. */
const TYPED_FACTS_DELAY_MS = 400;

/** What a new project carries besides its fields: the check command, saved after creating it. */
export interface ProjectExtras {
  /** The check command as typed ("" automatic). */
  checkCommand: string;
  /** What crowsnest found by itself; saving the same again is not needed. */
  detectedCheck: string;
  /** "Agents may work in this folder without asking": record the agents' folder trust (8.2.1). */
  trustFolder: boolean;
}

interface ProjectFormProps {
  /** The project being edited; a new project when absent. */
  project?: Project;
  submitLabel: string;
  pending: boolean;
  /** The server's refusal, shown above the buttons. */
  error?: string | null;
  onSubmit: (input: ProjectInput, extras: ProjectExtras) => void;
  onCancel: () => void;
  /** Open the folder picker at once (the palette's "New project in…"). */
  startBrowsing?: boolean;
}

function isolateValue(value: boolean | null | undefined): "-1" | "0" | "1" {
  return value === true ? "1" : value === false ? "0" : "-1";
}

export function ProjectForm({ project, submitLabel, pending, error, onSubmit, onCancel, startBrowsing }: ProjectFormProps) {
  const ids = useId();
  const creating = !project;
  const [dir, setDir] = useState(project?.dir ?? "");
  const [name, setName] = useState(project?.name ?? "");
  const [nameTouched, setNameTouched] = useState(!creating);
  const [slug, setSlug] = useState("");
  const [description, setDescription] = useState(project?.description ?? "");
  const [role, setRole] = useState(project?.default_role ?? "");
  const [engine, setEngine] = useState(project?.default_engine ?? "");
  const [model, setModel] = useState(project?.default_model ?? "");
  const [isolate, setIsolate] = useState(isolateValue(project?.default_isolate));
  const [permission, setPermission] = useState(project?.permission_mode ?? "");
  const [leadStart, setLeadStart] = useState<LeadStart>(project?.lead_start ?? "open");
  const [open, setOpen] = useState(!creating);
  const [browsing, setBrowsing] = useState(!!startBrowsing && creating);
  const [checkCommand, setCheckCommand] = useState("");
  const [checkTouched, setCheckTouched] = useState(false);
  // R9-S4: ticked by default only for a folder that carries nothing an agent runs or reads at its
  // start; until the facts say so (or the human chooses), unticked.
  const [trustChoice, setTrustChoice] = useState<boolean | null>(null);
  const dirs = useDirs();
  const roles = useRoles();
  const codexInfo = engineInfo(useEngines().data, "codex");
  const typed = useSettled(creating && isPathInput(dir) ? dir.trim() : "", TYPED_FACTS_DELAY_MS);
  const factsQuery = useFolderFacts(typed);
  const facts = factsQuery.data && typed !== "" ? factsQuery.data : undefined;

  // New facts (a picked or typed folder) prefill the name and the check command, unless typed over.
  const [prefilledFrom, setPrefilledFrom] = useState("");
  if (facts && facts.path !== prefilledFrom) {
    setPrefilledFrom(facts.path);
    if (!nameTouched) setName(suggestName(facts.path));
    if (!checkTouched) setCheckCommand(facts.check_command ?? "");
    setTrustChoice(null);
  }
  const findings = facts?.agent_start;
  const trustFolder = trustChoice ?? (findings !== undefined && findings.length === 0);

  const pickDir = (value: string) => {
    setDir(value);
    if (!nameTouched) setName(suggestName(value));
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!dir.trim() || !name.trim() || pending) return;
    onSubmit({
      name: name.trim(),
      dir: dir.trim(),
      ...(creating && slug.trim() ? { slug: slug.trim() } : {}),
      description: description.trim(),
      default_role: role,
      default_engine: engine,
      default_model: model.trim(),
      default_isolate: Number(isolate) as -1 | 0 | 1,
      permission_mode: permission,
      lead_start: leadStart,
    }, { checkCommand: checkCommand.trim(), detectedCheck: facts?.check_command ?? "", trustFolder: creating && trustFolder });
  };

  const remembered = (dirs.data ?? []).slice(0, DIR_CHOICES);
  return (
    <form onSubmit={submit} aria-label={creating ? "New project" : "Edit project"} className="flex flex-col gap-3 p-4">
      <label className={labelClass}>
        Directory (where its jobs run)
        <input
          value={dir}
          onChange={(event) => pickDir(event.target.value)}
          list={`${ids}-dirs`}
          placeholder="~/src/project"
          required
          autoFocus={creating}
          className={`${inputClass} font-mono`}
        />
        <datalist id={`${ids}-dirs`}>
          {(dirs.data ?? []).map((entry) => (
            <option key={entry.path} value={entry.path}>
              {entry.label}
            </option>
          ))}
        </datalist>
      </label>
      {creating && (
        <div className="-mt-1 flex flex-col gap-1">
          <Button compact icon={FolderSearch} onClick={() => setBrowsing(true)} className="self-start">
            Browse…
          </Button>
          {facts && <FolderNote facts={facts} />}
        </div>
      )}
      {creating && remembered.length > 0 && (
        <div className="-mt-1 flex flex-wrap gap-1" aria-label="Remembered directories" role="group">
          {remembered.map((entry) => (
            <Button key={entry.path} compact variant="ghost" icon={FolderOpen} onClick={() => pickDir(entry.path)} title={entry.path} className="max-w-full">
              <span className="truncate font-mono">{entry.label || entry.path}</span>
            </Button>
          ))}
        </div>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <label className={labelClass}>
          Name
          <input
            value={name}
            onChange={(event) => {
              setName(event.target.value);
              setNameTouched(true);
            }}
            placeholder="From the directory"
            required
            className={inputClass}
          />
        </label>
        {creating ? (
          <label className={labelClass}>
            Short name (optional; for URLs and lead-&lt;name&gt;)
            <input value={slug} onChange={(event) => setSlug(event.target.value)} placeholder="from the name" className={`${inputClass} font-mono`} />
          </label>
        ) : (
          <div className={labelClass}>
            Short name
            <span className="flex min-h-11 items-center font-mono text-sm text-heading md:min-h-8">{project.slug}</span>
          </div>
        )}
      </div>
      {creating && (
        <label className={labelClass}>
          Check command (how crowsnest checks a job's finished work)
          <input
            value={checkCommand}
            onChange={(event) => {
              setCheckCommand(event.target.value);
              setCheckTouched(true);
            }}
            placeholder="Automatic: mise run check when there is a check task"
            className={`${inputClass} font-mono`}
          />
        </label>
      )}
      <label className={labelClass}>
        Description
        <textarea value={description} onChange={(event) => setDescription(event.target.value)} rows={2} placeholder="What it is, in a line" className={`${inputClass} resize-y py-2`} />
      </label>
      <button
        type="button"
        aria-expanded={open}
        aria-controls={`${ids}-defaults`}
        onClick={() => setOpen(!open)}
        className="inline-flex min-h-11 items-center gap-1 self-start rounded px-1 text-xs text-muted hover:text-heading md:min-h-7"
      >
        {open ? <ChevronDown className="h-3.5 w-3.5" aria-hidden /> : <ChevronRight className="h-3.5 w-3.5" aria-hidden />}
        Defaults for its jobs and lead
      </button>
      {open && (
        <div id={`${ids}-defaults`} className="grid gap-3 sm:grid-cols-2">
          <label className={labelClass}>
            Role
            <select value={role} onChange={(event) => setRole(event.target.value)} className={inputClass}>
              <option value="">Crowsnest's default</option>
              {(roles.data ?? []).map((info) => (
                <option key={info.name} value={info.name} title={info.summary}>
                  {info.name}
                </option>
              ))}
            </select>
          </label>
          <label className={labelClass}>
            Engine
            <select value={engine} onChange={(event) => setEngine(event.target.value)} className={inputClass}>
              <option value="">Default (Claude)</option>
              <option value="claude">Claude</option>
              <option value="codex">Codex{codexInfo && !codexInfo.installed ? " (not installed)" : ""}</option>
            </select>
            {engine === "codex" && codexInfo && <span className="font-normal text-warning">{withoutSentence(codexInfo)}</span>}
          </label>
          <label className={labelClass}>
            Model
            <input value={model} onChange={(event) => setModel(event.target.value)} placeholder="Claude's default" className={`${inputClass} font-mono`} />
          </label>
          <label className={labelClass}>
            Own worktree
            <select value={isolate} onChange={(event) => setIsolate(event.target.value as "-1" | "0" | "1")} className={inputClass}>
              <option value="-1">The role's default</option>
              <option value="1">Yes, a worktree per job</option>
              <option value="0">No, work in the directory</option>
            </select>
          </label>
          <label className={labelClass}>
            Permission mode
            <select value={permission} onChange={(event) => setPermission(event.target.value)} className={inputClass}>
              {permissionModes.map((mode) => (
                <option key={mode} value={mode}>
                  {mode || "Claude's default"}
                </option>
              ))}
            </select>
          </label>
          <label className={labelClass}>
            Start the lead
            <select value={leadStart} onChange={(event) => setLeadStart(event.target.value as LeadStart)} className={inputClass}>
              {(Object.keys(leadStartLabels) as LeadStart[]).map((value) => (
                <option key={value} value={value}>
                  {leadStartLabels[value]}
                </option>
              ))}
            </select>
          </label>
        </div>
      )}
      {creating && <TrustFolderBox checked={trustFolder} onChange={setTrustChoice} findings={findings} />}
      {error && (
        <Banner tone="danger" role="alert" className="rounded">
          {error}
        </Banner>
      )}
      {creating && (
        <DirPicker
          open={browsing}
          onClose={() => setBrowsing(false)}
          initialPath={dir}
          title="Choose the project's folder"
          purpose="project"
          onSelect={(path) => pickDir(path)}
        />
      )}
      <div className="flex justify-end gap-2">
        <Button variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" disabled={!dir.trim() || !name.trim() || pending}>
          {pending ? "Saving…" : submitLabel}
        </Button>
      </div>
    </form>
  );
}

/**
 * "Agents may work in this folder without asking" (DESIGN.md 8.2.1). You chose the folder, so the
 * agents' own "do you trust this folder?" would only ask you again, from a terminal; ticked,
 * crowsnest records that answer for Claude Code (and Codex) when it creates the project, and every
 * job worktree made from it inherits it. Trusting also lets whatever the folder carries run the
 * moment an agent starts (R9-S4): the box lists it, and is unticked by default when there is any.
 */
function TrustFolderBox({ checked, onChange, findings }: { checked: boolean; onChange: (value: boolean) => void; findings?: AgentStartFinding[] }) {
  // The whole row is the tap target (a phone's 44 px rule): the real checkbox covers it, unseen,
  // and the drawn box shows its state. The findings sit outside the label, so reading them is not
  // a tap on the box.
  return (
    <div className="flex flex-col gap-1.5 rounded border border-border bg-surface-sunken p-2.5 text-xs">
      <label className="relative flex min-h-11 cursor-pointer items-start gap-2 has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-accent">
        <input type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} className="absolute inset-0 h-full w-full cursor-pointer opacity-0" />
        <span aria-hidden className={`mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded border ${checked ? "border-accent bg-accent text-accent-fg" : "border-border-strong bg-surface"}`}>
          {checked && <Check className="h-3 w-3" />}
        </span>
        <span className="flex flex-col gap-0.5">
          <span className="font-medium text-heading">Agents may work in this folder without asking</span>
          <span className="text-2xs text-muted">
            Claude Code and Codex each ask once whether to trust a new folder, in their terminal. Trusting lets them read,
            edit and run files here, and lets whatever the folder carries for agents run as soon as one starts. You chose
            this folder, so crowsnest can record that yes for you; job worktrees made from it start trusted too. Untick to
            answer the question yourself on the agent&apos;s card.
          </span>
        </span>
      </label>
      {findings === undefined ? (
        <span className="text-2xs text-muted">Pick the folder to see what in it runs when an agent starts.</span>
      ) : (
        <AgentStartList findings={findings} />
      )}
    </div>
  );
}

/** The facts about the chosen folder, in a line, with the remote's note and any warnings. */
function FolderNote({ facts }: { facts: FolderFacts }) {
  const note = hostNote(facts);
  return (
    <div aria-live="polite" className="flex flex-col gap-0.5 text-2xs">
      <span className="text-body">{factsSummary(facts)}</span>
      {note && <span className="text-muted">{note}</span>}
      {folderWarnings(facts, "project").map((warning) => (
        <span key={warning.text} className={warning.tone === "danger" ? "text-danger" : warning.tone === "warning" ? "text-warning" : "text-muted"}>
          {warning.text}
        </span>
      ))}
    </div>
  );
}

/** value, once it has stopped changing for delay ms. */
function useSettled(value: string, delay: number): string {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return settled;
}
