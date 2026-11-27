// The standalone diff route's search (/diff?job=j_12&scope=all&file=…), validated (DESIGN.md 6.21).
import { scopes, type ChangeScope, type DiffWho } from "./changes";

export interface DiffSearch {
  job?: string;
  project?: string;
  orchestrator?: string;
  scope?: ChangeScope;
  file?: string;
  base?: string;
  from?: string;
}

export function validateDiffSearch(search: Record<string, unknown>): DiffSearch {
  const text = (key: string) => (typeof search[key] === "string" && search[key] ? (search[key] as string) : undefined);
  const scope = text("scope");
  const from = text("from");
  return {
    job: text("job"),
    project: text("project"),
    orchestrator: text("orchestrator"),
    scope: scope && (scopes as string[]).includes(scope) ? (scope as ChangeScope) : undefined,
    file: text("file"),
    base: text("base"),
    // Only an in-app path: never a link off the app.
    from: from && from.startsWith("/") && !from.startsWith("//") ? from : undefined,
  };
}

export function whoFromSearch(search: DiffSearch): DiffWho | null {
  if (search.job) return { kind: "job", id: search.job };
  if (search.project) return { kind: "project", ref: search.project };
  if (search.orchestrator) return { kind: "orchestrator" };
  return null;
}

