// Settings → Instructions (DESIGN.md 21.3), opened from the account menu and the command
// palette: every instruction layer crowsnest adds to the agents it starts — Global (every agent),
// each role (built-in, edited, or from a file and then read-only), each project — with the layer
// editor (components/instructions/LayerEditor.tsx). A list beside the editor from md; on a phone
// the list, then the chosen layer with a way back. Nothing here touches the user's own CLAUDE.md
// or AGENTS.md: those still load as Claude loads them, and are only listed (by path) on an
// agent's Instructions card.
import { ArrowLeft, BookText } from "lucide-react";
import { useState } from "react";
import { cn } from "../../lib/cn";
import { layerId, layerKey, sourceBadge, type InstructionsList, type Layer } from "../../lib/instructions";
import { DESKTOP_QUERY, useMediaQuery } from "../../lib/media";
import { useInstructions } from "../../lib/queries";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Chip } from "../ui/Chip";
import { EmptyState } from "../ui/EmptyState";
import { Sheet } from "../ui/Sheet";
import { SkeletonRows } from "../ui/Skeleton";
import { LayerEditor } from "./LayerEditor";
import { HelpButton } from "../help/HelpButton";

export function InstructionsSheet({ open, onClose, initial }: { open: boolean; onClose: () => void; initial?: string }) {
  return (
    <Sheet open={open} onClose={onClose} title="Instructions" headerExtra={<HelpButton topic="concepts/instruction-layers" what="instruction layers" inline={false} />}>
      {open && <InstructionsPanel initial={initial} />}
    </Sheet>
  );
}

const hints: Record<Layer["scope"], string> = {
  global: "Every agent crowsnest starts gets this: job agents, project leads and the orchestrator.",
  role: "Job agents with this role get this, after the global layer.",
  project: "The project's lead and its job agents get this, after the global and role layers.",
};

/** The layers and the editor. `initial` is a layer id ("global", "role:builder", "project:3"). */
export function InstructionsPanel({ initial }: { initial?: string }) {
  const list = useInstructions();
  const desktop = useMediaQuery(DESKTOP_QUERY);
  const [selected, setSelected] = useState<string | null>(initial ?? null);
  if (list.isPending) return <SkeletonRows rows={6} label="Loading the instructions" />;
  if (list.isError) {
    return (
      <Banner tone="danger" role="alert">
        Could not load the instructions: {list.error.message}
      </Banner>
    );
  }
  const layers = allLayers(list.data);
  const current = layers.find((layer) => layerId(layer) === (selected ?? (desktop ? "global" : "")));
  const showList = desktop || !current;
  return (
    <div className="flex min-h-0 flex-col md:flex-row">
      {showList && (
        <nav aria-label="Instruction layers" className="shrink-0 border-border md:w-52 md:border-r">
          <LayerGroup title="Global" layers={[list.data.global]} selected={current} onSelect={setSelected} />
          <LayerGroup title="Roles" layers={list.data.roles} selected={current} onSelect={setSelected} />
          <LayerGroup title="Projects" layers={list.data.projects} selected={current} onSelect={setSelected} empty="No projects yet." />
        </nav>
      )}
      {current ? (
        <section aria-label={`${current.name} layer`} className="min-w-0 flex-1 p-3">
          {!desktop && (
            <Button compact variant="ghost" icon={ArrowLeft} onClick={() => setSelected(null)} className="-ml-2 mb-1">
              All layers
            </Button>
          )}
          <h3 className="mb-2 text-sm font-semibold text-heading">
            {current.scope === "global" ? "Global" : current.scope === "role" ? `Role: ${current.name}` : `Project: ${current.name}`}
          </h3>
          <LayerEditor key={layerId(current)} scope={current.scope} apiKey={layerKey(current)} maxBytes={list.data.max_bytes} hint={hints[current.scope]} />
        </section>
      ) : (
        desktop && (
          <EmptyState icon={BookText} title="Pick a layer">
            Its text, preview, history and, for a role, the diff against the built-in open here.
          </EmptyState>
        )
      )}
    </div>
  );
}

function allLayers(list: InstructionsList): Layer[] {
  return [list.global, ...list.roles, ...list.projects];
}

function LayerGroup({ title, layers, selected, onSelect, empty }: { title: string; layers: Layer[]; selected?: Layer; onSelect: (id: string) => void; empty?: string }) {
  return (
    <section aria-label={title}>
      <h4 className="border-b border-border bg-surface-raised px-3 py-1 text-2xs font-semibold uppercase tracking-wide text-muted">{title}</h4>
      {layers.length === 0 && empty && <p className="px-3 py-2 text-xs text-muted">{empty}</p>}
      <ul className="divide-y divide-border">
        {layers.map((layer) => {
          const badge = sourceBadge(layer);
          const active = selected && layerId(selected) === layerId(layer);
          return (
            <li key={layerId(layer)}>
              <button
                type="button"
                onClick={() => onSelect(layerId(layer))}
                aria-current={active || undefined}
                className={cn("flex min-h-11 w-full items-center gap-2 px-3 text-left text-sm hover:bg-hover md:min-h-9", active && "bg-selected font-medium text-heading")}
              >
                <span className="min-w-0 flex-1 truncate">{layer.scope === "global" ? "Every agent" : layer.name}</span>
                <Chip tone={badge.tone}>{badge.label}</Chip>
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
