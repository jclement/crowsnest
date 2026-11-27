// LayerEditor: one instruction layer (DESIGN.md 21.3), edited in place: the markdown in a
// monospace box, a preview through the one safe renderer (components/Markdown), for a role the
// diff of the text against the built-in (the Inbox's DiffView), and the kept revisions, each with
// a diff against the current text and Restore. A role whose text comes from a file is read-only
// here ("from file" and its path): the file is the explicit mechanism and wins. Saving empty text
// clears a global or project layer; a role goes back to the built-in with Reset instead.
import { History, RotateCcw, Save } from "lucide-react";
import { useState } from "react";
import { relativeTime } from "../../lib/format";
import { byteLength, formatBytes, sourceBadge, type Layer, type LayerDetail, type LayerRevision, type LayerScope } from "../../lib/instructions";
import { useInstructionEdit, useInstructionLayer } from "../../lib/queries";
import { DiffView } from "../inbox/DiffView";
import { Markdown } from "../Markdown";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Chip } from "../ui/Chip";
import { SkeletonRows } from "../ui/Skeleton";
import { TabPanel, Tabs } from "../ui/Tabs";
import { useToast } from "../ui/toast-context";

/** The server's cap when the listing has not said otherwise (instructions.MaxBodyBytes). */
const DEFAULT_MAX_BYTES = 32 * 1024;

interface LayerEditorProps {
  scope: LayerScope;
  /** The URL key: "-" for global, the role's name, the project's id. */
  apiKey: string;
  maxBytes?: number;
  /** Shown above the box: what this layer is for. */
  hint?: string;
}

export function LayerEditor({ scope, apiKey, maxBytes = DEFAULT_MAX_BYTES, hint }: LayerEditorProps) {
  const detail = useInstructionLayer(scope, apiKey);
  if (detail.isPending) return <SkeletonRows rows={4} label="Loading the instructions" />;
  if (detail.isError) {
    return (
      <Banner tone="danger" role="alert" className="rounded">
        Could not load these instructions: {detail.error.message}
      </Banner>
    );
  }
  // Keyed on the revision and source: a save, reset, restore or a file appearing starts a fresh draft.
  return <Editor key={`${detail.data.layer.revision}:${detail.data.layer.source}`} detail={detail.data} scope={scope} apiKey={apiKey} maxBytes={maxBytes} hint={hint} />;
}

type EditorTab = "edit" | "preview" | "builtin" | "history";

/** The text the box starts from: what an agent gets now. */
function startingText(layer: Layer): string {
  return layer.effective_md;
}

