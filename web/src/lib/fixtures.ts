// Fixtures: one realistic fleet and its screens, shared by the unit tests, the `?fixtures=1`
// development mode and the Playwright screenshots. The shapes are the API's (lib/types.ts), so
// a fixture that compiles is a fixture the real server could send.
//
// The fleet is chosen to exercise the UI: one agent blocked on a dialog with a risky option, one
// whose dialog is a multi-question form, agents in every state, plain shell panes, a long cwd
// and a long terminal title. fixtureApi() answers API calls from it in memory, so pressing a
// dialog option in fixtures mode behaves like the real thing (the dialog goes away).
import type { AnswerRequest, Question } from "./questions";
import { fixtureQuestions } from "./questionFixtures";
import { makeBoard, makeDetail } from "./jobFixtures";
import { ApiError } from "./api";
import type {
  Agent,
  AgentScreen,
  AgentStatus,
  DialogOption,
  Fleet,
  FleetCounts,
  KeysResponse,
  Pane,
  PaneAgent,
  SendKeysRequest,
  Tab,
  Workspace,
} from "./types";

// ---- Builders ----------------------------------------------------------------

interface AgentSpec {
  name: string;
  kind?: string;
  status: AgentStatus;
  title?: string;
  dialog?: boolean;
  transcript?: boolean;
}

interface PaneSpec {
  cwd: string;
  agent?: AgentSpec;
  focused?: boolean;
}

interface TabSpec {
  label: string;
  panes: PaneSpec[];
}

/** The most urgent status wins when herdr rolls a tab or workspace up. */
function rollUp(statuses: AgentStatus[]): AgentStatus {
  const order: AgentStatus[] = ["blocked", "working", "done", "idle", "unknown"];
  return order.find((status) => statuses.includes(status)) ?? "unknown";
}

function buildAgent(spec: AgentSpec, paneId: string, sessionId: string): PaneAgent {
  const kind = spec.kind ?? "claude";
  const transcript = spec.transcript ?? kind === "claude";
  return {
    name: spec.name,
    target: spec.name || paneId,
    kind,
    status: spec.status,
    ...(transcript ? { session_id: sessionId } : {}),
    title: spec.title ?? "",
    has_dialog: spec.dialog ?? false,
    has_transcript: transcript,
    interactive_ready: spec.status !== "unknown",
  };
}

function buildWorkspace(number: number, label: string, tabs: TabSpec[]): Workspace {
  const workspaceId = `w${number}`;
  let paneCounter = 0;
  const builtTabs: Tab[] = tabs.map((tabSpec, tabIndex) => {
    const panes: Pane[] = tabSpec.panes.map((paneSpec) => {
      paneCounter += 1;
      const paneId = `${workspaceId}:p${paneCounter}`;
      const sessionId = `5f0c${number}a2e-${paneCounter}b7d-4e1a-9c3f-0d2b8e6f1a${number}${paneCounter}`;
      return {
        id: paneId,
        cwd: paneSpec.cwd,
        title: paneSpec.agent?.title ?? "",
        focused: paneSpec.focused ?? false,
        agent_status: paneSpec.agent?.status ?? "unknown",
        agent: paneSpec.agent ? buildAgent(paneSpec.agent, paneId, sessionId) : null,
      };
    });
    return {
      id: `${workspaceId}:t${tabIndex + 1}`,
      number: tabIndex + 1,
      label: tabSpec.label,
      focused: tabIndex === 0,
      agent_status: rollUp(panes.filter((pane) => pane.agent).map((pane) => pane.agent_status)),
      panes,
    };
  });
  return {
    id: workspaceId,
    number,
    label,
    focused: number === 1,
    agent_status: rollUp(builtTabs.map((tab) => tab.agent_status)),
    tabs: builtTabs,
  };
}

/** The flat agent list and counts, derived from the tree exactly as internal/fleet/view.go does. */
function withAgents(session: string, workspaces: Workspace[]): Fleet {
  const agents: Agent[] = [];
  let panes = 0;
  for (const workspace of workspaces)
    for (const tab of workspace.tabs)
      for (const pane of tab.panes) {
        panes += 1;
        if (!pane.agent) continue;
        const needs = pane.agent.status === "blocked" || pane.agent.has_dialog;
        agents.push({
          ...pane.agent,
          pane_id: pane.id,
          tab_id: tab.id,
          workspace_id: workspace.id,
          workspace: workspace.label,
          cwd: pane.cwd,
          needs_you: needs,
        });
      }
  return {
    session,
    herdr_version: "0.9.5",
    revision: 1,
    updated_at: "2026-11-26T03:10:00Z",
    workspaces,
    agents,
    counts: countAgents(agents, panes, workspaces.length),
  };
}

