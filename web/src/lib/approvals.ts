// Tool-call approvals (DESIGN.md 8.7): the TypeScript mirror of internal/approvals in its JSON
// spelling, and the pure helpers the cards and the tray need: what a call is in a line, how risky
// it is in words, and an Edit/Write/MultiEdit call as diff files for DiffView.
import type { QuestionFile } from "./questions";

export type ApprovalStatus = "pending" | "allowed" | "denied" | "expired" | "superseded";
export type Risk = "low" | "elevated" | "dangerous";

export interface Approval {
  id: string;
  job_id?: string;
  pane_id?: string;
  agent_name?: string;
  session_id?: string;
  cwd?: string;
  tool_name: string;
  tool_input: Record<string, unknown>;
  suggestions: unknown[];
  risk: Risk;
  risk_reasons: string[];
  status: ApprovalStatus;
  decided_via?: string;
  note?: string;
  status_reason?: string;
  /** The policy rules that allowed it, when decided_via is "policy" (8.7 Policy). */
  rule_ids?: string[];
  /** What "Allow and remember for this job" proposes; only on a pending, non-dangerous call of a live job. */
  remember?: { kind: "bash" | "edit" | "tool"; pattern: string; exact?: boolean };
  created_at: string;
  settled_at?: string | null;
  expires_at: string;
  summary: string;
}

/** The body of POST /approvals/{id}/decide. */
export interface DecideRequest {
  decision: "allow" | "deny";
  note?: string;
  /** The explicit second confirmation a dangerous allow needs. */
  confirm?: boolean;
  /** "Allow and remember for this job": also store a job rule with this pattern. */
  remember?: { pattern: string };
}

/** The data of an `approval` SSE event. */
export interface ApprovalEvent {
  id: string;
  status: ApprovalStatus;
  risk: Risk;
  tool: string;
  job_id?: string;
  pane_id?: string;
  change: "requested" | "allowed" | "denied" | "expired" | "superseded";
}

export const riskLabels: Record<Risk, string> = { low: "Low risk", elevated: "Elevated", dangerous: "Dangerous" };

export const riskRank: Record<Risk, number> = { low: 0, elevated: 1, dangerous: 2 };

/** Who is asking, in words. */
export function requester(approval: Approval): string {
  return approval.agent_name || (approval.job_id ? `Job ${approval.job_id}` : approval.pane_id ? `Pane ${approval.pane_id}` : "An agent");
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/** The plain-language risk line under a call: the reasons, or what a low one does. */
export function riskLine(approval: Approval): string {
  if (approval.risk_reasons.length > 0) return approval.risk_reasons.join(". ") + ".";
  switch (toolKind(approval.tool_name)) {
    case "bash":
      return approval.cwd ? `Runs a shell command in ${approval.cwd}. No risky pattern recognised.` : "Runs a shell command. No risky pattern recognised.";
    case "edit":
      return "Changes a file inside the job's directory.";
    case "fetch":
      return "Reads a web page.";
    case "mcp":
      return "Calls a tool on an MCP server.";
    default:
      return "No risky pattern recognised.";
  }
}

export type ToolKind = "bash" | "edit" | "fetch" | "mcp" | "other";

export function toolKind(tool: string): ToolKind {
  if (tool === "Bash") return "bash";
  if (tool === "Edit" || tool === "Write" || tool === "MultiEdit" || tool === "NotebookEdit") return "edit";
  if (tool === "WebFetch") return "fetch";
  if (tool.startsWith("mcp__")) return "mcp";
  return "other";
}

/** An MCP tool name split into its server and tool ("mcp__github__create_issue"). */
export function mcpParts(tool: string): { server: string; tool: string } {
  const rest = tool.slice("mcp__".length);
  const cut = rest.indexOf("__");
  return cut < 0 ? { server: rest, tool: "" } : { server: rest.slice(0, cut), tool: rest.slice(cut + 2) };
}

/** The command of a Bash call. */
export function bashCommand(approval: Approval): string {
  return text(approval.tool_input.command);
}

/**
 * An edit as diff files. Edit shows old → new strings; MultiEdit each edit in turn; Write the new
 * content against nothing (crowsnest does not read the file on the browser's behalf).
 */
export function editFiles(approval: Approval): QuestionFile[] {
  const input = approval.tool_input;
  const filename = text(input.file_path) || text(input.notebook_path) || "file";
  switch (approval.tool_name) {
    case "Edit":
      return [{ filename, original: text(input.old_string), final: text(input.new_string) }];
    case "MultiEdit": {
      const edits = Array.isArray(input.edits) ? (input.edits as Record<string, unknown>[]) : [];
      return edits.map((edit, index) => ({ filename: `${filename} (edit ${index + 1} of ${edits.length})`, original: text(edit.old_string), final: text(edit.new_string) }));
    }
    case "Write":
      return [{ filename, original: "", final: text(input.content) }];
    default:
      return [{ filename, original: "", final: text(input.new_source) || JSON.stringify(input, null, 2) }];
  }
}

/** The input as indented JSON, for MCP arguments and unknown tools. */
export function prettyInput(input: unknown): string {
  try {
    return JSON.stringify(input, null, 2);
  } catch {
    return String(input);
  }
}

/** Newest first among the same risk, most dangerous first overall. */
export function compareApprovals(a: Approval, b: Approval): number {
  return riskRank[b.risk] - riskRank[a.risk] || b.created_at.localeCompare(a.created_at);
}

/** What happened to a settled approval, in words. */
export function outcome(approval: Approval): string {
  switch (approval.status) {
    case "allowed":
      if (approval.decided_via === "policy") return approval.status_reason || "Allowed by a rule";
      if (approval.decided_via === "notification") return "Allowed from a notification";
      return approval.status_reason || "Allowed";
    case "denied":
      return approval.note ? `Denied: ${approval.note}` : "Denied";
    case "expired":
      return "Expired with no answer";
    case "superseded":
      return approval.status_reason || "The agent moved on";
    default:
      return "Waiting";
  }
}
