// The command palette's model (DESIGN.md 12, "Command palette"): what it offers, as plain data,
// and how a query ranks it. Nothing here touches React, the network or the router: items carry a
// PaletteCommand (a description of what to do) and components/palette/run.ts carries it out. That
// keeps item building, ranking, the "Give job" rules and the recents list testable on their own.
//
// Sources are the queries the app already keeps live through the event stream (jobs, open
// questions, the fleet, the orchestrator's status), so the palette never asks the server to search.
import { fuzzyMatch, PERFECT_CHAR_SCORE, queryTokens } from "./fuzzy";
import { relaunchConfirmations } from "./conversation";
import { cwdTail, cleanTitle } from "./fleet";
import { cardLine, isFinished, stateChip, stateLabels, type Job } from "./jobs";
import { compareQuestions, kindLabels, type Question } from "./questions";
import type { ThemeChoice } from "./theme";
import type { AgentStatus, Fleet } from "./types";
import type { ChipTone } from "../components/ui/Chip";

// ---- Commands -------------------------------------------------------------------------------

export type ViewPath = "/" | "/chat" | "/inbox" | "/fleet" | "/terminal";
export type PaletteSheet = "journal" | "notifications" | "shortcuts" | "voice";

/** What running an item does. components/palette/run.ts is the one place that does it. */
export type PaletteCommand =
  | { kind: "navigate"; to: ViewPath; search?: Record<string, string> }
  | { kind: "giveJob"; brief: string; open: boolean }
  | { kind: "jobForm"; brief: string }
  | { kind: "ask"; text: string; open?: boolean }
  | { kind: "jobAction"; id: string; action: "nudge" | "resume" | "cancel" }
  | { kind: "jobMessage"; id: string; text: string }
  | { kind: "answer"; questionId: string; value?: string; label: string }
  | { kind: "poke" }
  | { kind: "relaunch"; action: "restart" | "new" }
  | { kind: "pushTest" }
  | { kind: "theme"; choice: ThemeChoice }
  | { kind: "signOut" }
  | { kind: "sheet"; sheet: PaletteSheet }
  | { kind: "speak"; on: boolean }
  /** Not an effect: the palette asks first, then runs `command`. */
  | { kind: "confirm"; confirm: ConfirmSpec }
  /** Not an effect: the palette asks for a line of text, then runs `command` with it. */
  | { kind: "input"; input: InputSpec };

export interface ConfirmSpec {
  title: string;
  body: string;
  confirmLabel: string;
  cancelLabel: string;
  command: PaletteCommand;
}

export interface InputSpec {
  title: string;
  placeholder: string;
  /** The command the typed text completes. */
  target: { kind: "jobMessage"; id: string };
}

/** The command an input level runs with the text that was typed. */
export function completeInput(input: InputSpec, text: string): PaletteCommand {
  return { kind: "jobMessage", id: input.target.id, text };
}

// ---- Items ----------------------------------------------------------------------------------

export type PaletteGroupId = "recent" | "do" | "needs" | "jobs" | "navigate" | "agents" | "actions";

export const groupLabels: Record<PaletteGroupId, string> = {
  recent: "Recent",
  do: "Do",
  needs: "Needs you",
  jobs: "Jobs",
  navigate: "Go to",
  agents: "Agents and panes",
  actions: "Actions",
};

/** Icons by name, so this file stays free of React; the component maps them to lucide icons. */
export type PaletteIcon =
  | "jobs"
  | "chat"
  | "inbox"
  | "fleet"
  | "terminal"
  | "send"
  | "form"
  | "job"
  | "question"
  | "dialog"
  | "agent"
  | "pane"
  | "poke"
  | "restart"
  | "new"
  | "bell"
  | "journal"
  | "keyboard"
  | "theme-system"
  | "theme-light"
  | "theme-dark"
  | "signout"
  | "speak"
  | "voice";

/** A secondary action of an item: reached with → (or the row's chevron), or ⌘Enter for the first. */
export interface PaletteAction {
  id: string;
  label: string;
  command: PaletteCommand;
  danger?: boolean;
}

