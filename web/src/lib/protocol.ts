// crowsnest's protocol text, kept off the screen where a person's words belong (R10 Fable "agent
// protocol text leaks into the decision screen", Opus F18). Two shapes reach the browser:
//
//   - an agent reply's first line "SAY: <sentence>" (the spoken line, DESIGN.md 13): in Chat it is
//     a small caption (lib/voice/say.ts splitSay); anywhere else a reply is quoted, it goes;
//   - the headers crowsnest types in front of what it sends an agent, "[crowsnest <kind> <args>] …"
//     (job, message, answer, check, review, nudge, poke, upkeep, restart, voice: the shapes in real
//     transcripts under ~/.claude/projects). A user message that starts with one is crowsnest's
//     own; an agent that quotes one ("On **[crowsnest job j_10] You hold job j_10 …**") gets the
//     bracket removed.
//
// Pure: Chat's MessageItem, the decision packet's last message and a job's last message read it.
import { splitSay } from "./voice/say";

/** "[crowsnest job j_10]", "[crowsnest answer q_7]", "[crowsnest poke 2026-11-28T10:00:00Z talk=off]". */
const HEADER = /^\s*\[crowsnest ([a-z]+)(?:[ :]([^\]\n]*))?\]\s*/;
/** The same header anywhere in a line, for text that quotes one. */
const INLINE_HEADER = /\[crowsnest [a-z]+(?:[ :][^\]\n]*)?\]\s*/g;

/** A user message crowsnest typed: what kind, its argument (a job or question id), and the rest. */
export interface CrowsnestHeader {
  kind: string;
  /** The header's argument: "j_10", "q_7", a time; "" when it has none. */
  ref: string;
  /** The text after the header. */
  rest: string;
}

/** The header a message starts with, or null for a message a person typed. */
export function crowsnestHeader(text: string): CrowsnestHeader | null {
  const match = HEADER.exec(text);
  if (!match) return null;
  return { kind: match[1] ?? "", ref: (match[2] ?? "").trim(), rest: text.slice(match[0].length) };
}

/** Text with every quoted "[crowsnest …]" header taken out. */
export function withoutHeaders(text: string): string {
  return text.replace(INLINE_HEADER, "");
}

/**
 * An agent's reply as quoted outside Chat (the decision packet's "last message", a job's): the
 * SAY line dropped (its sentence kept when it is all there is) and quoted headers removed.
 */
export function quotedReply(text: string): string {
  const { spoken, body } = splitSay(text);
  const kept = body.trim() ? body : (spoken ?? "");
  return withoutHeaders(kept).trim();
}

/**
 * The one line Chat shows for a message crowsnest typed into an agent's conversation, in place of
 * the protocol text ("Job j_10 handed to this agent"), or null when the message is the human's own
 * words and should stay a bubble (a job message, an answer, a voice prompt).
 */
export function systemLine(header: CrowsnestHeader): string | null {
  const job = header.ref.match(/j_\d+/)?.[0];
  switch (header.kind) {
    case "job":
      return job ? `Job ${job} handed to this agent` : "A job handed to this agent";
    case "check":
      return "crowsnest's check result sent";
    case "review":
      return job ? `Review comments on ${job} sent` : "Review comments sent";
    case "nudge":
      return "Nudged: asked to say where the job stands";
    case "restart":
      return "Restarted on the same conversation";
    case "upkeep":
      return "Asked to write a hand-over before a fresh start";
    case "message":
    case "answer":
    case "voice":
    case "poke":
      return null;
    default:
      return `crowsnest: ${header.kind}`;
  }
}