function countAgents(agents: Agent[], panes: number, workspaces: number): FleetCounts {
  const counts: FleetCounts = { agents: agents.length, working: 0, blocked: 0, ready: 0, unknown: 0, dialogs: 0, settled: 0, panes, workspaces };
  for (const agent of agents) {
    if (agent.needs_you) counts.blocked += 1;
    else if (agent.status === "working") counts.working += 1;
    else if (agent.status === "idle" || agent.status === "done") counts.ready += 1;
    else counts.unknown += 1;
    if (agent.has_dialog) counts.dialogs += 1;
    if (agent.status === "done") counts.settled += 1;
  }
  return counts;
}

/** Recomputes the flat list after a fixture changed the tree. */
function refreshAgents(fleet: Fleet): Fleet {
  return { ...withAgents(fleet.session, fleet.workspaces), revision: fleet.revision + 1 };
}

// ---- The fleet ---------------------------------------------------------------

const HOME = "/Users/jsc";

/** A fresh copy of the fixture fleet (tests may mutate what they get). */
export function makeFleet(): Fleet {
  return withAgents("crowsnest", [
      buildWorkspace(1, "orchestrator", [
        {
          label: "lead",
          panes: [
            {
              cwd: `${HOME}/.local/share/crowsnest/orchestrator`,
              agent: { name: "orchestrator", status: "working", title: "✳ Reviewing M2 fleet cache" },
              focused: true,
            },
          ],
        },
      ]),
      buildWorkspace(2, "crowsnest · wip/m3-web", [
        {
          label: "build",
          panes: [
            {
              cwd: `${HOME}/.herdr/worktrees/crowsnest/wip-m3-web`,
              agent: { name: "m3-builder", status: "done", title: "✳ Fleet view: screenshots at 375 and 1440 px" },
            },
            { cwd: `${HOME}/.herdr/worktrees/crowsnest/wip-m3-web/web` },
          ],
        },
        {
          label: "review",
          panes: [
            {
              cwd: `${HOME}/.herdr/worktrees/crowsnest/wip-m3-web`,
              agent: { name: "m3-review", kind: "codex", status: "idle", title: "codex: waiting for input" },
            },
          ],
        },
      ]),
      buildWorkspace(3, "pumpcurves · feature/head-loss", [
        {
          label: "builder",
          panes: [
            {
              cwd: `${HOME}/Developer/pumpcurves/src/Hydraulics/Solvers/HeadLoss`,
              agent: {
                name: "head-loss",
                status: "blocked",
                dialog: true,
                title: "✳ Allow git push to origin feature/head-loss-darcy-weisbach-friction-factor?",
              },
            },
          ],
        },
        {
          label: "tests",
          panes: [
            {
              cwd: `${HOME}/Developer/pumpcurves`,
              agent: { name: "head-loss-tests", status: "blocked", dialog: true, title: "✳ Questions about the fixture set" },
            },
            { cwd: `${HOME}/Developer/pumpcurves` },
          ],
        },
      ]),
      buildWorkspace(4, "scratch", [
        {
          label: "shell",
          panes: [
            { cwd: `${HOME}/tmp`, agent: { name: "probe", kind: "opencode", status: "unknown", transcript: false } },
            { cwd: HOME },
          ],
        },
      ]),
  ]);
}

// ---- Screens -----------------------------------------------------------------

const workingScreen = `● Reading internal/fleet/cache.go (212 lines)
● Bash(go test ./internal/fleet/... -run TestDebounce -count=1)
  ⎿  ok   github.com/jclement/crowsnest/internal/fleet  0.412s

✳ Reviewing M2 fleet cache… (2m 14s · ↓ 3.1k tokens · esc to interrupt)

╭──────────────────────────────────────────────────────────────╮
│ >                                                            │
╰──────────────────────────────────────────────────────────────╯
  ⏵⏵ auto mode on (shift+tab to cycle)`;

const pushDialogScreen = `● Bash(git push -u origin feature/head-loss)

╭──────────────────────────────────────────────────────────────╮
│ Bash command                                                 │
│                                                              │
│   git push -u origin feature/head-loss                       │
│   Push the head-loss branch for review                       │
│                                                              │
│ Do you want to proceed?                                      │
│ ❯ 1. Yes                                                     │
│   2. Yes, and don't ask again for git push commands in       │
│      /Users/jsc/Developer/pumpcurves                         │
│   3. No, and tell Claude what to do differently (esc)        │
│                                                              │
╰──────────────────────────────────────────────────────────────╯
 Enter to confirm · Esc to cancel`;