export interface PaletteItem {
  /** Stable across refetches: the selection and the recents list follow it. */
  id: string;
  group: PaletteGroupId;
  title: string;
  subtitle?: string;
  /** Searched but not shown: ids, states, agents, directories. */
  keywords?: string[];
  icon: PaletteIcon;
  chip?: { label: string; tone: ChipTone };
  status?: AgentStatus;
  /** Enter. */
  command: PaletteCommand;
  /** A word for what Enter does ("Open", "Give"), shown on the selected row. */
  verb: string;
  actions?: PaletteAction[];
  /** ⌘Enter (Ctrl+Enter): the obvious second thing. Without one, ⌘Enter opens the actions. */
  alt?: PaletteAction;
}

export interface PaletteSources {
  jobs?: Job[];
  /** Open questions. */
  questions?: Question[];
  fleet?: Fleet;
  /** There is an orchestrator to poke, restart or start afresh (it is not disabled). */
  orchestrator: boolean;
  /** Login is on, so Sign out means something. */
  auth: boolean;
  theme: ThemeChoice;
  /** Speak mode, when the voice build that owns it is present: null leaves the action out. */
  speak: { on: boolean } | null;
  /** A voice settings sheet exists to open. */
  voiceSettings: boolean;
}

const views: Array<{ to: ViewPath; label: string; icon: PaletteIcon; keywords: string[]; key: string }> = [
  { to: "/", label: "Jobs", icon: "jobs", keywords: ["board", "home"], key: "j" },
  { to: "/chat", label: "Chat", icon: "chat", keywords: ["orchestrator", "conversation"], key: "c" },
  { to: "/inbox", label: "Inbox", icon: "inbox", keywords: ["questions", "dialogs", "answer"], key: "i" },
  { to: "/fleet", label: "Fleet", icon: "fleet", keywords: ["agents", "workspaces", "panes"], key: "f" },
  { to: "/terminal", label: "Terminal", icon: "terminal", keywords: ["herdr", "tui", "shell"], key: "t" },
];

/** The g-sequence for each view (g j, g c, …), for the shortcuts sheet and lib/hotkeys.ts. */
export const viewKeys: Array<{ key: string; to: ViewPath; label: string }> = views.map(({ key, to, label }) => ({ key, to, label }));

const themeLabels: Record<ThemeChoice, string> = { system: "System", light: "Light", dark: "Dark" };

/** The wording JobDetail's cancel asks with, so cancelling from the palette asks the same. */
export function cancelConfirmation(id: string): ConfirmSpec {
  return {
    title: `Cancel ${id}?`,
    body: "Esc is sent to the agent; its pane stays open.",
    confirmLabel: "Cancel the job",
    cancelLabel: "Keep it",
    command: { kind: "jobAction", id, action: "cancel" },
  };
}

function relaunch(action: "restart" | "new"): PaletteCommand {
  const words = relaunchConfirmations[action];
  return {
    kind: "confirm",
    confirm: { title: words.title, body: words.body, confirmLabel: words.button, cancelLabel: "Cancel", command: { kind: "relaunch", action } },
  };
}

function jobItem(job: Job): PaletteItem {
  const open: PaletteCommand = { kind: "navigate", to: "/", search: { job: job.id } };
  const actions: PaletteAction[] = [{ id: "open", label: "Open job", command: open }];
  if (!isFinished(job.state)) {
    if (job.stuck) actions.push({ id: "resume", label: "Get it moving (retry, restart or nudge)", command: { kind: "jobAction", id: job.id, action: "resume" } });
    actions.push({ id: "nudge", label: "Nudge", command: { kind: "jobAction", id: job.id, action: "nudge" } });
    if (job.pane_id) {
      actions.push({
        id: "message",
        label: "Message its agent…",
        command: { kind: "input", input: { title: `Message ${job.id}`, placeholder: "What should the agent hear?", target: { kind: "jobMessage", id: job.id } } },
      });
    }
    actions.push({ id: "cancel", label: "Cancel job…", danger: true, command: { kind: "confirm", confirm: cancelConfirmation(job.id) } });
  }
  return {
    id: `job:${job.id}`,
    group: "jobs",
    title: job.title || job.id,
    subtitle: [job.id, cardLine(job)].filter(Boolean).join(" · "),
    keywords: [job.id, stateLabels[job.state], job.agent_name, job.dir, job.work_dir, job.role, job.engine, job.stuck ? "stuck" : ""].filter(Boolean),
    icon: "job",
    chip: stateChip(job),
    command: open,
    verb: "Open",
    actions,
  };
}

