import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { herdrMissing, onboarding, report } from "../components/system/fixtures.test-data";
import { renderWithClient, stubApi } from "../test/render";
import { Checklist } from "./WelcomePage";

describe("Checklist", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("all green: Start finishes the first run", async () => {
    const calls = stubApi((call) => (call.path === "/onboarding/start" ? { body: { data: onboarding({ pending: false }) } } : undefined));
    const onStarted = vi.fn();
    const user = userEvent.setup();
    renderWithClient(<Checklist view={onboarding()} rechecking={false} onRecheck={() => {}} onStarted={onStarted} />);
    expect(screen.getByText(/Everything it needs is here/)).toBeInTheDocument();
    expect(screen.getByText("The orchestrator's folder")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Start" }));
    await vi.waitFor(() => expect(onStarted).toHaveBeenCalled());
    expect(calls).toContainEqual({ method: "POST", path: "/onboarding/start", body: {} });
  });

  it("herdr missing: shows the install command and holds Start", () => {
    stubApi(() => undefined);
    renderWithClient(<Checklist view={onboarding({ report: herdrMissing() })} rechecking={false} onRecheck={() => {}} onStarted={() => {}} />);
    expect(screen.getByText("brew install herdr")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start" })).toBeDisabled();
    expect(screen.getByText("Start needs herdr and one agent.")).toBeInTheDocument();
  });

  it("Claude not signed in: offers a terminal with the sign-in command", async () => {
    const base = report();
    const signedOut = {
      ...base,
      checks: base.checks.map((check) => (check.id === "agent.claude" ? { ...check, status: "fail" as const, detail: "not signed in", fix: "Sign in once", command: "claude" } : check)),
    };
    const calls = stubApi((call) => (call.path === "/onboarding/shell" ? { status: 503, body: { error: { code: "UNAVAILABLE", message: "Could not open a shell" } } } : undefined));
    const user = userEvent.setup();
    renderWithClient(<Checklist view={onboarding({ report: signedOut })} rechecking={false} onRecheck={() => {}} onStarted={() => {}} />);
    await user.click(screen.getByRole("button", { name: "Open a terminal to sign in" }));
    expect(calls).toContainEqual({ method: "POST", path: "/onboarding/shell", body: { command: "claude" } });
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not open a shell");
  });

  it("both agents: asks which runs the orchestrator, lists Codex's gaps, and sends the choice", async () => {
    const calls = stubApi((call) => (call.path === "/onboarding/start" ? { body: { data: onboarding({ pending: false }) } } : undefined));
    const user = userEvent.setup();
    renderWithClient(
      <Checklist view={onboarding({ choose_engine: true, codex_gaps: ["No stop-hook guarantee"] })} rechecking={false} onRecheck={() => {}} onStarted={() => {}} />,
    );
    expect(screen.queryByText("What Codex mode lacks")).not.toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: /Codex/ }));
    expect(screen.getByText("What Codex mode lacks")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Start" }));
    await vi.waitFor(() => expect(calls).toContainEqual({ method: "POST", path: "/onboarding/start", body: { engine: "codex" } }));
  });
});
