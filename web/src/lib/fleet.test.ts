import { describe, expect, it } from "vitest";
import { makeFleet } from "./fixtures";
import { allAgents, cleanTitle, cwdTail, filterFleet, findAgent, needsYou, needsYouCount, sortFleet } from "./fleet";
import type { Workspace } from "./types";

const labels = (workspaces: Workspace[]) => workspaces.map((workspace) => workspace.label);
const agentNames = (workspaces: Workspace[]) =>
  workspaces.flatMap((w) => w.tabs.flatMap((t) => t.panes.flatMap((p) => (p.agent ? [p.agent.name] : []))));

describe("needsYou", () => {
  it("is true for blocked agents and for agents whose screen shows a dialog", () => {
    const agent = allAgents(makeFleet())[0]!;
    expect(needsYou({ ...agent, agent_status: "blocked", has_dialog: false })).toBe(true);
    expect(needsYou({ ...agent, agent_status: "idle", has_dialog: true })).toBe(true);
    expect(needsYou({ ...agent, agent_status: "working", has_dialog: false })).toBe(false);
    expect(needsYou(null)).toBe(false);
  });

  it("counts the fixture's two blocked agents", () => {
    expect(needsYouCount(makeFleet())).toBe(2);
    expect(needsYouCount(undefined)).toBe(0);
  });
});

describe("sortFleet", () => {
  it("puts workspaces with a needs-you agent first, then keeps herdr's numbering", () => {
    expect(labels(sortFleet(makeFleet().workspaces))).toEqual([
      "pumpcurves · feature/head-loss",
      "orchestrator",
      "crowsnest · wip/m3-web",
      "scratch",
    ]);
  });

  it("orders panes inside a tab: needs-you agents, other agents, then shells", () => {
    const fleet = makeFleet();
    const tab = fleet.workspaces[1]!.tabs[0]!; // m3-builder (done) then a shell
    tab.panes.reverse(); // shell first in herdr's order
    const sorted = sortFleet(fleet.workspaces).find((w) => w.workspace_id === "w2")!.tabs[0]!;
    expect(sorted.panes.map((pane) => pane.agent?.name ?? "shell")).toEqual(["m3-builder", "shell"]);
  });

  it("moves a tab holding a needs-you agent above its siblings, and ranks urgent workspaces by number", () => {
    const fleet = makeFleet();
    fleet.workspaces[1]!.tabs[1]!.panes[0]!.agent!.agent_status = "blocked"; // m3-review, in w2's second tab
    const sorted = sortFleet(fleet.workspaces);
    expect(sorted.map((w) => w.workspace_id)).toEqual(["w2", "w3", "w1", "w4"]);
    expect(sorted[0]!.tabs.map((tab) => tab.label)).toEqual(["review", "build"]);
  });

  it("does not mutate its input", () => {
    const fleet = makeFleet();
    const before = JSON.stringify(fleet);
    sortFleet(fleet.workspaces);
    expect(JSON.stringify(fleet)).toBe(before);
  });
});

describe("filterFleet", () => {
  const workspaces = makeFleet().workspaces;

  it("returns everything for an empty or blank query", () => {
    expect(filterFleet(workspaces, "")).toBe(workspaces);
    expect(filterFleet(workspaces, "   ")).toBe(workspaces);
  });

  it("matches agent names case-insensitively and drops empty groups", () => {
    const result = filterFleet(workspaces, "HEAD-LOSS");
    expect(labels(result)).toEqual(["pumpcurves · feature/head-loss"]); // the label matches too
    expect(agentNames(filterFleet(workspaces, "m3-rev"))).toEqual(["m3-review"]);
  });

  it("matches kind, status, cwd and terminal title", () => {
    expect(agentNames(filterFleet(workspaces, "codex"))).toEqual(["m3-review"]);
    expect(agentNames(filterFleet(workspaces, "blocked"))).toEqual(["head-loss", "head-loss-tests"]);
    expect(agentNames(filterFleet(workspaces, "Hydraulics/Solvers"))).toEqual(["head-loss"]);
    expect(agentNames(filterFleet(workspaces, "screenshots at 375"))).toEqual(["m3-builder"]);
  });

  it("keeps every pane of a workspace or tab whose label matches", () => {
    const result = filterFleet(workspaces, "scratch");
    expect(result).toHaveLength(1);
    expect(result[0]!.tabs[0]!.panes).toHaveLength(2);
  });

  it("returns nothing when nothing matches", () => {
    expect(filterFleet(workspaces, "no-such-thing")).toEqual([]);
  });
});

describe("helpers", () => {
  it("finds an agent with its workspace and tab", () => {
    const found = findAgent(makeFleet(), "w3:p1");
    expect(found?.agent.name).toBe("head-loss");
    expect(found?.workspace.label).toBe("pumpcurves · feature/head-loss");
    expect(found?.tab.label).toBe("builder");
    expect(findAgent(makeFleet(), "w2:p2")).toBeNull(); // a shell pane has no agent
  });

  it("shortens paths to their tail", () => {
    expect(cwdTail("/Users/jsc/Developer/pumpcurves/src")).toBe("…/pumpcurves/src");
    expect(cwdTail("/Users/jsc")).toBe("/Users/jsc");
    expect(cwdTail("")).toBe("/");
  });

  it("strips Claude's spinner glyph from titles", () => {
    expect(cleanTitle("✳ Reviewing M2")).toBe("Reviewing M2");
    expect(cleanTitle("codex: waiting")).toBe("codex: waiting");
  });
});
