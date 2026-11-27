// Instruction layers (DESIGN.md 21.3): the TypeScript mirror of internal/instructions' views
// (Layer, List, Revision, Detail, Manifest) and the pure helpers Settings → Instructions and the
// agents' Instructions card draw with: a layer's source in words, its API path, and the subject
// an agent's start was recorded under.
import type { ChipTone } from "../components/ui/Chip";

export type LayerScope = "global" | "role" | "project";

/** Where a layer's text comes from. "brief" is a job's own brief (manifests only). */
export type LayerSource = "none" | "builtin" | "db" | "file" | "brief";

/** One layer as Settings lists it (internal/instructions Layer). */
export interface Layer {
  scope: LayerScope;
  /** "" for global, the role's name, or the project's id. */
  key: string;
  /** For display: "Global", the role's name, the project's name. */
  name: string;
  source: LayerSource;
  /** The text an agent started now gets. */
  effective_md: string;
  /** What is saved in the app ("" when nothing), even when a file wins. */
  saved_md: string;
  /** A built-in role's text; "" otherwise. */
  builtin_md: string;
  /** The override file, when the source is a file. */
  file_path: string;
  editable: boolean;
  /** The saved revision; 0 when nothing is saved. */
  revision: number;
  updated_at: string | null;
  /** Bytes of effective_md. */
  size: number;
  /** A role file's text hash, and whether it is unread or changed since it was read (R5-S14). */
  file_hash?: string;
  file_changed?: boolean;
}

/** GET /instructions. */
export interface InstructionsList {
  global: Layer;
  roles: Layer[];
  projects: Layer[];
  /** The most a body may be, in bytes. */
  max_bytes: number;
}

/** One saved revision; history only grows (restore saves the old text again). */
export interface LayerRevision {
  revision: number;
  body_md: string;
  /** A reset (role) or a clear (global, project). */
  deleted: boolean;
  created_at: string;
  size: number;
}

/** GET /instructions/{scope}/{key}. */
export interface LayerDetail {
  layer: Layer;
  revisions: LayerRevision[];
}

/** One layer an agent was started with (internal/instructions ManifestLayer). */
export interface ManifestLayer {
  /** For display: "global", "role builder", "project crowsnest", "job j_12". */
  layer: string;
  scope: LayerScope | "job";
  key: string;
  revision: number;
  source: LayerSource;
  size: number;
  hash: string;
}

/** A layer that is not what the agent started with. */
export interface LayerChange {
  layer: string;
  scope: string;
  key: string;
  was_source: string;
  /** "missing" for a role that no longer exists. */
  now_source: string;
  was_revision: number;
  now_revision: number;
}

/** A file Claude reported loading (the InstructionsLoaded hook): listed, never served. */
export interface LoadedFile {
  path: string;
  memory_type: string;
  load_reason: string;
  size: number;
  at: string;
}

/** GET /agents-instructions/{subject}. */
export interface AgentInstructions {
  subject: string;
  /** null: no start was recorded for this agent. */
  started_at: string | null;
  hash: string;
  layers: ManifestLayer[];
  changed: boolean;
  changes: LayerChange[];
  loaded: LoadedFile[];
  skills_note: string;
}

/** The subject a lead's starts are recorded under. */
export function leadSubject(slug: string): string {
  return `lead:${slug}`;
}

/** The URL key of a layer: "-" for global (an empty path segment would not route). */
export function layerKey(layer: Pick<Layer, "scope" | "key">): string {
  return layer.scope === "global" || !layer.key ? "-" : layer.key;
}

/** A stable id for a layer, for lists and selection: "global", "role:builder", "project:3". */
export function layerId(layer: Pick<Layer, "scope" | "key">): string {
  return layer.scope === "global" ? "global" : `${layer.scope}:${layer.key}`;
}

/** The badge for where a layer's text comes from. */
export function sourceBadge(layer: { scope: string; source: LayerSource; file_changed?: boolean }): { label: string; tone: ChipTone } {
  switch (layer.source) {
    case "file":
      // A role file outranks the app's text and any agent may write it (R5-S14): unread or
      // changed on disk, it says so loudly.
      return layer.file_changed ? { label: "from file: changed, review", tone: "danger" } : { label: "from file", tone: "warning" };
    case "db":
      return { label: layer.scope === "role" ? "edited" : "saved", tone: "accent" };
    case "builtin":
      return { label: "built-in", tone: "neutral" };
    case "brief":
      return { label: "brief", tone: "neutral" };
    default:
      return { label: "empty", tone: "neutral" };
  }
}

/** Bytes in a string as the server counts them (UTF-8). */
export function byteLength(text: string): number {
  return new TextEncoder().encode(text).length;
}

/** "1.2 KB" for a byte count. */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  return `${(bytes / 1024).toFixed(bytes < 10_240 ? 1 : 0)} KB`;
}
