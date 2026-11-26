// Fixture projects for the unit tests: three projects in the demo's shape (two leads running,
// one off), as GET /projects returns them.
import type { LeadStatus, ProjectList, ProjectView } from "./projects";

const now = new Date().toISOString();

export function makeLead(overrides: Partial<LeadStatus> = {}): LeadStatus {
  return {
    state: "idle", name: "lead-crowsnest", pane_id: "w4:p1", session_id: "s-1", dir: "/src/crowsnest", model: "sonnet", restarts: 0, updated_at: now,
    project_id: 1, slug: "crowsnest", running: true, ...overrides,
  };
}

export function makeProject(overrides: Partial<ProjectView> = {}): ProjectView {
  const slug = overrides.slug ?? "crowsnest";
  return {
    id: 1, slug, name: "Crowsnest", dir: `/src/${slug}`, root_dir: `/src/${slug}`, description: "", default_role: "builder", default_engine: "", default_model: "",
    default_isolate: null, permission_mode: "", lead_start: "job", archived: false, archived_at: null, created_at: now, updated_at: now,
    lead: makeLead({ slug, name: `lead-${slug}`, project_id: overrides.id ?? 1 }),
    counts: { needs_you: 1, running: 2, total: 4 },
    ...overrides,
  };
}

export function makeProjectList(): ProjectList {
  return {
    projects: [
      makeProject(),
      makeProject({ id: 2, slug: "website", name: "Website", counts: { needs_you: 0, running: 1, total: 1 } }),
      makeProject({ id: 3, slug: "infra", name: "Infra", lead_start: "manual", lead: makeLead({ slug: "infra", name: "lead-infra", project_id: 3, state: "off", running: false, pane_id: "" }), counts: { needs_you: 0, running: 0, total: 0 } }),
    ],
    unfiled: { needs_you: 1, running: 1, total: 2 },
  };
}
