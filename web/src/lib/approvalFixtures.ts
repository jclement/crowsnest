// Approvals for fixtures mode and the unit tests (DESIGN.md 8.7): one of each kind the card
// renders, and a dangerous one.
import type { Approval } from "./approvals";

const minutesAgo = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();

export const fixtureApprovals: Approval[] = [
  {
    id: "ap_1",
    pane_id: "w1:p2",
    agent_name: "builder",
    job_id: "j_3",
    cwd: "/home/crow/src/crowsnest",
    tool_name: "Bash",
    tool_input: { command: "go test ./internal/fleet/...", description: "Run the fleet package tests" },
    suggestions: [],
    risk: "low",
    risk_reasons: [],
    status: "pending",
    created_at: minutesAgo(2),
    expires_at: minutesAgo(-720),
    summary: "Bash: go test ./internal/fleet/...",
  },
  {
    id: "ap_2",
    pane_id: "w2:p1",
    agent_name: "docs-writer",
    cwd: "/home/crow/src/crowsnest",
    tool_name: "Edit",
    tool_input: {
      file_path: "/home/crow/src/crowsnest/internal/fleet/screen.go",
      old_string: "\tif len(lines) > 4 {\n\t\tlines = lines[len(lines)-4:]\n\t}\n",
      new_string: "\tif len(lines) > dialogTailLines {\n\t\tlines = lines[len(lines)-dialogTailLines:]\n\t}\n",
    },
    suggestions: [],
    risk: "low",
    risk_reasons: [],
    status: "pending",
    created_at: minutesAgo(5),
    expires_at: minutesAgo(-720),
    summary: "Edit: /home/crow/src/crowsnest/internal/fleet/screen.go",
  },
  {
    id: "ap_3",
    pane_id: "w1:p3",
    agent_name: "reviewer",
    cwd: "/home/crow/src/crowsnest",
    tool_name: "Bash",
    tool_input: { command: "git push --force origin main", description: "Force-push the rebased branch" },
    suggestions: [],
    risk: "dangerous",
    risk_reasons: ["Force push: rewrites the history of a remote branch", "Pushes to a remote"],
    status: "pending",
    created_at: minutesAgo(1),
    expires_at: minutesAgo(-720),
    summary: "Bash: git push --force origin main",
  },
];

/** Answers /approvals calls in fixtures mode; undefined for any other path. */
export function approvalFixtureApi(state: Approval[], path: string, method: string, body: unknown): unknown {
  if (path.startsWith("/approvals?")) {
    const all = path.includes("status=all");
    return structuredClone(state.filter((approval) => all || approval.status === "pending"));
  }
  const match = /^\/approvals\/([^/]+)(\/decide)?$/.exec(path);
  if (!match?.[1]) return undefined;
  const approval = state.find((entry) => entry.id === decodeURIComponent(match[1] ?? ""));
  if (!approval) return undefined;
  if (match[2] && method === "POST") {
    const request = body as { decision: "allow" | "deny"; note?: string; confirm?: boolean };
    approval.status = request.decision === "allow" ? "allowed" : "denied";
    approval.note = request.note ?? "";
    approval.decided_via = "browser";
    approval.settled_at = new Date().toISOString();
  }
  return structuredClone(approval);
}