function questionItem(question: Question): PaletteItem {
  const open: PaletteCommand = { kind: "navigate", to: "/inbox", search: { q: question.id } };
  const actions: PaletteAction[] = [{ id: "open", label: "Open in Inbox", command: open }];
  if ((question.kind === "confirm" || question.kind === "choice") && !question.multi) {
    for (const option of question.options) {
      const recommended = question.recommend === option.key ? " (recommended)" : "";
      actions.push({
        id: `answer:${option.key}`,
        label: `Answer: ${option.label}${recommended}`,
        command: { kind: "answer", questionId: question.id, value: option.key, label: option.label },
      });
    }
  } else if (question.kind === "ack") {
    actions.push({ id: "answer:ack", label: "Acknowledge", command: { kind: "answer", questionId: question.id, label: "Acknowledged" } });
  } else if (question.kind === "notice") {
    actions.push({ id: "answer:dismiss", label: "Dismiss", command: { kind: "answer", questionId: question.id, label: "Dismissed" } });
  }
  return {
    id: `q:${question.id}`,
    group: "needs",
    title: question.title,
    subtitle: [question.id, kindLabels[question.kind], question.job_id ?? question.source_pane ?? ""].filter(Boolean).join(" · "),
    keywords: [question.id, question.kind, question.urgency, question.job_id ?? "", "question"].filter(Boolean),
    icon: "question",
    chip: question.urgency === "blocking" ? { label: "Blocking", tone: "danger" } : question.multi ? { label: "Pick several", tone: "neutral" } : undefined,
    command: open,
    verb: "Open",
    actions,
  };
}

function fleetItems(fleet: Fleet | undefined): PaletteItem[] {
  if (!fleet) return [];
  const items: PaletteItem[] = [];
  for (const agent of fleet.agents) {
    const name = agent.name || agent.pane_id;
    const where = `${agent.workspace} · ${cwdTail(agent.cwd)}`;
    const conversation: PaletteCommand = { kind: "navigate", to: "/fleet", search: { agent: agent.pane_id, view: "conversation" } };
    const terminal: PaletteCommand = { kind: "navigate", to: "/fleet", search: { agent: agent.pane_id, view: "terminal" } };
    if (agent.needs_you) {
      items.push({
        id: `dialog:${agent.pane_id}`,
        group: "needs",
        title: `${name} is waiting at a dialog`,
        subtitle: where,
        keywords: [agent.pane_id, "dialog", "permission", agent.kind],
        icon: "dialog",
        status: agent.status,
        chip: { label: "Dialog", tone: "danger" },
        command: { kind: "navigate", to: "/inbox", search: { q: `dialog:${agent.pane_id}` } },
        verb: "Open",
        actions: [
          { id: "open", label: "Answer in Inbox", command: { kind: "navigate", to: "/inbox", search: { q: `dialog:${agent.pane_id}` } } },
          { id: "terminal", label: "Open its terminal", command: terminal },
        ],
      });
    }
    const actions: PaletteAction[] = [];
    if (agent.has_transcript) actions.push({ id: "conversation", label: "Open conversation", command: conversation });
    actions.push({ id: "terminal", label: "Open terminal", command: terminal });
    items.push({
      id: `agent:${agent.pane_id}`,
      group: "agents",
      title: name,
      subtitle: [agent.kind, where, cleanTitle(agent.title)].filter(Boolean).join(" · "),
      keywords: [agent.pane_id, agent.status, agent.kind, agent.workspace, agent.cwd, "agent"],
      icon: "agent",
      status: agent.status,
      command: agent.has_transcript ? conversation : terminal,
      verb: agent.has_transcript ? "Conversation" : "Terminal",
      actions,
      alt: agent.has_transcript ? actions[1] : undefined,
    });
  }
  for (const workspace of fleet.workspaces)
    for (const tab of workspace.tabs)
      for (const pane of tab.panes) {
        if (pane.agent) continue;
        items.push({
          id: `pane:${pane.id}`,
          group: "agents",
          title: `Shell ${pane.id}`,
          subtitle: `${workspace.label} › ${tab.label} · ${cwdTail(pane.cwd)}`,
          keywords: [pane.id, workspace.label, tab.label, pane.cwd, "pane", "shell"],
          icon: "pane",
          command: { kind: "navigate", to: "/fleet", search: { agent: pane.id } },
          verb: "Terminal",
        });
      }
  return items;
}

