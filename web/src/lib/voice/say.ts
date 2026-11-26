// The SAY line in a reply, for display (DESIGN.md 7.1, 13). The server decides what is spoken
// (internal/voice/say.go); the Chat view only needs to take the `SAY:` line off the top of a
// reply and show its sentence as a small spoken caption above the rest. The rules match the
// server's: only the first non-blank line counts, and `SAY: -` is silent.

export interface SplitReply {
  /** The sentence that was (or would be) spoken; null when there is none or it is silent. */
  spoken: string | null;
  /** The reply without its SAY line: what renders as Markdown. */
  body: string;
}

export function splitSay(reply: string): SplitReply {
  const trimmed = reply.replace(/^\s*\n/, "").trimStart();
  const newline = trimmed.indexOf("\n");
  const first = (newline === -1 ? trimmed : trimmed.slice(0, newline)).trim();
  if (!first.startsWith("SAY:")) return { spoken: null, body: reply };
  const sentence = first.slice(4).replace(/\*\*|__|`/g, "").replace(/\s+/g, " ").trim();
  const body = newline === -1 ? "" : trimmed.slice(newline + 1).replace(/^\s*\n/, "");
  const silent = sentence === "" || sentence === "-" || sentence === "—" || sentence === "–";
  return { spoken: silent ? null : sentence, body };
}
