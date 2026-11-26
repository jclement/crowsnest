import { act, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Message } from "../../lib/conversation";
import { queryKeys } from "../../lib/api";
import { renderWithClient, stubApi } from "../../test/render";
import { ConversationView } from "./ConversationView";

function msg(id: string, index: number, extra: Partial<Message>): Message {
  return { id, index, seq: index + 1, at: "2026-11-26T00:00:00Z", kind: "user", ...extra };
}

const initial: Message[] = [
  msg("u1", 0, { kind: "user", text: "status?" }),
  msg("p1", 1, { kind: "user", tag: "poke", text: "[crowsnest poke 2026-11-26T03:10Z talk=off]\n- builder settled" }),
  msg("th", 2, { kind: "thinking", text: "secret reasoning" }),
  msg("t1", 3, { kind: "tool", tool: { id: "t1", name: "Bash", summary: "ls", input: "", done: true, result: "a.go" } }),
  msg("a1", 4, { kind: "assistant", text: "All **good**:\n\n| a | b |\n|---|---|\n| 1 | 2 |" }),
];

/** Serves the initial page, then whatever `later` holds for requests after the first cursor. */
function serve(later: { messages: Message[] }) {
  return stubApi((call) => {
    if (!call.path.startsWith("/agents/orchestrator/messages")) return { body: { data: {} } };
    if (!call.path.includes("after=")) {
      return { body: { data: { source: "transcript", session_id: "s", cursor: "1.5", reset: true, earlier: false, more: false, messages: initial } } };
    }
    const messages = later.messages;
    later.messages = [];
    const cursor = `1.${5 + messages.length}`;
    return { body: { data: { source: "transcript", session_id: "s", cursor, reset: false, earlier: false, more: false, messages } } };
  });
}

function desktop() {
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: true, addEventListener: () => {}, removeEventListener: () => {} })));
}

/** Gives the scroll container real-looking geometry (jsdom lays nothing out). */
function geometry(container: HTMLElement, sizes: { scrollHeight: number; clientHeight: number }) {
  Object.defineProperty(container, "scrollHeight", { configurable: true, get: () => sizes.scrollHeight });
  Object.defineProperty(container, "clientHeight", { configurable: true, get: () => sizes.clientHeight });
}

describe("ConversationView", () => {
  it("renders the conversation: tags, tool cards, markdown; thinking hidden", async () => {
    desktop();
    serve({ messages: [] });
    renderWithClient(<ConversationView target="orchestrator" agentName="the orchestrator" state="idle" variant="embedded" />);
    const list = await screen.findByRole("list", { name: "Conversation" });
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(4);
    expect(within(items[1]!).getByText("poke")).toBeInTheDocument();
    expect(within(items[2]!).getByRole("button", { name: /Bash/ })).toBeInTheDocument();
    expect(within(items[3]!).getByRole("table")).toBeInTheDocument();
    expect(screen.queryByText("secret reasoning")).not.toBeInTheDocument();
  });

  it("shows thinking when asked", async () => {
    desktop();
    serve({ messages: [] });
    renderWithClient(<ConversationView target="orchestrator" agentName="the orchestrator" state="idle" variant="embedded" showThinking />);
    expect(await screen.findByRole("button", { name: /Thinking/ })).toBeInTheDocument();
  });

  it("follows new messages while at the bottom", async () => {
    desktop();
    const later = { messages: [] as Message[] };
    serve(later);
    const { container, queryClient } = renderWithClient(
      <ConversationView target="orchestrator" agentName="the orchestrator" state="idle" variant="embedded" />,
    );
    await screen.findByRole("list", { name: "Conversation" });
    const scroller = container.querySelector<HTMLElement>("[data-scroll-container]")!;
    const sizes = { scrollHeight: 1000, clientHeight: 400 };
    geometry(scroller, sizes);
    scroller.scrollTop = 600; // at the bottom
    act(() => void scroller.dispatchEvent(new Event("scroll")));

    later.messages = [msg("a2", 5, { kind: "assistant", text: "newer" })];
    sizes.scrollHeight = 1300;
    await act(() => queryClient.invalidateQueries({ queryKey: queryKeys.agents }));
    await screen.findByText("newer");
    expect(scroller.scrollTop).toBe(1300);
    expect(screen.queryByRole("button", { name: /new message/ })).not.toBeInTheDocument();
  });

  it("does not yank a reader who scrolled up; offers a pill instead", async () => {
    desktop();
    const later = { messages: [] as Message[] };
    serve(later);
    const { container, queryClient } = renderWithClient(
      <ConversationView target="orchestrator" agentName="the orchestrator" state="idle" variant="embedded" />,
    );
    await screen.findByRole("list", { name: "Conversation" });
    const scroller = container.querySelector<HTMLElement>("[data-scroll-container]")!;
    const sizes = { scrollHeight: 1000, clientHeight: 400 };
    geometry(scroller, sizes);
    scroller.scrollTop = 100; // reading older messages
    act(() => void scroller.dispatchEvent(new Event("scroll")));

    later.messages = [msg("a2", 5, { kind: "assistant", text: "one" }), msg("a3", 6, { kind: "assistant", text: "two" })];
    sizes.scrollHeight = 1400;
    await act(() => queryClient.invalidateQueries({ queryKey: queryKeys.agents }));
    await screen.findByText("two");
    expect(scroller.scrollTop).toBe(100);
    const pill = screen.getByRole("button", { name: "2 new messages" });
    act(() => pill.click());
    expect(scroller.scrollTop).toBe(1400);
    await waitFor(() => expect(screen.queryByRole("button", { name: /new message/ })).not.toBeInTheDocument());
  });

  it("shows the empty state, not pane text, for a Claude conversation with no transcript yet", async () => {
    desktop();
    stubApi((call) =>
      call.path.startsWith("/agents/orchestrator/messages")
        ? {
            body: {
              data: {
                source: "pane", session_id: "fresh", cursor: "3.1", reset: true, earlier: false, more: false,
                messages: [msg("pane", 0, { kind: "pane", text: "Claude Code v2.1.332" })],
              },
            },
          }
        : { body: { data: {} } },
    );
    const { unmount } = renderWithClient(
      <ConversationView target="orchestrator" agentName="the orchestrator" state="idle" variant="page" paneFallback={false} />,
    );
    expect(await screen.findByText("No messages yet")).toBeInTheDocument();
    expect(screen.queryByText(/Claude Code v2/)).not.toBeInTheDocument();
    unmount();
    renderWithClient(<ConversationView target="orchestrator" agentName="the orchestrator" state="idle" variant="page" />);
    expect(await screen.findByText(/Claude Code v2/)).toBeInTheDocument();
  });
});