const formScreen = `☐ Fixtures  ☐ Tolerance  ✔ Submit

Which fixture set should the regression tests use?

❯ 1. The 2019 field data (412 points)
  2. The synthetic set from the design doc
  3. Both, tagged separately
  4. Type something.

 Enter to select · Tab/Arrow keys to navigate · Esc to cancel`;

const doneScreen = `● Screenshots saved under web/screenshots/ (4 files).
  All 38 tests pass; typecheck and lint are clean.

> `;

const idleScreen = `codex  ▸ ready

  Waiting for a task. Type a request, or /help for commands.

▌ `;

const unknownScreen = `jsc@quark ~/tmp % opencode
  opencode v0.4  ·  press ctrl+k for commands`;

interface ScreenState {
  screen: AgentScreen;
  /** What the screen becomes after a key press in fixtures mode. */
  after?: AgentScreen;
}

const noDialog = (): AgentScreen["dialog"] => ({ present: false, options: [], form: false, warnings: [] });

function option(key: string, label: string, extra: Partial<DialogOption> = {}): DialogOption {
  return { key, label, selected: key === "1", ...extra };
}

function screen(target: string, name: string, text: string, status: AgentStatus, hash: string, dialog = noDialog()): AgentScreen {
  return {
    target,
    pane_id: target,
    name,
    kind: "claude",
    status,
    text,
    ansi: text,
    screen_hash: hash,
    revision: 1,
    dialog,
    draft: { input_found: false, present: false, text: "" },
  };
}

/** A fresh copy of the fixture screens, keyed by pane id. */
export function makeScreens(): Record<string, ScreenState> {
  return {
    "w1:p1": { screen: screen("w1:p1", "orchestrator", workingScreen, "working", "h-orch-1") },
    "w2:p1": { screen: screen("w2:p1", "m3-builder", doneScreen, "done", "h-m3-1") },
    "w2:p3": { screen: { ...screen("w2:p3", "m3-review", idleScreen, "idle", "h-rev-1"), kind: "codex" } },
    "w3:p1": {
      screen: screen("w3:p1", "head-loss", pushDialogScreen, "blocked", "h-push-1", {
        present: true,
        options: [
          option("1", "Yes"),
          option("2", "Yes, and don't ask again for git push commands in /Users/jsc/Developer/pumpcurves", {
            warning: "stops asking",
          }),
          option("3", "No, and tell Claude what to do differently (esc)"),
        ],
        form: false,
        warnings: ["stops asking"],
      }),
      after: screen(
        "w3:p1",
        "head-loss",
        workingScreen.replace("Reviewing M2 fleet cache", "Pushing feature/head-loss"),
        "working",
        "h-push-2",
      ),
    },
    "w3:p2": {
      screen: screen("w3:p2", "head-loss-tests", formScreen, "blocked", "h-form-1", {
        present: true,
        options: [
          option("1", "The 2019 field data (412 points)"),
          option("2", "The synthetic set from the design doc"),
          option("3", "Both, tagged separately"),
          option("4", "Type something."),
        ],
        form: true,
        warnings: [],
      }),
    },
    "w4:p1": { screen: { ...screen("w4:p1", "probe", unknownScreen, "unknown", "h-probe-1"), kind: "opencode" } },
  };
}

// ---- The in-memory API -------------------------------------------------------

/** What fixtures mode answers for /auth/me. */
export const fixtureMe = {
  username: "jeff",
  features: { auth: true, voice: false, demo: false, session: "crowsnest" },
};

/** What fixtures mode answers for /health. */
export const fixtureHealth = { status: "ok", version: "dev", update_available: false };

let fleetState = makeFleet();
let screenState = makeScreens();

/** Puts the in-memory fixture server back to its starting state (tests call this). */
export function resetFixtures(): void {
  fleetState = makeFleet();
  screenState = makeScreens();
}

function findAgent(paneId: string): PaneAgent | undefined {
  for (const workspace of fleetState.workspaces)
    for (const tab of workspace.tabs) for (const pane of tab.panes) if (pane.id === paneId) return pane.agent ?? undefined;
  return undefined;
}

function notFound(target: string): never {
  throw new ApiError(404, "NOT_FOUND", `No agent ${target} in this session.`);
}

