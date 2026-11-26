// Pure derivations over the fleet snapshot for the Fleet view: which agents need Jeff, the
// sort order (needs-you first), the filter, and small display helpers. Kept free of React so
// the rules are tested directly (fleet.test.ts) rather than through the DOM.
import type { Agent, Fleet, Pane, PaneAgent, Tab, Workspace } from "./types";

/** An agent needs Jeff when it is blocked or its screen shows a dialog (herdr sometimes calls a
 * dialog idle, so the screen check is the second signal; DESIGN.md section 8.2). The server sends
 * the same rule as `needs_you` on the flat list; the tree's agents are judged here. */
export function needsYou(agent: PaneAgent | null | undefined): boolean {
  return !!agent && (agent.status === "blocked" || agent.has_dialog);
}

/** Every agent in the fleet, flat, in tree order (the server's `agents` list). */
export function allAgents(fleet: Fleet | undefined): Agent[] {
  return fleet?.agents ?? [];
}

/** How many agents are waiting on Jeff: the Inbox badge until questions arrive in M6. */
export function needsYouCount(fleet: Fleet | undefined): number {
  return allAgents(fleet).filter((agent) => agent.needs_you).length;
}

/** The Inbox badge: open questions plus agents waiting at a terminal dialog. */
export function inboxCount(openQuestions: number | undefined, dialogs: number): number {
  return (openQuestions ?? 0) + dialogs;
}

/** Finds the agent in a pane, with the workspace and tab it sits in. */
export function findAgent(fleet: Fleet | undefined, paneId: string): { agent: Agent; workspace: Workspace; tab: Tab } | null {
  const agent = fleet?.agents.find((entry) => entry.pane_id === paneId);
  if (!fleet || !agent) return null;
  const workspace = fleet.workspaces.find((candidate) => candidate.id === agent.workspace_id);
  const tab = workspace?.tabs.find((candidate) => candidate.id === agent.tab_id);
  return workspace && tab ? { agent, workspace, tab } : null;
}

/** Finds any pane (agent or plain shell) with the workspace and tab it sits in. */
export function findPane(fleet: Fleet | undefined, paneId: string): { pane: Pane; workspace: Workspace; tab: Tab } | null {
  for (const workspace of fleet?.workspaces ?? [])
    for (const tab of workspace.tabs)
      for (const pane of tab.panes) if (pane.id === paneId) return { pane, workspace, tab };
  return null;
}

// ---- Sorting and filtering ---------------------------------------------------

function paneNeedsYou(pane: Pane): boolean {
  return needsYou(pane.agent);
}

function tabNeedsYou(tab: Tab): boolean {
  return tab.panes.some(paneNeedsYou);
}

function workspaceNeedsYou(workspace: Workspace): boolean {
  return workspace.tabs.some(tabNeedsYou);
}

/**
 * Orders the tree so whatever needs Jeff comes first at every level: workspaces holding a
 * needs-you agent before the rest, then by herdr's number; the same for tabs inside a
 * workspace; inside a tab, needs-you agents first, then other agents, then plain shells, each
 * group keeping herdr's order. The sort is stable, so the tree does not shuffle between polls.
 */
export function sortFleet(workspaces: Workspace[]): Workspace[] {
  const rank = (urgent: boolean) => (urgent ? 0 : 1);
  const paneRank = (pane: Pane) => (paneNeedsYou(pane) ? 0 : pane.agent ? 1 : 2);
  return [...workspaces]
    .sort((a, b) => rank(workspaceNeedsYou(a)) - rank(workspaceNeedsYou(b)) || a.number - b.number)
    .map((workspace) => ({
      ...workspace,
      tabs: [...workspace.tabs]
        .sort((a, b) => rank(tabNeedsYou(a)) - rank(tabNeedsYou(b)) || a.number - b.number)
        .map((tab) => ({ ...tab, panes: [...tab.panes].sort((a, b) => paneRank(a) - paneRank(b)) })),
    }));
}

function matches(query: string, ...fields: (string | undefined)[]): boolean {
  return fields.some((field) => field?.toLowerCase().includes(query));
}

function paneMatches(pane: Pane, query: string): boolean {
  const agent = pane.agent;
  if (!agent) return matches(query, pane.cwd, pane.id);
  return matches(query, agent.name, agent.kind, agent.status, pane.cwd, agent.title, pane.id);
}

/**
 * Keeps the parts of the tree that match a free-text query (case-insensitive): an agent by its
 * name, kind, status, cwd or terminal title; a shell pane by its cwd. A workspace or tab whose
 * own label matches keeps all of its panes. With an empty query the tree is returned as is.
 * Groups left empty by the filter are dropped.
 */
export function filterFleet(workspaces: Workspace[], rawQuery: string): Workspace[] {
  const query = rawQuery.trim().toLowerCase();
  if (!query) return workspaces;
  return workspaces.flatMap((workspace) => {
    if (matches(query, workspace.label)) return [workspace];
    const tabs = workspace.tabs.flatMap((tab) => {
      if (matches(query, tab.label)) return [tab];
      const panes = tab.panes.filter((pane) => paneMatches(pane, query));
      return panes.length ? [{ ...tab, panes }] : [];
    });
    return tabs.length ? [{ ...workspace, tabs }] : [];
  });
}

// ---- Display helpers ---------------------------------------------------------

/** The last `segments` parts of a path, prefixed with "…/" when anything was cut. */
export function cwdTail(path: string, segments = 2): string {
  const parts = path.split("/").filter(Boolean);
  if (parts.length <= segments) return path || "/";
  return "…/" + parts.slice(-segments).join("/");
}

/** Claude prefixes its terminal title with a spinner glyph; it adds nothing in a list. */
export function cleanTitle(title: string): string {
  return title.replace(/^[\s✳✻✽✶·*⠁-⣿]+/u, "").trim();
}

/** The tab title: the count of what needs the human, then crowsnest and the instance's name. */
export function pageTitle(needsYou: number, name?: string): string {
  const base = name ? `crowsnest · ${name}` : "crowsnest";
  return needsYou > 0 ? `(${needsYou}) ${base}` : base;
}
