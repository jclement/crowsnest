// AgentInstructionsCard: what an agent was started with (DESIGN.md 21.3, GET
// /agents-instructions/{subject}): crowsnest's layers in order, each with where its text came
// from (a click opens that layer's current text), the files Claude reported loading (path, memory
// type, why, size, when; by path only, their text is never fetched), and the note on skills.
// When a layer changed since the start, a banner says so and offers "Restart with new
// instructions" (a job's agent resumes with a fresh prompt file; a lead or the orchestrator
// restarts on the same conversation). Shown on the job detail, the project page (its lead) and
// from Chat's menu (the orchestrator or a lead).
import { BookText, RotateCw } from "lucide-react";
import { useState } from "react";
import { relativeTime } from "../../lib/format";
import { formatBytes, sourceBadge, type AgentInstructions, type LayerScope, type ManifestLayer } from "../../lib/instructions";
import { useAgentInstructions, useInstructionLayer, useRestartWithInstructions } from "../../lib/queries";
import { Markdown } from "../Markdown";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Chip } from "../ui/Chip";
import { Sheet } from "../ui/Sheet";
import { SkeletonRows } from "../ui/Skeleton";
import { useToast } from "../ui/toast-context";

/** Who to restart for new instructions. */
export type RestartTarget = { kind: "job"; id: string } | { kind: "lead"; slug: string } | { kind: "orchestrator" };

interface AgentInstructionsCardProps {
  /** j_<n>, orchestrator or lead:<slug>. */
  subject: string;
  restart: RestartTarget;
  /** The agent in words, for the restart question ("j_12's agent", "the crowsnest lead"). */
  who: string;
  /** Restarting is offered only while the agent runs. */
  canRestart?: boolean;
}

export function AgentInstructionsCard({ subject, restart, who, canRestart = true }: AgentInstructionsCardProps) {
  const query = useAgentInstructions(subject);
  return (
    <Card title={<>Instructions</>} aria-label="Instructions" bodyClassName="p-0">
      {query.isPending ? (
        <SkeletonRows rows={3} label="Loading the instructions" />
      ) : query.isError ? (
        <p className="px-3 py-2 text-xs text-danger">Could not load them: {query.error.message}</p>
      ) : (
        <Body data={query.data} restart={restart} who={who} canRestart={canRestart} />
      )}
    </Card>
  );
}

function Body({ data, restart, who, canRestart }: { data: AgentInstructions; restart: RestartTarget; who: string; canRestart: boolean }) {
  const [viewing, setViewing] = useState<ManifestLayer | null>(null);
  if (!data.started_at) {
    return <p className="px-3 py-2 text-xs text-muted">No start recorded yet: the layers are recorded each time the agent starts.</p>;
  }
  return (
    <div className="flex flex-col">
      {data.changed && <ChangedBanner data={data} restart={restart} who={who} canRestart={canRestart} />}
      <p className="px-3 pt-2 text-2xs text-muted">
        Started {relativeTime(data.started_at)} with these layers, in order <span className="font-mono">({data.hash.slice(0, 8)})</span>:
      </p>
      <ol aria-label="Layers at start" className="divide-y divide-border">
        {data.layers.map((layer) => {
          const badge = sourceBadge(layer);
          const viewable = layer.scope !== "job" && layer.source !== "none";
          return (
            <li key={`${layer.scope}:${layer.key}`} className="flex min-h-9 items-center gap-2 px-3 py-1 text-xs">
              {viewable ? (
                <button type="button" onClick={() => setViewing(layer)} className="min-h-11 min-w-0 flex-1 truncate text-left text-accent hover:underline md:min-h-7">
                  {layer.layer}
                </button>
              ) : (
                <span className="min-w-0 flex-1 truncate text-body">{layer.layer}</span>
              )}
              <Chip tone={badge.tone}>{badge.label}</Chip>
              {layer.revision > 0 && <span className="font-mono text-2xs text-muted">rev {layer.revision}</span>}
              <span className="w-14 text-right font-mono text-2xs text-muted">{layer.size ? formatBytes(layer.size) : "empty"}</span>
            </li>
          );
        })}
      </ol>
      <LoadedFiles data={data} />
      {data.skills_note && <p className="border-t border-border px-3 py-2 text-2xs text-muted">{data.skills_note}</p>}
      <LayerViewer layer={viewing} onClose={() => setViewing(null)} />
    </div>
  );
}