/** Everything the palette can offer apart from the query's own Give job and Ask items. */
export function buildItems(sources: PaletteSources): PaletteItem[] {
  const items: PaletteItem[] = [];
  // Needs you, in the Inbox's order: blocking questions, then agents at a dialog, then the rest.
  const open = (sources.questions ?? []).filter((question) => question.status === "open").sort(compareQuestions);
  const blocking = open.filter((question) => question.urgency === "blocking" && question.kind !== "notice");
  items.push(...blocking.map(questionItem));
  items.push(...fleetItems(sources.fleet).filter((item) => item.group === "needs"));
  items.push(...open.filter((question) => !blocking.includes(question)).map(questionItem));
  for (const view of views) {
    items.push({
      id: `nav:${view.to}`,
      group: "navigate",
      title: view.label,
      keywords: view.keywords,
      icon: view.icon,
      command: { kind: "navigate", to: view.to },
      verb: "Go",
    });
  }
  const sheet = (id: PaletteSheet, title: string, icon: PaletteIcon, keywords: string[]): PaletteItem => ({
    id: `sheet:${id}`,
    group: "navigate",
    title,
    keywords,
    icon,
    command: { kind: "sheet", sheet: id },
    verb: "Open",
  });
  items.push(sheet("notifications", "Notifications", "bell", ["push", "devices", "settings"]));
  items.push(sheet("journal", "Journal", "journal", ["pokes", "history", "log"]));
  items.push(sheet("shortcuts", "Keyboard shortcuts", "keyboard", ["help", "keys", "?"]));
  if (sources.voiceSettings) items.push(sheet("voice", "Voice settings", "voice", ["talk", "speak", "elevenlabs", "microphone"]));
  for (const choice of ["system", "light", "dark"] as const) {
    items.push({
      id: `theme:${choice}`,
      group: "navigate",
      title: `Theme: ${themeLabels[choice]}`,
      subtitle: sources.theme === choice ? "Current" : undefined,
      keywords: ["appearance", "colour", "color", "mode"],
      icon: `theme-${choice}`,
      command: { kind: "theme", choice },
      verb: "Switch",
    });
  }
  if (sources.auth) {
    items.push({ id: "signout", group: "navigate", title: "Sign out", keywords: ["log out", "logout"], icon: "signout", command: { kind: "signOut" }, verb: "Sign out" });
  }

  const jobs = [...(sources.jobs ?? [])].sort((a, b) => jobRank(a) - jobRank(b) || (b.last_activity_at ?? b.created_at).localeCompare(a.last_activity_at ?? a.created_at));
  items.push(...jobs.map(jobItem));
  items.push(...fleetItems(sources.fleet).filter((item) => item.group === "agents"));

  if (sources.orchestrator) {
    items.push({ id: "action:poke", group: "actions", title: "Poke now", subtitle: "Poke the orchestrator if anything is due", keywords: ["orchestrator", "wake"], icon: "poke", command: { kind: "poke" }, verb: "Poke" });
    items.push({ id: "action:new", group: "actions", title: "New conversation", subtitle: "Start the orchestrator afresh", keywords: ["orchestrator", "reset", "clear"], icon: "new", command: relaunch("new"), verb: "Start" });
    items.push({ id: "action:restart", group: "actions", title: "Restart orchestrator", subtitle: "Same conversation, Claude started again", keywords: ["orchestrator", "reboot"], icon: "restart", command: relaunch("restart"), verb: "Restart" });
  }
  if (sources.speak) {
    const on = sources.speak.on;
    items.push({
      id: "action:speak",
      group: "actions",
      title: `Speak replies: ${on ? "on" : "off"}`,
      subtitle: on ? "Turn spoken replies off" : "Turn spoken replies on",
      keywords: ["talk", "voice", "audio", "speak mode"],
      icon: "speak",
      command: { kind: "speak", on: !on },
      verb: on ? "Turn off" : "Turn on",
    });
  }
  items.push({ id: "action:pushtest", group: "actions", title: "Send test notification", subtitle: "To every device with notifications on", keywords: ["push", "notify", "bell"], icon: "bell", command: { kind: "pushTest" }, verb: "Send" });
  items.push({ id: "action:jobform", group: "actions", title: "New job form", subtitle: "The full form: directory, role, done when", keywords: ["give", "create", "job"], icon: "form", command: { kind: "jobForm", brief: "" }, verb: "Open" });
  return items;
}

