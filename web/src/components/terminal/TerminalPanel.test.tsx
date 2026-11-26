// TerminalPanel's wiring between the key bar, the sticky Ctrl and the terminal, with xterm.js
// replaced by a stub (jsdom has no canvas). The stub records what reaches the "socket" and lets
// the test type as the terminal would and push socket states.
import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { TerminalState } from "../../lib/termSocket";
import { renderWithClient } from "../../test/render";
import { TerminalPanel } from "./TerminalPanel";
import type { XTermHandle } from "./XTerm";

const stub = vi.hoisted(() => ({
  sent: [] as string[],
  reconnects: 0,
  appCursor: false,
  type: (_data: string) => {},
  setState: (_state: TerminalState) => {},
}));

vi.mock("./XTerm", () => ({
  XTerm: (props: {
    label: string;
    handleRef?: (handle: XTermHandle | null) => void;
    transformInput?: (data: string) => string;
    onState?: (state: TerminalState) => void;
  }) => {
    props.handleRef?.({
      send: (data) => stub.sent.push(data),
      paste: (text) => stub.sent.push(`[paste]${text}`),
      focus: () => {},
      reconnect: () => (stub.reconnects += 1),
      applicationCursor: () => stub.appCursor,
    });
    stub.type = (data) => stub.sent.push(props.transformInput ? props.transformInput(data) : data);
    stub.setState = (state) => props.onState?.(state);
    return <div role="region" aria-label={props.label} />;
  },
}));

describe("TerminalPanel", () => {
  beforeEach(() => {
    stub.sent = [];
    stub.reconnects = 0;
    stub.appCursor = false;
    localStorage.clear();
  });

  it("sends bar keys, following the cursor mode", async () => {
    const user = userEvent.setup();
    renderWithClient(<TerminalPanel path="/api/v1/term/tui" label="t" fill="fixed" />);
    await user.click(screen.getByRole("button", { name: "Escape" }));
    await user.click(screen.getByRole("button", { name: "Up" }));
    stub.appCursor = true;
    await user.click(screen.getByRole("button", { name: "Up" }));
    await user.click(screen.getByRole("button", { name: "Enter" }));
    expect(stub.sent).toEqual(["\x1b", "\x1b[A", "\x1bOA", "\r"]);
  });

  it("applies a sticky Ctrl to the next typed key only", async () => {
    const user = userEvent.setup();
    renderWithClient(<TerminalPanel path="/api/v1/term/tui" label="t" fill="fixed" />);
    const ctrl = screen.getByRole("button", { name: /^Control/ });
    await user.click(ctrl);
    expect(ctrl).toHaveAttribute("aria-pressed", "true");
    act(() => stub.type("c"));
    expect(ctrl).toHaveAttribute("aria-pressed", "false");
    act(() => stub.type("c"));
    expect(stub.sent).toEqual(["\x03", "c"]);
  });

  it("lets go of Ctrl when a bar key is pressed instead", async () => {
    const user = userEvent.setup();
    renderWithClient(<TerminalPanel path="/api/v1/term/tui" label="t" fill="fixed" />);
    await user.click(screen.getByRole("button", { name: /^Control/ }));
    await user.click(screen.getByRole("button", { name: "Tab" }));
    act(() => stub.type("x"));
    expect(stub.sent).toEqual(["\t", "x"]);
  });

  it("pastes from the clipboard, and says so when it cannot", async () => {
    const user = userEvent.setup();
    renderWithClient(<TerminalPanel path="/api/v1/term/tui" label="t" fill="fixed" />);
    const readText = vi.fn().mockResolvedValue("git status");
    Object.defineProperty(navigator, "clipboard", { value: { readText }, configurable: true });
    await user.click(screen.getByRole("button", { name: "Paste" }));
    await vi.waitFor(() => expect(stub.sent).toEqual(["[paste]git status"]));

    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
    await user.click(screen.getByRole("button", { name: "Paste" }));
    expect(await screen.findByText("Paste is not available here")).toBeInTheDocument();
  });

  it("shows the connection state and offers Reconnect after an exit", async () => {
    const user = userEvent.setup();
    renderWithClient(<TerminalPanel path="/api/v1/term/tui" label="t" fill="fixed" />);
    expect(screen.getByRole("status")).toHaveTextContent("Connecting…");
    act(() => stub.setState({ status: "open", attempt: 0 }));
    expect(screen.getByRole("status")).toHaveTextContent("Connected");
    expect(screen.queryByRole("button", { name: "Reconnect" })).not.toBeInTheDocument();
    act(() => stub.setState({ status: "reconnecting", attempt: 2, retryAt: Date.now() + 4000 }));
    await vi.waitFor(() => expect(screen.getByRole("status")).toHaveTextContent(/Reconnecting in [34] s/));
    act(() => stub.setState({ status: "exited", reason: "terminal attach taken over", attempt: 0 }));
    expect(screen.getByRole("status")).toHaveTextContent("Ended: terminal attach taken over");
    await user.click(screen.getByRole("button", { name: "Reconnect" }));
    expect(stub.reconnects).toBe(1);
  });

  it("changes and remembers the font size", async () => {
    const user = userEvent.setup();
    renderWithClient(<TerminalPanel path="/api/v1/term/tui" label="t" fill="fixed" />);
    await user.click(screen.getByRole("button", { name: "Larger text" }));
    await user.click(screen.getByRole("button", { name: "Larger text" }));
    expect(localStorage.getItem("crowsnest-terminal-font-size")).toBe("15");
  });
});