function ChangedBanner({ data, restart, who, canRestart }: { data: AgentInstructions; restart: RestartTarget; who: string; canRestart: boolean }) {
  const [confirming, setConfirming] = useState(false);
  const run = useRestartWithInstructions();
  const toast = useToast();
  const go = () =>
    run.mutate(restart, {
      onSuccess: () => {
        setConfirming(false);
        toast.show({ tone: "success", title: "Restarted with the new instructions" });
      },
      onError: (error) => toast.show({ tone: "danger", title: "Not restarted", description: error.message }),
    });
  return (
    <Banner
      tone="warning"
      role="status"
      className="border-x-0 border-t-0"
      action={
        canRestart &&
        !confirming && (
          <Button compact variant="warning" icon={RotateCw} onClick={() => setConfirming(true)}>
            Restart with new instructions
          </Button>
        )
      }
    >
      <strong className="font-semibold">Instructions changed since start.</strong>{" "}
      {data.changes.map((change) => change.layer).join(", ")} {data.changes.length === 1 ? "is" : "are"} not what {who} started with; it keeps the old text until it restarts.
      {confirming && (
        <span className="mt-1.5 flex flex-wrap items-center gap-2 text-body">
          Restart {who}? Anything it is doing is interrupted; it resumes the same conversation.
          <Button compact variant="danger" onClick={go} disabled={run.isPending}>
            {run.isPending ? "Restarting…" : "Restart"}
          </Button>
          <Button compact variant="ghost" onClick={() => setConfirming(false)}>
            Not now
          </Button>
        </span>
      )}
    </Banner>
  );
}

function LoadedFiles({ data }: { data: AgentInstructions }) {
  return (
    <section aria-label="Files Claude loaded" className="border-t border-border">
      <h4 className="px-3 pt-2 text-2xs font-semibold uppercase tracking-wide text-muted">Files Claude loaded</h4>
      {data.loaded.length === 0 ? (
        <p className="px-3 pb-2 text-xs text-muted">None reported yet (CLAUDE.md and rules files are reported as Claude loads them).</p>
      ) : (
        <ul className="divide-y divide-border">
          {data.loaded.map((file) => (
            <li key={`${file.path}:${file.at}`} className="flex flex-col gap-0.5 px-3 py-1.5 text-xs">
              <code className="break-all font-mono text-heading">{file.path}</code>
              <span className="flex flex-wrap gap-x-2 text-2xs text-muted">
                <span>{file.memory_type}</span>
                <span>{file.load_reason.replace(/_/g, " ")}</span>
                <span>{formatBytes(file.size)}</span>
                <time dateTime={file.at} title={new Date(file.at).toLocaleString()}>
                  {relativeTime(file.at)}
                </time>
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/** A layer's current text, from /instructions (crowsnest's own layers only, never the user's files). */
function LayerViewer({ layer, onClose }: { layer: ManifestLayer | null; onClose: () => void }) {
  return (
    <Sheet open={!!layer} onClose={onClose} title={layer ? `Layer: ${layer.layer}` : ""} size="md">
      {layer && <LayerText scope={layer.scope as LayerScope} apiKey={layer.scope === "global" || !layer.key ? "-" : layer.key} />}
    </Sheet>
  );
}

function LayerText({ scope, apiKey }: { scope: LayerScope; apiKey: string }) {
  const detail = useInstructionLayer(scope, apiKey);
  if (detail.isPending) return <SkeletonRows rows={4} label="Loading the layer" />;
  if (detail.isError) return <p className="p-4 text-xs text-danger">Could not load it: {detail.error.message}</p>;
  const layer = detail.data.layer;
  const badge = sourceBadge(layer);
  return (
    <div className="flex flex-col gap-2 p-4">
      <p className="flex flex-wrap items-center gap-1.5 text-xs text-muted">
        <BookText className="h-4 w-4" aria-hidden /> As it applies now
        <Chip tone={badge.tone}>{badge.label}</Chip>
        {layer.file_path && <code className="break-all font-mono">{layer.file_path}</code>}
      </p>
      {layer.effective_md.trim() ? <Markdown className="text-sm">{layer.effective_md}</Markdown> : <p className="text-xs text-muted">Empty.</p>}
    </div>
  );
}
