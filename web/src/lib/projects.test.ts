import { describe, expect, it } from "vitest";
import type { Job } from "./jobs";
import {
  ALL_PROJECTS,
  dirBasename,
  filterFromParam,
  filterJobs,
  filterToParam,
  leadAgentName,
  leadChip,
  loadProjectFilter,
  PROJECT_FILTER_KEY,
  resolveProjectFilter,
  saveProjectFilter,
  slugOfLead,
  suggestName,
} from "./projects";

const projects = [
  { id: 1, slug: "crowsnest" },
  { id: 2, slug: "website" },
];
const job = (id: string, projectId: number) => ({ id, project_id: projectId }) as Job;

/** A Storage stand-in. */
function memoryStorage(initial: Record<string, string> = {}) {
  const values = new Map(Object.entries(initial));
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => void values.set(key, value),
    values,
  };
}

describe("the project switcher's filter", () => {
  it("round-trips through the URL word: all, unfiled, a slug", () => {
    expect(filterToParam(ALL_PROJECTS)).toBe("");
    expect(filterFromParam("")).toEqual(ALL_PROJECTS);
    expect(filterFromParam("all")).toEqual(ALL_PROJECTS);
    expect(filterFromParam("unfiled")).toEqual({ kind: "unfiled" });
    expect(filterFromParam("website")).toEqual({ kind: "project", slug: "website" });
    expect(filterToParam({ kind: "project", slug: "website" })).toBe("website");
  });

  it("is remembered per browser", () => {
    const storage = memoryStorage();
    saveProjectFilter({ kind: "project", slug: "crowsnest" }, storage);
    expect(storage.values.get(PROJECT_FILTER_KEY)).toBe("crowsnest");
    expect(loadProjectFilter(storage)).toEqual({ kind: "project", slug: "crowsnest" });
    saveProjectFilter({ kind: "unfiled" }, storage);
    expect(loadProjectFilter(storage)).toEqual({ kind: "unfiled" });
    expect(loadProjectFilter(memoryStorage())).toEqual(ALL_PROJECTS);
  });

  it("survives storage that throws", () => {
    const broken = {
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => {
        throw new Error("blocked");
      },
    };
    expect(loadProjectFilter(broken)).toEqual(ALL_PROJECTS);
    expect(() => saveProjectFilter(ALL_PROJECTS, broken)).not.toThrow();
  });

  it("prefers the URL, and drops a remembered project that is gone", () => {
    expect(resolveProjectFilter("website", { kind: "unfiled" }, projects)).toEqual({ kind: "project", slug: "website" });
    expect(resolveProjectFilter(undefined, { kind: "project", slug: "gone" }, projects)).toEqual(ALL_PROJECTS);
    expect(resolveProjectFilter(undefined, { kind: "project", slug: "gone" }, undefined)).toEqual({ kind: "project", slug: "gone" });
    expect(resolveProjectFilter(undefined, { kind: "unfiled" }, projects)).toEqual({ kind: "unfiled" });
  });

  it("keeps the jobs of the chosen project, the unfiled ones, or all", () => {
    const jobs = [job("j_1", 1), job("j_2", 2), job("j_3", 0), { id: "j_4" } as Job];
    expect(filterJobs(jobs, ALL_PROJECTS, projects).map((j) => j.id)).toEqual(["j_1", "j_2", "j_3", "j_4"]);
    expect(filterJobs(jobs, { kind: "project", slug: "website" }, projects).map((j) => j.id)).toEqual(["j_2"]);
    expect(filterJobs(jobs, { kind: "unfiled" }, projects).map((j) => j.id)).toEqual(["j_3", "j_4"]);
    expect(filterJobs(jobs, { kind: "project", slug: "nope" }, projects)).toEqual([]);
  });
});

describe("leads", () => {
  it("are agents named lead-<slug>", () => {
    expect(leadAgentName("website")).toBe("lead-website");
    expect(slugOfLead("lead-website")).toBe("website");
    expect(slugOfLead("orchestrator")).toBeNull();
    expect(slugOfLead("lead-")).toBeNull();
  });

  it("say their state in words", () => {
    expect(leadChip({ state: "off", running: false }).label).toBe("Lead off");
    expect(leadChip({ state: "idle", running: true })).toEqual({ label: "Lead idle", tone: "success" });
    expect(leadChip({ state: "working", running: true }).tone).toBe("accent");
    expect(leadChip({ state: "blocked", running: true }).tone).toBe("danger");
  });
});

describe("a new project's name from its directory", () => {
  it("is the basename, words split and capitalised", () => {
    expect(dirBasename("~/src/web-site/")).toBe("web-site");
    expect(suggestName("~/src/web-site/")).toBe("Web site");
    expect(suggestName("/home/crow/crowsnest")).toBe("Crowsnest");
    expect(suggestName("/tmp/my_project.v2")).toBe("My project v2");
    expect(suggestName("~")).toBe("");
    expect(suggestName("")).toBe("");
  });
});
