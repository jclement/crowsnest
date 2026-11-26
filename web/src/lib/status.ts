// Display metadata for agent states, shared by StatusDot, StatusChip and the fleet summary.
import type { AgentStatus } from "./types";

export const statusLabels: Record<AgentStatus, string> = {
  blocked: "Blocked",
  working: "Working",
  done: "Done",
  idle: "Idle",
  unknown: "Unknown",
};

/** One-line explanations, used as tooltips. */
export const statusHints: Record<AgentStatus, string> = {
  blocked: "Waiting on a dialog: needs you",
  working: "Running a turn",
  done: "Finished its turn; ready for input",
  idle: "Ready for input",
  unknown: "herdr cannot tell what this agent is doing",
};
