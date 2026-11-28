// The folder picker's types (the TypeScript mirror of internal/browse and internal/httpd/browse.go,
// DESIGN.md 4.23 and 10) and the pure logic the picker and the forms share: filtering a listing,
// telling a pasted path from a filter, the one-line summary of a folder's facts, its warnings,
// and the note about where its remote is hosted. Kept free of React so Vitest covers it directly.

import type { AgentStartFinding } from "./projects";

/** Where a place came from. */
export type PlaceKind = "home" | "root" | "project" | "remembered" | "recent" | "project-parent" | "favourite";

/** A root or favourite: GET /browse/places. */
export interface Place {
  path: string;
  label: string;
  kind: PlaceKind;
  git?: boolean;
}

export interface BrowsePlaces {
  home: string;
  favourites: Place[];
  roots: Place[];
}

export interface Crumb {
  name: string;
  path: string;
  browsable: boolean;
}

export interface BrowseEntry {
  name: string;
  path: string;
  dir: boolean;
  git: boolean;
  symlink: boolean;
  target?: string;
  /** A link out of the browsable folders: shown, never entered or chosen. */
  outside?: boolean;
  hidden: boolean;
  mtime: string;
  readable: boolean;
  error?: string;
}

/** GET /browse: one directory. */
export interface Listing {
  path: string;
  resolved: string;
  label: string;
  root: Place;
  parent: string;
  crumbs: Crumb[];
  entries: BrowseEntry[];
  more: number;
  hidden_count: number;
  file_count: number;
  partial: boolean;
  git: boolean;
}

export type RemoteHost = "github" | "azure" | "gitlab" | "bitbucket" | "other";

export interface FolderProject {
  id: number;
  slug: string;
  name: string;
  dir: string;
  exact: boolean;
}

/** GET /browse/facts. */
export interface FolderFacts {
  path: string;
  resolved: string;
  label: string;
  name: string;
  git: boolean;
  worktree: boolean;
  git_top?: string;
  branch?: string;
  detached?: boolean;
  default_branch?: string;
  remote?: string;
  host?: RemoteHost;
  check_command?: string;
  agents_md: boolean;
  claude_md: boolean;
  /** What the folder carries that an agent runs or reads at its start (R9-S4). */
  agent_start?: AgentStartFinding[];
  /** The folder's .codex/ fingerprint (absent for none), sent back with the trust box (R10 S5a). */
  codex_fingerprint?: string;
  claude_trusted?: boolean;
  is_home: boolean;
  is_filesystem_root: boolean;
  project: FolderProject | null;
  contains: FolderProject[];
}

export interface BrowseOptions {
  files?: boolean;
  hidden?: boolean;
}

// ---- Filtering ------------------------------------------------------------------------------

/** True when the filter box holds a path to go to rather than a filter: `/…` or `~`, `~/…`. */
export function isPathInput(text: string): boolean {
  const value = text.trim();
  return value.startsWith("/") || value === "~" || value.startsWith("~/");
}

/**
 * The entries the picker shows: those whose name contains the filter (any case), repositories
 * first, then other folders, then files, keeping the server's name order within each.
 */
export function filterEntries(entries: BrowseEntry[], filter: string): BrowseEntry[] {
  const needle = isPathInput(filter) ? "" : filter.trim().toLowerCase();
  const matched = needle ? entries.filter((entry) => entry.name.toLowerCase().includes(needle)) : entries;
  const rank = (entry: BrowseEntry) => (entry.dir && entry.git ? 0 : entry.dir ? 1 : 2);
  return matched
    .map((entry, index) => ({ entry, index }))
    .sort((a, b) => rank(a.entry) - rank(b.entry) || a.index - b.index)
    .map(({ entry }) => entry);
}

/** Whether an entry can be opened (descended into) and chosen. */
export function canEnter(entry: BrowseEntry): boolean {
  return entry.dir && entry.readable && !entry.outside;
}

// ---- Facts in words -------------------------------------------------------------------------

export const hostLabels: Record<RemoteHost, string> = {
  github: "GitHub",
  azure: "Azure DevOps",
  gitlab: "GitLab",
  bitbucket: "Bitbucket",
  other: "another host",
};

/** "git repo on GitHub, branch main, has a check task, AGENTS.md present". */
export function factsSummary(facts: FolderFacts): string {
  const parts: string[] = [];
  if (facts.git) {
    const where = facts.host ? ` on ${hostLabels[facts.host]}` : ", no remote";
    parts.push(`${facts.worktree ? "git worktree" : "git repo"}${where}`);
    if (facts.branch) parts.push(`branch ${facts.branch}`);
    else if (facts.detached) parts.push("detached HEAD");
    if (facts.default_branch && facts.default_branch !== facts.branch) parts.push(`base ${facts.default_branch}`);
  } else {
    parts.push("not a git repo");
  }
  if (facts.check_command) parts.push("has a check task");
  const docs = [facts.agents_md ? "AGENTS.md" : "", facts.claude_md ? "CLAUDE.md" : ""].filter(Boolean);
  if (docs.length > 0) parts.push(`${docs.join(" and ")} present`);
  if (facts.claude_trusted === true) parts.push("Claude trusts it");
  return parts.join(", ");
}