/** Jobs that need Jeff first, then live ones, then finished. */
function jobRank(job: Job): number {
  if (isFinished(job.state)) return 2;
  if (job.stuck || job.state === "needs_you" || job.state === "blocked") return 0;
  return 1;
}

// ---- The query ------------------------------------------------------------------------------

export interface ParsedQuery {
  /** "job" when the query starts with ">" or "job ": only Give job is offered. */
  mode: "all" | "job";
  /** The query without its prefix, trimmed. */
  text: string;
}

export function parseQuery(raw: string): ParsedQuery {
  const leading = raw.trimStart();
  if (leading.startsWith(">")) return { mode: "job", text: leading.slice(1).trim() };
  const prefix = /^job\s+/i.exec(leading);
  if (prefix) return { mode: "job", text: leading.slice(prefix[0].length).trim() };
  return { mode: "all", text: raw.trim() };
}

/** The Give job and Ask items a query makes. */
export function doItems(parsed: ParsedQuery): PaletteItem[] {
  const text = parsed.text;
  if (!text) return [];
  const short = text.length > 80 ? `${text.slice(0, 79)}…` : text;
  const form: PaletteAction = { id: "form", label: "Open the full form (directory, role, done when)…", command: { kind: "jobForm", brief: text } };
  const askAndOpen: PaletteAction = { id: "chat", label: "Send and open Chat", command: { kind: "ask", text, open: true } };
  const items: PaletteItem[] = [
    {
      id: "do:job",
      group: "do",
      title: `Give job: ${short}`,
      subtitle: "To the orchestrator · ⌘Enter for the full form",
      icon: "send",
      command: { kind: "giveJob", brief: text, open: false },
      verb: "Give",
      actions: [form, { id: "open", label: "Give job and open it", command: { kind: "giveJob", brief: text, open: true } }],
      alt: form,
    },
  ];
  if (parsed.mode === "all") {
    items.push({
      id: "do:ask",
      group: "do",
      title: `Send to orchestrator: ${short}`,
      subtitle: "A Chat prompt",
      icon: "chat",
      command: { kind: "ask", text },
      verb: "Send",
      actions: [askAndOpen],
      alt: askAndOpen,
    });
  }
  return items;
}

// ---- Ranking --------------------------------------------------------------------------------

export interface RankedItem {
  item: PaletteItem;
  score: number;
  titlePositions: number[];
  subtitlePositions: number[];
}

export interface PaletteGroup {
  id: PaletteGroupId;
  label: string;
  items: RankedItem[];
}

const GROUP_LIMIT = 8;
const JOB_GROUP_LIMIT = 10;
const RECENT_LIMIT = 5;
const RECENT_BOOST = 8;
/** Field weights: a hit in the title counts in full, elsewhere for less. */
const SUBTITLE_WEIGHT = 0.6;
const KEYWORD_WEIGHT = 0.8;
/**
 * How good the best match must be (score per query character, against a perfect word-start run)
 * for it to come before Give job. Below this the query reads as a new job, not a search.
 */
