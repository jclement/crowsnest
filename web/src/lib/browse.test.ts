import { describe, expect, it } from "vitest";
import { factsSummary, filterEntries, folderWarnings, hostNote, isPathInput, middlePath, refusesFolder, tildePath, type BrowseEntry, type FolderFacts } from "./browse";

function entry(name: string, extra: Partial<BrowseEntry> = {}): BrowseEntry {
  return { name, path: `/h/${name}`, dir: true, git: false, symlink: false, hidden: false, mtime: "", readable: true, ...extra };
}

function facts(extra: Partial<FolderFacts> = {}): FolderFacts {
  return {
    path: "/h/acme", resolved: "/h/acme", label: "~/acme", name: "acme", git: true, worktree: false, branch: "main", default_branch: "main",
    host: "github", remote: "git@github.com:a/acme.git", check_command: "mise run check", agents_md: true, claude_md: false,
    is_home: false, is_filesystem_root: false, project: null, contains: [], ...extra,
  };
}

describe("isPathInput", () => {
  it("tells a pasted path from a filter", () => {
    expect(isPathInput("/srv/app")).toBe(true);
    expect(isPathInput("  ~/Developer ")).toBe(true);
    expect(isPathInput("~")).toBe(true);
    expect(isPathInput("~bob")).toBe(false);
    expect(isPathInput("acme")).toBe(false);
  });
});

describe("filterEntries", () => {
  const entries = [entry("alpha"), entry("Beta", { git: true }), entry("notes.txt", { dir: false }), entry("gamma", { git: true })];
  it("floats repositories first, then folders, then files, in the server's order", () => {
    expect(filterEntries(entries, "").map((e) => e.name)).toEqual(["Beta", "gamma", "alpha", "notes.txt"]);
  });
  it("filters by name without case, and ignores a pasted path", () => {
    expect(filterEntries(entries, "BE").map((e) => e.name)).toEqual(["Beta"]);
    expect(filterEntries(entries, "/h/alpha")).toHaveLength(4);
  });
});

describe("factsSummary", () => {
  it("says what kind of folder it is in one line", () => {
    expect(factsSummary(facts())).toBe("git repo on GitHub, branch main, has a check task, AGENTS.md present");
    expect(factsSummary(facts({ host: "azure", branch: "develop", default_branch: "main", check_command: "", agents_md: false, claude_md: true, claude_trusted: true }))).toBe(
      "git repo on Azure DevOps, branch develop, base main, CLAUDE.md present, Claude trusts it",
    );
    expect(factsSummary(facts({ git: false, host: undefined, branch: undefined, check_command: "", agents_md: false }))).toBe("not a git repo");
  });
});

describe("folderWarnings", () => {
  const texts = (f: FolderFacts, purpose?: "project" | "job") => folderWarnings(f, purpose).map((w) => w.text);
  it("warns about a folder that is not a repository, or below its top", () => {
    expect(texts(facts({ git: false }))).toEqual(["Not a git repository: jobs here cannot be isolated in worktrees."]);
    expect(texts(facts({ git: false, git_top: "/h/acme" }))[0]).toMatch(/Inside the repository \/h\/acme/);
  });
  it("refuses home and / for a project", () => {
    expect(folderWarnings(facts({ is_home: true, git: false }))).toEqual([expect.objectContaining({ tone: "danger", text: expect.stringMatching(/home folder/) })]);
    expect(texts(facts({ is_filesystem_root: true }))[0]).toMatch(/is \/ itself/);
  });
  it("names an overlapping project, which only informs for a job", () => {
    const inside = facts({ project: { id: 1, slug: "acme", name: "Acme", dir: "/h/acme", exact: false } });
    expect(folderWarnings(inside)[0]).toEqual({ tone: "danger", text: "Inside the project Acme (/h/acme). Projects may not overlap." });
    expect(folderWarnings(inside, "job")[0]).toEqual({ tone: "info", text: "Inside the project Acme (/h/acme). The job joins it." });
    expect(texts(facts({ contains: [{ id: 2, slug: "x", name: "X", dir: "/h/acme/x", exact: false }] }))).toEqual(["Holds the project X: a project here would overlap."]);
  });
});

describe("hostNote", () => {
  it("says whether Land can open pull requests", () => {
    expect(hostNote(facts())).toBe("GitHub: Land can open pull requests here.");
    expect(hostNote(facts({ host: "azure" }))).toBe("Azure DevOps: Land will not open pull requests here; it merges locally.");
    expect(hostNote(facts({ host: "other" }))).toMatch(/^Not GitHub: /);
    expect(hostNote(facts({ host: undefined }))).toBe("No remote: Land merges locally.");
    expect(hostNote(facts({ git: false }))).toBe("");
  });
});

describe("tildePath", () => {
  it("shows the home directory as ~", () => {
    expect(tildePath("/h", "/h")).toBe("~");
    expect(tildePath("/h/x", "/h")).toBe("~/x");
    expect(tildePath("/hx", "/h")).toBe("/hx");
  });
});

// R10 O-B2: the picker warned "too wide for a project" and then selected the home folder anyway.
describe("refusesFolder", () => {
  it("refuses, for a project, every folder it warns about in red", () => {
    expect(refusesFolder(facts({ is_home: true }))).toBe(true);
    expect(refusesFolder(facts({ is_filesystem_root: true }))).toBe(true);
    expect(refusesFolder(facts({ project: { id: 1, slug: "site", name: "Site", dir: "/h/acme", exact: true } }))).toBe(true);
    expect(refusesFolder(facts({ contains: [{ id: 1, slug: "site", name: "Site", dir: "/h/acme/site", exact: false }] }))).toBe(true);
    expect(refusesFolder(facts())).toBe(false);
    expect(refusesFolder(facts({ git: false }))).toBe(false); // a warning, not a refusal
  });

  it("refuses nothing for a job, which may run anywhere it is pointed", () => {
    expect(refusesFolder(facts({ is_home: true }), "job")).toBe(false);
  });
});

describe("middlePath", () => {
  it("keeps the start and the last two folders of a long path", () => {
    expect(middlePath("/tmp/claude-501/r10/crowsnest-demo-2921922139/state/demo-src/website")).toBe("/tmp/…/demo-src/website");
    expect(middlePath("~/Developer/crowsnest")).toBe("~/Developer/crowsnest");
    expect(middlePath("~/Work/clients/acme/projects/storefront-api", 20)).toBe("~/…/projects/storefront-api");
  });
});
