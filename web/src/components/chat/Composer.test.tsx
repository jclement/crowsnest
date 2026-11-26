import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import type { ChatState } from "../../lib/conversation";
import { renderWithClient, stubApi } from "../../test/render";
import { Composer } from "./Composer";

function desktop(matches: boolean) {
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({ matches, addEventListener: () => {}, removeEventListener: () => {} })),
  );
}

const sent = { data: { pane_id: "w1:p1", status: "working", queued: false, started: true } };

describe("Composer", () => {
  it("sends with Enter on a desktop, and Shift+Enter adds a line", async () => {
    desktop(true);
    const user = userEvent.setup();
    const calls = stubApi((call) => (call.method === "POST" ? { body: sent } : { body: { data: {} } }));
    const onSent = vi.fn();
    renderWithClient(<Composer target="orchestrator" agentName="the orchestrator" state="idle" onSent={onSent} />);
    const input = screen.getByRole("textbox", { name: /Message to the orchestrator/ });
    await user.type(input, "first line{Shift>}{Enter}{/Shift}second");
    expect(calls.filter((call) => call.method === "POST")).toHaveLength(0);
    await user.keyboard("{Enter}");
    await waitFor(() => expect(onSent).toHaveBeenCalled());
    const post = calls.find((call) => call.method === "POST");
    expect(post?.path).toBe("/agents/orchestrator/prompt");
    expect(post?.body).toEqual({ text: "first line\nsecond", force_over_draft: false });
    expect(input).toHaveValue("");
  });

  it("on a phone Enter is a new line and the button sends", async () => {
    desktop(false);
    const user = userEvent.setup();
    const calls = stubApi((call) => (call.method === "POST" ? { body: sent } : { body: { data: {} } }));
    renderWithClient(<Composer target="w1:p1" agentName="builder" state="idle" />);
    const input = screen.getByRole("textbox", { name: /Message to builder/ });
    await user.type(input, "hello{Enter}there");
    expect(calls.filter((call) => call.method === "POST")).toHaveLength(0);
    expect(input).toHaveValue("hello\nthere");
    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(calls.filter((call) => call.method === "POST")).toHaveLength(1));
  });

  it("stops at a draft typed at the terminal and appends only when told", async () => {
    desktop(true);
    const user = userEvent.setup();
    const calls = stubApi((call) => {
      if (call.method !== "POST") return { body: { data: {} } };
      const forced = (call.body as { force_over_draft?: boolean }).force_over_draft;
      return forced
        ? { body: sent }
        : { status: 409, body: { error: { code: "DRAFT", message: "draft", detail: { input_found: true, present: true, text: "half a thought" } } } };
    });
    renderWithClient(<Composer target="orchestrator" agentName="the orchestrator" state="idle" />);
    await user.type(screen.getByRole("textbox"), "hello{Enter}");
    expect(await screen.findByRole("alert")).toHaveTextContent("half a thought");
    expect(screen.getByRole("textbox")).toHaveValue("hello");
    await user.click(screen.getByRole("button", { name: "Send anyway" }));
    await waitFor(() => expect(calls.filter((call) => call.method === "POST")).toHaveLength(2));
    expect(calls.filter((call) => call.method === "POST")[1]?.body).toEqual({ text: "hello", force_over_draft: true });
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
  });

  it("says a send while working is queued", async () => {
    desktop(true);
    const user = userEvent.setup();
    stubApi((call) => (call.method === "POST" ? { body: { data: { ...sent.data, queued: true } } } : { body: { data: {} } }));
    function Harness() {
      const [state, setState] = useState<ChatState>("working");
      return (
        <>
          <Composer target="orchestrator" agentName="the orchestrator" state={state} />
          <button onClick={() => setState("idle")}>turn ends</button>
        </>
      );
    }
    renderWithClient(<Harness />);
    expect(screen.getByText(/queued until this turn ends/)).toBeInTheDocument();
    await user.type(screen.getByRole("textbox"), "also this{Enter}");
    expect(await screen.findByText(/Queued: Claude reads it/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "turn ends" }));
    expect(screen.queryByText(/Queued: Claude reads it/)).not.toBeInTheDocument();
  });

  it("is replaced by a notice when the agent is at a dialog", async () => {
    desktop(true);
    const user = userEvent.setup();
    stubApi(() => undefined);
    const open = vi.fn();
    renderWithClient(<Composer target="w1:p1" agentName="builder" state="blocked" onOpenTerminal={open} />);
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("builder is waiting at a dialog");
    await user.click(screen.getByRole("button", { name: "Answer" }));
    expect(open).toHaveBeenCalled();
  });
});