export const STRONG_MATCH = 0.75;
/** A longer query is more likely to be a sentence: it needs a better match to outrank Give job. */
export const STRONG_MATCH_LONG = 0.9;
/**
 * The least a word of the query must score where it matched (field weight included). Below this
 * it is letters scattered through a subtitle or keyword, which reads as noise rather than a hit;
 * title abbreviations ("kbd" for Keyboard shortcuts) score 0.8 and up.
 */
export const MIN_TOKEN_QUALITY = 0.5;

/** Scores one item: every token must hit the title, the subtitle or a keyword. */
export function scoreItem(tokens: string[], item: PaletteItem): Omit<RankedItem, "item"> | null {
  let score = 0;
  const titlePositions = new Set<number>();
  const subtitlePositions = new Set<number>();
  for (const token of tokens) {
    let best = -Infinity;
    let bestTitle: number[] | null = null;
    let bestSubtitle: number[] | null = null;
    const title = fuzzyMatch(token, item.title);
    if (title && title.score > best) {
      best = title.score;
      bestTitle = title.positions;
      bestSubtitle = null;
    }
    const subtitle = item.subtitle ? fuzzyMatch(token, item.subtitle) : null;
    if (subtitle && subtitle.score * SUBTITLE_WEIGHT > best) {
      best = subtitle.score * SUBTITLE_WEIGHT;
      bestTitle = null;
      bestSubtitle = subtitle.positions;
    }
    for (const keyword of item.keywords ?? []) {
      const hit = fuzzyMatch(token, keyword);
      if (hit && hit.score * KEYWORD_WEIGHT > best) {
        best = hit.score * KEYWORD_WEIGHT;
        bestTitle = null;
        bestSubtitle = null;
      }
    }
    if (best === -Infinity || matchQuality(best, [token]) < MIN_TOKEN_QUALITY) return null;
    score += best;
    for (const position of bestTitle ?? []) titlePositions.add(position);
    for (const position of bestSubtitle ?? []) subtitlePositions.add(position);
  }
  const sorted = (set: Set<number>) => [...set].sort((a, b) => a - b);
  return { score, titlePositions: sorted(titlePositions), subtitlePositions: sorted(subtitlePositions) };
}

/** How good a score is for a query: 1 is a perfect word-start run of every character. */
export function matchQuality(score: number, tokens: string[]): number {
  const length = tokens.reduce((sum, token) => sum + token.length, 0);
  return length === 0 ? 0 : score / (length * PERFECT_CHAR_SCORE);
}

const emptyOrder: PaletteGroupId[] = ["recent", "needs", "navigate", "jobs", "actions"];

/**
 * The groups to show for a query. With no query: recent items, what needs Jeff, the views,
 * jobs that need attention or are running, and the actions. With one: every item that matches,
 * grouped, groups ordered by their best match, and Give job / Send to orchestrator placed first
 * unless something matches strongly (then second, so Enter does the obvious thing either way).
 */