export type WarningTone = "warning" | "danger" | "info";

export interface FolderWarning {
  tone: WarningTone;
  text: string;
}

/**
 * What the human should know before choosing a folder for a project (or, with purpose "job",
 * for a job): too wide, not a repository, inside or around a project.
 */
export function folderWarnings(facts: FolderFacts, purpose: "project" | "job" = "project"): FolderWarning[] {
  const out: FolderWarning[] = [];
  if (facts.is_filesystem_root) out.push({ tone: "danger", text: "This is / itself: projects refuse it." });
  else if (facts.is_home) out.push({ tone: "danger", text: "This is your home folder: too wide for a project (it would claim every job)." });
  // Home and / are refused outright; whether they are repositories adds nothing.
  if (!facts.git && !facts.is_home && !facts.is_filesystem_root) {
    out.push(
      facts.git_top
        ? { tone: "warning", text: `Inside the repository ${facts.git_top}, not its top folder: worktrees are made from the top.` }
        : { tone: "warning", text: "Not a git repository: jobs here cannot be isolated in worktrees." },
    );
  }
  if (facts.project) {
    const text = facts.project.exact
      ? `Already the project ${facts.project.name}.`
      : `Inside the project ${facts.project.name} (${facts.project.dir}).`;
    out.push({ tone: purpose === "project" ? "danger" : "info", text: purpose === "project" ? `${text} Projects may not overlap.` : `${text} The job joins it.` });
  }
  if (purpose === "project" && facts.contains.length > 0) {
    const names = facts.contains.map((project) => project.name).join(", ");
    out.push({ tone: "danger", text: `Holds the project${facts.contains.length > 1 ? "s" : ""} ${names}: a project here would overlap.` });
  }
  return out;
}

/**
 * True when the picker must not offer the folder at all (R10 O-B2): for a project, every danger
 * warning (/, the home folder, an overlap) is a refusal the server makes too, so Select this folder
 * stays off rather than warning and then letting it through.
 */
export function refusesFolder(facts: FolderFacts, purpose: "project" | "job" = "project"): boolean {
  return purpose === "project" && folderWarnings(facts, purpose).some((warning) => warning.tone === "danger");
}

/** What Land can do with this folder's remote (pull requests are GitHub's only, through gh). */
export function hostNote(facts: FolderFacts): string {
  if (!facts.git) return "";
  if (!facts.host) return "No remote: Land merges locally.";
  if (facts.host === "github") return "GitHub: Land can open pull requests here.";
  const host = facts.host === "other" ? "Not GitHub" : hostLabels[facts.host];
  return `${host}: Land will not open pull requests here; it merges locally.`;
}

// ---- Recent choices -------------------------------------------------------------------------

const RECENT_KEY = "crowsnest-picker-recent";
const RECENT_MAX = 8;

/** The folders chosen lately in this browser, newest first. */
export function recentFolders(): string[] {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(RECENT_KEY) ?? "[]");
    return Array.isArray(parsed) ? parsed.filter((value): value is string => typeof value === "string").slice(0, RECENT_MAX) : [];
  } catch {
    return [];
  }
}

/** Records a chosen folder at the head of the recent list. */
export function rememberFolder(path: string): void {
  try {
    const next = [path, ...recentFolders().filter((value) => value !== path)].slice(0, RECENT_MAX);
    localStorage.setItem(RECENT_KEY, JSON.stringify(next));
  } catch {
    // Private mode or a full store: recents are a convenience.
  }
}

/** A path for display: ~ for the home directory. */
export function tildePath(path: string, home: string | undefined): string {
  if (!home) return path;
  if (path === home) return "~";
  return path.startsWith(`${home}/`) ? `~${path.slice(home.length)}` : path;
}

/**
 * A long path cut in the middle, keeping where it starts and its last two folders (R10, Fable
 * polish: a list cut the tail and kept "/tmp/claude-501/r10/crowsnest-demo-…", which tells one
 * project from another by nothing). Short paths are left whole; the title should carry the rest.
 */
export function middlePath(path: string, max = 36): string {
  if (path.length <= max) return path;
  const parts = path.split("/");
  const tail = parts.slice(-2).join("/");
  const head = path.startsWith("/") ? `/${parts[1] ?? ""}` : (parts[0] ?? "");
  const cut = `${head}/…/${tail}`;
  return cut.length < path.length ? cut : path;
}
