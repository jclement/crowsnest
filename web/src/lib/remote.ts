// Remote Control on a standing agent (DESIGN.md 7.10): what "Open in Claude" shows for the
// orchestrator or a lead. A link only when the server captured a current session URL of the
// exact shape https://claude.ai/code/session_<id> (never built from a name); a muted fallback
// line when Remote Control was asked for but no link is known; nothing when it is off.

/** The Remote Control fields of an orchestrator or lead status. */
export interface RemoteControlFields {
  /** The Remote Control session name Claude was started with; empty when it is off. */
  remote_control?: string;
  /** "active", or a sentence saying it did not come up and why it may not have. */
  remote_control_note?: string;
  /** The session's https://claude.ai/code/session_… link, once Claude has shown it. */
  remote_control_url?: string;
}

export type RemoteControlView =
  | { kind: "link"; name: string; url: string }
  | { kind: "fallback"; name: string; reason?: string }
  | { kind: "off" };

/** The same strict shape the server accepts (internal/orch ValidRemoteControlURL). */
const sessionURL = /^https:\/\/claude\.ai\/code\/session_[A-Za-z0-9]{20,64}$/;

export function remoteControlView(status: RemoteControlFields | undefined): RemoteControlView {
  const name = status?.remote_control?.trim() ?? "";
  if (!name) return { kind: "off" };
  const url = status?.remote_control_url ?? "";
  if (sessionURL.test(url)) return { kind: "link", name, url };
  const note = status?.remote_control_note?.trim();
  return { kind: "fallback", name, reason: note && note !== "active" ? note : undefined };
}

/** The help table: which surface is for what (R4 Fable 3.7, short form). */
export const surfaceRows: { aspect: string; claude: string; crowsnest: string }[] = [
  { aspect: "Sees", claude: "One agent's conversation", crowsnest: "Every project, job, question, approval and spend" },
  { aspect: "Good at", claude: "A long back-and-forth with one agent; native app and notifications", crowsnest: "The queue across projects, supervision, approvals, landing" },
  { aspect: "Blind to", claude: "Jobs, other projects, the supervisor", crowsnest: "Nothing in the session, but its chat is a plainer client" },
  { aspect: "Reach", claude: "Anywhere", crowsnest: "Your tailnet" },
];
