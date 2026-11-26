// Fixtures: one realistic fleet and its screens, shared by the unit tests, the `?fixtures=1`
// development mode and the Playwright screenshots. The shapes are the API's (lib/types.ts), so
// a fixture that compiles is a fixture the real server could send.
//
// The fleet is chosen to exercise the UI: one agent blocked on a dialog with a risky option, one
// whose dialog is a multi-question form, agents in every state, plain shell panes, a long cwd
// and a long terminal title. fixtureApi() answers API calls from it in memory, so pressing a
// dialog option in fixtures mode behaves like the real thing (the dialog goes away).
import { ApiError } from "./api";
import type { Agent, AgentScreen, AgentStatus, Fleet, Pane, SendKeysRequest, Tab, Workspace } from "./types";

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

function buildWorkspace(number: number, label: string, tabs: TabSpec[]): Workspace {
  const workspaceId = `w${number}`;
  let paneCounter = 0;
  const builtTabs: Tab[] = tabs.map((tabSpec, tabIndex) => {
    const tabId = `${workspaceId}:t${tabIndex + 1}`;
    const panes: Pane[] = tabSpec.panes.map((paneSpec) => {
      paneCounter += 1;
      const paneId = `${workspaceId}:p${paneCounter}`;
      const status = paneSpec.agent?.status ?? "unknown";
      const agent: Agent | null = paneSpec.agent
        ? {
            name: paneSpec.agent.name,
            agent: paneSpec.agent.kind ?? "claude",
            agent_status: status,
            pane_id: paneId,
            tab_id: tabId,
            workspace_id: workspaceId,
            cwd: paneSpec.cwd,
            terminal_title_stripped: paneSpec.agent.title ?? "",
            agent_session: { value: `5f0c${number}a2e-${paneCounter}b7d-4e1a-9c3f-0d2b8e6f1a${number}${paneCounter}` },
            state_change_seq: 10 * number + paneCounter,
            has_dialog: paneSpec.agent.dialog ?? false,
            has_transcript: paneSpec.agent.transcript ?? (paneSpec.agent.kind ?? "claude") === "claude",
          }
        : null;
      return {
        pane_id: paneId,
        tab_id: tabId,
        workspace_id: workspaceId,
        terminal_id: `term-${number}-${paneCounter}`,
        cwd: paneSpec.cwd,
        foreground_cwd: paneSpec.cwd,
        agent_status: status,
        focused: paneSpec.focused ?? false,
        revision: 100 + paneCounter,
        agent,
      };
    });
    return {
      tab_id: tabId,
      workspace_id: workspaceId,
      label: tabSpec.label,
      number: tabIndex + 1,
      agent_status: rollUp(panes.filter((pane) => pane.agent).map((pane) => pane.agent_status)),
      focused: tabIndex === 0,
      pane_count: panes.length,
      panes,
    };
  });
  return {
    workspace_id: workspaceId,
    label,
    number,
    active_tab_id: builtTabs[0]?.tab_id ?? "",
    agent_status: rollUp(builtTabs.map((tab) => tab.agent_status)),
    focused: number === 1,
    pane_count: builtTabs.reduce((sum, tab) => sum + tab.pane_count, 0),
    tab_count: builtTabs.length,
    tabs: builtTabs,
  };
}

// ---- The fleet ---------------------------------------------------------------

const HOME = "/Users/jsc";

/** A fresh copy of the fixture fleet (tests may mutate what they get). */
export function makeFleet(): Fleet {
  return {
    session: "crowsnest",
    herdr_version: "0.9.5",
    workspaces: [
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
    ],
  };
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

function screen(text: string, status: AgentStatus, hash: string, dialog: AgentScreen["dialog"] = null): AgentScreen {
  return { text, hash, status, dialog, draft: "empty" };
}

/** A fresh copy of the fixture screens, keyed by pane id. */
export function makeScreens(): Record<string, ScreenState> {
  return {
    "w1:p1": { screen: screen(workingScreen, "working", "h-orch-1") },
    "w2:p1": { screen: screen(doneScreen, "done", "h-m3-1") },
    "w2:p3": { screen: screen(idleScreen, "idle", "h-rev-1") },
    "w3:p1": {
      screen: screen(pushDialogScreen, "blocked", "h-push-1", {
        options: [
          { digit: "1", text: "Yes" },
          { digit: "2", text: "Yes, and don't ask again for git push commands in /Users/jsc/Developer/pumpcurves" },
          { digit: "3", text: "No, and tell Claude what to do differently (esc)" },
        ],
        multi_form: false,
      }),
      after: screen(workingScreen.replace("Reviewing M2 fleet cache", "Pushing feature/head-loss"), "working", "h-push-2"),
    },
    "w3:p2": {
      screen: screen(formScreen, "blocked", "h-form-1", {
        options: [
          { digit: "1", text: "The 2019 field data (412 points)" },
          { digit: "2", text: "The synthetic set from the design doc" },
          { digit: "3", text: "Both, tagged separately" },
          { digit: "4", text: "Type something." },
        ],
        multi_form: true,
      }),
    },
    "w4:p1": { screen: screen(unknownScreen, "unknown", "h-probe-1") },
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

function findAgent(paneId: string): Agent | undefined {
  for (const workspace of fleetState.workspaces)
    for (const tab of workspace.tabs) for (const pane of tab.panes) if (pane.pane_id === paneId) return pane.agent ?? undefined;
  return undefined;
}

function notFound(target: string): never {
  throw new ApiError(404, "NOT_FOUND", `No agent ${target} in this session.`);
}

/** Applies a dialog key press to the fixture state, as the server would. */
function pressKeys(target: string, body: SendKeysRequest): { sent: boolean } {
  const state = screenState[target];
  const agent = findAgent(target);
  if (!state || !agent) notFound(target);
  if (body.screen_hash !== state.screen.hash) {
    throw new ApiError(409, "CONFLICT", "The screen changed since you looked. It has been refreshed.");
  }
  if (state.after) {
    state.screen = state.after;
    state.after = undefined;
    agent.agent_status = state.screen.status;
    agent.has_dialog = state.screen.dialog !== null;
    agent.state_change_seq += 1;
  }
  return { sent: true };
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
