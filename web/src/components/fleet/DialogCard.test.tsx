import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { findAgent } from "../../lib/fleet";
import { makeFleet, makeScreens } from "../../lib/fixtures";
import type { AgentScreen } from "../../lib/types";
import { renderWithClient, stubApi } from "../../test/render";
import { AgentDetail } from "./AgentDetail";
import { DialogCard } from "./DialogCard";

const pushScreen = makeScreens()["w3:p1"]!.screen;
const formScreen = makeScreens()["w3:p2"]!.screen;

describe("DialogCard", () => {
  it("shows the screen verbatim and one button per option", () => {
    stubApi(() => undefined);
    renderWithClient(<DialogCard target="w3:p1" agentName="head-loss" screen={pushScreen} onOpenTerminal={() => {}} />);
    expect(screen.getByLabelText("Screen text").textContent).toBe(pushScreen.text);
    const options = within(screen.getByRole("list", { name: "Options" })).getAllByRole("button");
    expect(options.map((option) => option.textContent)).toEqual([
      "1Yes",
      expect.stringMatching(/^2Yes, and don't ask again.*Risky/),
      "3No, and tell Claude what to do differently (esc)",
    ]);
  });

  it("puts a warning badge on risky options only", () => {
    stubApi(() => undefined);
    renderWithClient(<DialogCard target="w3:p1" agentName="head-loss" screen={pushScreen} onOpenTerminal={() => {}} />);
    const badges = screen.getAllByText("Risky");
    expect(badges).toHaveLength(1);
    expect(badges[0]!.closest("button")).toHaveTextContent(/^2/);
  });

  it("sends the digit bound to the screen hash", async () => {
    const user = userEvent.setup();
    const calls = stubApi((call) => (call.method === "POST" ? { body: { data: { sent: true } } } : undefined));
    renderWithClient(<DialogCard target="w3:p1" agentName="head-loss" screen={pushScreen} onOpenTerminal={() => {}} />);
    await user.click(screen.getByRole("button", { name: /^1\s*Yes$/ }));
    await waitFor(() => expect(calls.some((call) => call.method === "POST")).toBe(true));
    expect(calls.find((call) => call.method === "POST")).toEqual({
      method: "POST",
      path: "/agents/w3%3Ap1/keys",
      body: { keys: ["1"], screen_hash: "h-push-1" },
    });
    expect(await screen.findByText("Sent “1” to head-loss")).toBeInTheDocument();
  });

  it("badges an option the server marked risky even when no client pattern matches", () => {
    stubApi(() => undefined);
    const marked = {
      ...pushScreen,
      dialog: { ...pushScreen.dialog, options: [{ key: "1", label: "Proceed", selected: true, warning: "bypasses permissions" }] },
    };
    renderWithClient(<DialogCard target="w3:p1" agentName="head-loss" screen={marked} onOpenTerminal={() => {}} />);
    expect(screen.getByText("Risky").closest("button")).toHaveTextContent(/^1Proceed/);
  });

  it("shows the dialog-level warnings the server found", () => {
    stubApi(() => undefined);
    renderWithClient(<DialogCard target="w3:p1" agentName="head-loss" screen={pushScreen} onOpenTerminal={() => {}} />);
    expect(screen.getByText(/mentions something risky: stops asking/)).toBeInTheDocument();
  });

  it("offers only the terminal for a multi-question form", async () => {
    const user = userEvent.setup();
    stubApi(() => undefined);
    let opened = false;
    renderWithClient(
      <DialogCard target="w3:p2" agentName="head-loss-tests" screen={formScreen} onOpenTerminal={() => (opened = true)} />,
    );
    expect(screen.queryByRole("list", { name: "Options" })).not.toBeInTheDocument();
    expect(screen.getByText(/form with several questions/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Open terminal" }));
    expect(opened).toBe(true);
  });
});

describe("DialogCard conflict", () => {
  it("on 409 sends nothing more, refreshes the screen and says so", async () => {
    const user = userEvent.setup();
    const changed: AgentScreen = {
      ...pushScreen,
      screen_hash: "h-push-9",
      text: pushScreen.text.replace("git push -u origin feature/head-loss", "git push origin main"),
    };
    let current = pushScreen;
    const calls = stubApi((call) => {
      if (call.path === "/agents/w3%3Ap1/screen") return { body: { data: current } };
      if (call.path === "/agents/w3%3Ap1/keys") {
        current = changed; // the screen moved on before the press arrived
        return { status: 409, body: { error: { code: "CONFLICT", message: "The screen changed." } } };
      }
      return undefined;
    });
    const found = findAgent(makeFleet(), "w3:p1")!;
    renderWithClient(<AgentDetail agent={found.agent} workspace={found.workspace} tab={found.tab} />);

    await user.click(await screen.findByRole("button", { name: /^1\s*Yes$/ }));
    expect(await screen.findByText(/The screen changed before your answer arrived/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByLabelText("Screen text")).toHaveTextContent("git push origin main"));
    expect(calls.filter((call) => call.method === "POST")).toHaveLength(1);
    expect(screen.queryByText(/Sent “1”/)).not.toBeInTheDocument();

    // The next press quotes the new hash.
    await user.click(screen.getByRole("button", { name: /^1\s*Yes$/ }));
    await waitFor(() => expect(calls.filter((call) => call.method === "POST")).toHaveLength(2));
    expect(calls.filter((call) => call.method === "POST")[1]!.body).toEqual({ keys: ["1"], screen_hash: "h-push-9" });
  });
});
