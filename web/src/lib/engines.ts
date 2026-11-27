// What each engine gives a job (DESIGN.md 20.1): the TypeScript mirror of internal/engines, served
// by GET /api/v1/engines. The job form, a project's default engine, a Codex job's card and detail,
// and Rescue all read this one table, so none of them can promise what the server does not do.

export interface EngineCapability {
  id: string;
  label: string;
  available: boolean;
  /** Holds with a stated gap (the note says which). */
  partial?: boolean;
  note: string;
}

export interface EngineModel {
  id: string;
  label: string;
}

export interface EngineRescue {
  /** interrupt, continue, nudge, unstick, restart, compact or model. */
  id: string;
  available: boolean;
  /** The server's sentence refusing it on this engine. */
  refusal?: string;
}

export interface EngineInfo {
  id: "claude" | "codex" | string;
  name: string;
  installed: boolean;
  summary: string;
  capabilities: EngineCapability[];
  rescue: EngineRescue[];
  models: EngineModel[];
  /** The labels of the guarantees it runs without. */
  without: string[];
}

/** The engine's row, if the table names it. */
export function engineInfo(table: EngineInfo[] | undefined, id: string): EngineInfo | undefined {
  return table?.find((engine) => engine.id === (id || "claude"));
}

/** Whether a rescue action works on the engine; true while the table is not loaded (the server refuses anyway). */
export function rescueAvailable(info: EngineInfo | undefined, action: string): boolean {
  const row = info?.rescue.find((rescue) => rescue.id === action);
  return row ? row.available : true;
}

/** "Runs without the stop guarantee, …" in one sentence, or "" when it runs without nothing. */
export function withoutSentence(info: EngineInfo | undefined): string {
  if (!info || info.without.length === 0) return "";
  const names = info.without.map((label) => label.charAt(0).toLowerCase() + label.slice(1));
  const list = names.length === 1 ? names[0] : `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
  return `${info.name} runs without ${list}.`;
}
