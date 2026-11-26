import { describe, expect, it } from "vitest";
import { chatStateOf, mergePage, visibleMessages, type Message, type MessagePage } from "./conversation";

function message(id: string, index: number, seq: number, extra: Partial<Message> = {}): Message {
  return { id, index, seq, kind: "user", text: id, at: "2026-11-26T00:00:00Z", ...extra };
}

function page(messages: Message[], cursor: string, extra: Partial<MessagePage> = {}): MessagePage {
  return { source: "transcript", session_id: "s", cursor, reset: false, earlier: false, more: false, messages, ...extra };
}

describe("mergePage", () => {
  it("takes a reset page as the whole conversation, sorted by index", () => {
    const state = mergePage(undefined, page([message("b", 1, 2), message("a", 0, 1)], "1.2", { reset: true, earlier: true }));
    expect(state.messages.map((m) => m.id)).toEqual(["a", "b"]);
    expect(state.cursor).toBe("1.2");
    expect(state.earlier).toBe(true);
  });

  it("merges changes by id, keeping each message's place", () => {
    const first = mergePage(undefined, page([message("a", 0, 1), message("t", 1, 2, { kind: "tool" })], "1.2", { reset: true }));
    const done = message("t", 1, 4, { kind: "tool", tool: { id: "t", name: "Bash", summary: "ls", input: "", done: true, result: "ok" } });
    const next = mergePage(first, page([message("c", 2, 3), done], "1.4"));
    expect(next.messages.map((m) => m.id)).toEqual(["a", "t", "c"]);
    expect(next.messages[1]!.tool?.result).toBe("ok");
    expect(next.cursor).toBe("1.4");
  });

  it("replaces everything on a reset after a session change", () => {
    const first = mergePage(undefined, page([message("a", 0, 1)], "1.1", { reset: true }));
    const next = mergePage(first, page([message("z", 0, 1)], "2.1", { reset: true, session_id: "new" }));
    expect(next.messages.map((m) => m.id)).toEqual(["z"]);
    expect(next.sessionId).toBe("new");
  });

  it("keeps the same object when nothing changed", () => {
    const first = mergePage(undefined, page([message("a", 0, 1)], "1.1", { reset: true }));
    expect(mergePage(first, page([], "1.1"))).toBe(first);
  });
});

describe("chatStateOf", () => {
  it("maps orchestrator and herdr states", () => {
    expect(chatStateOf("done")).toBe("idle");
    expect(chatStateOf("restarting")).toBe("starting");
    expect(chatStateOf("disabled")).toBe("off");
    expect(chatStateOf("needs_terminal")).toBe("needs_terminal");
    expect(chatStateOf(undefined)).toBe("unknown");
  });
});

describe("visibleMessages", () => {
  it("hides thinking unless asked", () => {
    const messages = [message("a", 0, 1), message("t", 1, 2, { kind: "thinking" })];
    expect(visibleMessages(messages, false).map((m) => m.id)).toEqual(["a"]);
    expect(visibleMessages(messages, true)).toHaveLength(2);
  });
});