function Editor({ detail, scope, apiKey, maxBytes, hint }: { detail: LayerDetail; scope: LayerScope; apiKey: string; maxBytes: number; hint?: string }) {
  const { layer, revisions } = detail;
  const [draft, setDraft] = useState(() => startingText(layer));
  const [tab, setTab] = useState<EditorTab>("edit");
  const [confirmReset, setConfirmReset] = useState(false);
  const edit = useInstructionEdit(scope, apiKey);
  const toast = useToast();
  const readOnly = layer.source === "file" || !layer.editable;
  const size = byteLength(draft);
  const tooBig = size > maxBytes;
  const dirty = draft !== startingText(layer);
  const badge = sourceBadge(layer);
  const tabsId = `layer-${layer.scope}-${layer.key || "global"}`;

  // mutateAsync, not mutate's callbacks: a save changes the revision, which remounts this
  // editor (the key above), and a callback of an unmounted mutate never runs.
  const report = (title: string, promise: Promise<unknown>, after?: () => void) =>
    promise.then(
      () => toast.show({ tone: "success", title, description: "Agents get it from their next start." }),
      (error: Error) => toast.show({ tone: "danger", title: "Not saved", description: error.message }),
    ).finally(after);
  const save = () => {
    if (scope === "role" && !draft.trim()) {
      toast.show({ tone: "warning", title: "A role cannot be empty", description: "Use Reset to built-in to go back to crowsnest's text." });
      return;
    }
    void report(draft.trim() ? "Instructions saved" : "Instructions cleared", edit.mutateAsync({ kind: "save", body: draft }));
  };
  const reset = () => void report("Back to the built-in role", edit.mutateAsync({ kind: "reset" }));
  const restore = (revision: LayerRevision) => void report(`Revision ${revision.revision} restored`, edit.mutateAsync({ kind: "restore", revision: revision.revision }));

  const tabs: { value: EditorTab; label: string; icon?: typeof History }[] = [
    { value: "edit", label: readOnly ? "Text" : "Edit" },
    { value: "preview", label: "Preview" },
  ];
  if (layer.builtin_md) tabs.push({ value: "builtin", label: "Diff vs built-in" });
  tabs.push({ value: "history", label: `History (${revisions.length})`, icon: History });

  return (
    <div className="flex min-w-0 flex-col gap-2" aria-label={`${layer.name} instructions`} role="group">
      <div className="flex flex-wrap items-center gap-1.5 text-xs">
        <Chip tone={badge.tone}>
          <span data-testid="layer-source">{badge.label}</span>
        </Chip>
        {layer.revision > 0 && <Chip mono>rev {layer.revision}</Chip>}
        {layer.updated_at && <span className="text-2xs text-muted">saved {relativeTime(layer.updated_at)}</span>}
        <span className={`ml-auto font-mono text-2xs ${tooBig ? "text-danger" : "text-muted"}`}>
          {formatBytes(size)} / {formatBytes(maxBytes)}
        </span>
      </div>
      {layer.source === "file" && (
        <Banner
          tone={layer.file_changed ? "danger" : "warning"}
          className="rounded"
          action={
            layer.file_changed && layer.file_hash ? (
              <Button compact onClick={() => void report("Marked read", edit.mutateAsync({ kind: "file-seen", hash: layer.file_hash ?? "" }))} disabled={edit.isPending}>
                I've read it
              </Button>
            ) : undefined
          }
        >
          {layer.file_changed && <strong>Changed on disk since you last read it, or never read. </strong>}
          From file <code className="break-all font-mono">{layer.file_path}</code>: read-only here; edit the file. It applies at each agent's next start
          {layer.file_changed ? ", and it outranks the app's text: any agent that can write that file can change what every later agent of this role is told. Read the Text tab." : "."}
          {layer.saved_md && " Text saved in the app is kept and applies again if the file goes."}
        </Banner>
      )}
      {hint && !readOnly && <p className="text-2xs text-muted">{hint}</p>}
      <Tabs<EditorTab> id={tabsId} label="Instruction views" value={tab} onChange={setTab} items={tabs} className="w-full overflow-x-auto whitespace-nowrap sm:w-auto" />
      <TabPanel id={tabsId} value={tab} className="flex min-w-0 flex-col gap-2">
        {tab === "edit" && (
          <textarea
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            readOnly={readOnly}
            aria-label={`${layer.name} instructions (markdown)`}
            rows={12}
            spellCheck={false}
            placeholder={scope === "global" ? "Instructions every agent gets, in markdown." : "Instructions in markdown."}
            className="min-h-48 w-full resize-y rounded border border-border-strong bg-surface p-2 font-mono text-xs leading-relaxed text-heading placeholder:text-muted focus-visible:outline-2 focus-visible:outline-accent read-only:bg-surface-sunken"
          />
        )}
        {tab === "preview" &&
          (draft.trim() ? (
            <div className="rounded border border-border p-3">
              <Markdown className="text-sm">{draft}</Markdown>
            </div>
          ) : (
            <p className="text-xs text-muted">Nothing: this layer is left out of the agents' instructions.</p>
          ))}
        {tab === "builtin" && <DiffView files={[{ filename: `${layer.name} (built-in → ${dirty ? "unsaved" : "current"})`, original: layer.builtin_md, final: draft }]} />}
        {tab === "history" && <Revisions revisions={revisions} current={draft} onRestore={restore} disabled={readOnly || edit.isPending} />}
      </TabPanel>
      {!readOnly && (
        <div className="flex flex-wrap items-center justify-end gap-2">
          {scope === "role" && layer.source === "db" && layer.builtin_md && !confirmReset && (
            <Button compact variant="ghost" icon={RotateCcw} onClick={() => setConfirmReset(true)} disabled={edit.isPending}>
              Reset to built-in
            </Button>
          )}
          {confirmReset && (
            <span className="flex flex-wrap items-center gap-2 text-xs text-body">
              Put the built-in text back? The edit stays in the history.
              <Button compact variant="danger" onClick={reset} disabled={edit.isPending}>
                Reset
              </Button>
              <Button compact variant="ghost" onClick={() => setConfirmReset(false)}>
                Keep it
              </Button>
            </span>
          )}
          {dirty && (
            <Button compact variant="ghost" onClick={() => setDraft(startingText(layer))}>
              Discard changes
            </Button>
          )}
          <Button compact variant="primary" icon={Save} onClick={save} disabled={!dirty || tooBig || edit.isPending}>
            {edit.isPending ? "Saving…" : "Save"}
          </Button>
        </div>
      )}
    </div>
  );
}

function Revisions({ revisions, current, onRestore, disabled }: { revisions: LayerRevision[]; current: string; onRestore: (revision: LayerRevision) => void; disabled: boolean }) {
  const [open, setOpen] = useState<number | null>(null);
  if (revisions.length === 0) return <p className="text-xs text-muted">Nothing saved yet: every save is kept here (the newest 20).</p>;
  return (
    <ol aria-label="Revisions" className="flex flex-col divide-y divide-border rounded border border-border">
      {revisions.map((revision) => (
        <li key={revision.revision} className="flex flex-col gap-1.5 px-2 py-1.5 text-xs">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono font-semibold text-heading">rev {revision.revision}</span>
            <time dateTime={revision.created_at} title={new Date(revision.created_at).toLocaleString()} className="text-2xs text-muted">
              {relativeTime(revision.created_at)}
            </time>
            <span className="text-2xs text-muted">{revision.deleted ? "reset / cleared" : formatBytes(revision.size)}</span>
            <span className="ml-auto flex gap-1">
              <Button compact variant="ghost" onClick={() => setOpen(open === revision.revision ? null : revision.revision)} aria-expanded={open === revision.revision}>
                {open === revision.revision ? "Hide diff" : "Diff"}
              </Button>
              <Button compact variant="ghost" icon={RotateCcw} onClick={() => onRestore(revision)} disabled={disabled} aria-label={`Restore revision ${revision.revision}`}>
                Restore
              </Button>
            </span>
          </div>
          {open === revision.revision && <DiffView files={[{ filename: `rev ${revision.revision} → current`, original: revision.body_md, final: current }]} />}
        </li>
      ))}
    </ol>
  );
}