export function rankItems(items: PaletteItem[], rawQuery: string, recents: readonly string[]): PaletteGroup[] {
  const parsed = parseQuery(rawQuery);
  const plain = (item: PaletteItem): RankedItem => ({ item, score: 0, titlePositions: [], subtitlePositions: [] });

  if (parsed.mode === "job") {
    const made = doItems(parsed);
    return made.length > 0 ? [{ id: "do", label: groupLabels.do, items: made.map(plain) }] : [];
  }

  if (!parsed.text) {
    const byId = new Map(items.map((item) => [item.id, item]));
    const recent = recents.map((id) => byId.get(id)).filter((item): item is PaletteItem => !!item).slice(0, RECENT_LIMIT);
    const taken = new Set(recent.map((item) => item.id));
    const groups: PaletteGroup[] = [];
    for (const id of emptyOrder) {
      let members: PaletteItem[];
      if (id === "recent") members = recent;
      else if (id === "jobs") members = items.filter((item) => item.group === "jobs" && !taken.has(item.id) && liveJob(item)).slice(0, 6);
      else if (id === "navigate") members = items.filter((item) => item.group === "navigate" && item.id.startsWith("nav:") && !taken.has(item.id));
      else members = items.filter((item) => item.group === id && !taken.has(item.id)).slice(0, GROUP_LIMIT);
      if (members.length > 0) groups.push({ id, label: groupLabels[id], items: members.map(plain) });
    }
    return groups;
  }

  const tokens = queryTokens(parsed.text);
  const recentSet = new Set(recents);
  const byGroup = new Map<PaletteGroupId, RankedItem[]>();
  for (const [index, item] of items.entries()) {
    const scored = scoreItem(tokens, item);
    if (!scored) continue;
    const ranked: RankedItem = { item, ...scored, score: scored.score + (recentSet.has(item.id) ? RECENT_BOOST : 0) - index * 1e-6 };
    const list = byGroup.get(item.group) ?? [];
    list.push(ranked);
    byGroup.set(item.group, list);
  }
  const groups: PaletteGroup[] = [];
  for (const [id, list] of byGroup) {
    list.sort((a, b) => b.score - a.score);
    groups.push({ id, label: groupLabels[id], items: list.slice(0, id === "jobs" ? JOB_GROUP_LIMIT : GROUP_LIMIT) });
  }
  groups.sort((a, b) => (b.items[0]?.score ?? 0) - (a.items[0]?.score ?? 0));

  const made = doItems(parsed);
  const doGroup: PaletteGroup = { id: "do", label: groupLabels.do, items: made.map(plain) };
  const best = groups[0]?.items[0]?.score ?? 0;
  const threshold = tokens.length >= 3 ? STRONG_MATCH_LONG : STRONG_MATCH;
  const strong = matchQuality(best, tokens) >= threshold;
  if (strong) groups.splice(1, 0, doGroup);
  else groups.unshift(doGroup);
  return groups;
}

function liveJob(item: PaletteItem): boolean {
  const label = item.chip?.label;
  return label !== undefined && !["Done", "Failed", "Cancelled"].includes(label);
}

/** The rows in display order. */
export function flatten(groups: PaletteGroup[]): RankedItem[] {
  return groups.flatMap((group) => group.items);
}

/**
 * Where the selection goes when the rows change under it (live data, a new query): it stays on
 * the same item if that item is still there, else at the same row index (clamped), so a job
 * appearing above it never moves the highlight to another item.
 */
export function keepSelection(rows: readonly { item: { id: string } }[], selectedId: string | null, previousIndex: number): number {
  if (rows.length === 0) return -1;
  if (selectedId) {
    const index = rows.findIndex((row) => row.item.id === selectedId);
    if (index >= 0) return index;
  }
  return Math.min(Math.max(previousIndex, 0), rows.length - 1);
}

// ---- Recents --------------------------------------------------------------------------------

export const RECENTS_KEY = "crowsnest-palette-recent";
const RECENTS_MAX = 12;

/** Items worth remembering: Give job and Ask texts are one-offs, and answers are done once. */
export function rememberable(itemId: string): boolean {
  return !itemId.startsWith("do:") && !itemId.startsWith("q:") && !itemId.startsWith("dialog:");
}

export function pushRecent(recents: readonly string[], itemId: string): string[] {
  if (!rememberable(itemId)) return [...recents];
  return [itemId, ...recents.filter((id) => id !== itemId)].slice(0, RECENTS_MAX);
}

export function loadRecents(storage: Pick<Storage, "getItem"> | undefined = safeStorage()): string[] {
  try {
    const parsed: unknown = JSON.parse(storage?.getItem(RECENTS_KEY) ?? "[]");
    return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === "string").slice(0, RECENTS_MAX) : [];
  } catch {
    return [];
  }
}

export function saveRecents(recents: readonly string[], storage: Pick<Storage, "setItem"> | undefined = safeStorage()): void {
  try {
    storage?.setItem(RECENTS_KEY, JSON.stringify(recents));
  } catch {
    /* storage blocked: recents last for this page only */
  }
}

function safeStorage(): Storage | undefined {
  try {
    return typeof localStorage === "undefined" ? undefined : localStorage;
  } catch {
    return undefined;
  }
}