/** Applies a dialog key press to the fixture state, as the server would. */
function pressKeys(target: string, body: SendKeysRequest): KeysResponse {
  const state = screenState[target];
  const agent = findAgent(target);
  if (!state || !agent) notFound(target);
  if (body.screen_hash !== state.screen.screen_hash) {
    throw new ApiError(409, "CONFLICT", "The screen changed since you looked. It has been refreshed.");
  }
  if (state.after) {
    state.screen = state.after;
    state.after = undefined;
    agent.status = state.screen.status;
    agent.has_dialog = state.screen.dialog.present;
    fleetState = refreshAgents(fleetState);
  }
  return { sent: body.keys, pane_id: target, screen_hash: body.screen_hash };
}

/**
 * Answers an API call from the fixtures, with the same envelope-free result and ApiError
 * failures as lib/api.ts. Unknown paths are a 404 so a missing fixture is loud, not silent.
 */
export async function fixtureApi(path: string, method: string, body: unknown): Promise<unknown> {
  await new Promise((resolve) => setTimeout(resolve, 120)); // feel like a network
  if (path === "/health") return fixtureHealth;
  if (path === "/auth/me") return fixtureMe;
  if (path === "/auth/logout") return { signed_out: true };
  if (path === "/fleet") return structuredClone(fleetState);
  const questionsAnswer = questionFixtureApi(path, method, body);
  if (questionsAnswer !== undefined) return questionsAnswer;
  const jobsAnswer = jobFixtureApi(path);
  if (jobsAnswer !== undefined) return jobsAnswer;
  const match = /^\/agents\/([^/]+)\/(screen|keys)$/.exec(path);
  if (match?.[1] && match[2]) {
    const target = decodeURIComponent(match[1]);
    if (match[2] === "keys" && method === "POST") return pressKeys(target, body as SendKeysRequest);
    const state = screenState[target];
    if (!state) notFound(target);
    return structuredClone(state.screen);
  }
  throw new ApiError(404, "NOT_FOUND", `No fixture for ${method} ${path}.`);
}

// ---- Questions (M6) ----------------------------------------------------------------

const questionState: Question[] = structuredClone(fixtureQuestions);

/** Answers the question routes from fixtureQuestions; undefined for any other path. */
function questionFixtureApi(path: string, method: string, body: unknown): unknown {
  if (path === "/inbox/summary") {
    const open = questionState.filter((question) => question.status === "open");
    return { open: open.length, blocking: open.filter((question) => question.urgency === "blocking").length };
  }
  const list = /^\/questions\?status=(.*)$/.exec(path);
  if (list) {
    const statuses = decodeURIComponent(list[1] ?? "").split(",");
    return questionState
      .filter((question) => statuses.includes("all") || statuses.includes(question.status))
      .map((question) => ({ ...question, files: undefined }));
  }
  const one = /^\/questions\/([^/]+)(\/answer)?$/.exec(path);
  if (!one?.[1]) return undefined;
  const question = questionState.find((candidate) => candidate.id === decodeURIComponent(one[1] ?? ""));
  if (!question) throw new ApiError(404, "NOT_FOUND", "No such question.");
  if (method === "POST" || method === "DELETE") {
    if (question.status !== "open") throw new ApiError(409, "CONFLICT", "This question was already answered, expired or cancelled.");
    const answer = (body ?? {}) as AnswerRequest;
    Object.assign(question, {
      status: method === "POST" ? "answered" : "cancelled",
      answer_value: answer.answer_value ?? null,
      answer_note: answer.answer_note ?? "",
      settled_at: new Date().toISOString(),
      delivery: "type",
    });
  }
  return structuredClone(question);
}

// ---- Jobs (M7) -----------------------------------------------------------------------------

/** Answers the job routes from lib/jobFixtures; undefined for any other path. Read-only. */
function jobFixtureApi(path: string): unknown {
  if (path === "/jobs") return makeBoard();
  if (path === "/roles") return ["builder", "lead", "reviewer", "tester"].map((name) => ({ name, summary: `The ${name} role`, custom: false }));
  if (path === "/dirs") return [{ path: "/src/crowsnest", label: "crowsnest", source: "remembered" }];
  const one = /^\/jobs\/(j_\d+)$/.exec(path);
  if (!one) return undefined;
  const job = makeBoard().find((candidate) => candidate.id === one[1]);
  if (!job) throw new ApiError(404, "NOT_FOUND", "No such job.");
  return { ...makeDetail(), ...job };
}
