import { screen, waitFor } from "@testing-library/react";
import { makePeriod, makeReport } from "../../lib/spendFixtures";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { contextLine, contextPercent, formatTokens, nextDueText } from "../../lib/schedule";
import { fixtureContext, fixtureSchedule } from "../../lib/scheduleFixtures";
import { renderWithClient, stubApi } from "../../test/render";
import { ChatMenu, ContextUsage } from "./ChatMenu";

afterEach(() => vi.unstubAllGlobals());

describe("context usage", () => {
  it("formats sizes and fills the bar against the threshold", () => {
    expect(formatTokens(950)).toBe("950");
    expect(formatTokens(4_250)).toBe("4.3k");
    expect(formatTokens(128_400)).toBe("128k");
    expect(contextPercent(fixtureContext)).toBe(86);
    expect(contextPercent({ ...fixtureContext, threshold: 0, tokens: 100_000 })).toBe(50);
    expect(contextPercent({ ...fixtureContext, tokens: 900_000 })).toBe(100);
    expect(contextPercent({ ...fixtureContext, known: false })).toBeNull();
    expect(contextLine(fixtureContext)).toBe("128k tokens · compacts at 150k");
    expect(contextLine({ ...fixtureContext, threshold: 0, known: false })).toBe("size not known yet · auto-compaction off");
  });

  it("draws an amber meter past four fifths", () => {
    renderWithClient(<ContextUsage info={fixtureContext} />);
    const meter = screen.getByRole("meter", { name: "Context used" });
    expect(meter).toHaveAttribute("aria-valuenow", "86");
    expect(meter.firstElementChild).toHaveClass("bg-warning");
    expect(screen.getByText("Context: 128k tokens · compacts at 150k")).toBeInTheDocument();
  });

  it("says when a task runs next", () => {
    const [check, tidy] = fixtureSchedule.tasks;
    const relative = () => "in 6 min";
    expect(nextDueText(check!, relative)).toBe("in 6 min");
    expect(nextDueText(tidy!, relative)).toBe("off");
    expect(nextDueText({ ...check!, next_due_at: null, every: "poke" }, relative)).toBe("with the next poke");
    expect(nextDueText({ ...check!, run_now: true }, relative)).toBe("now (Run now)");
  });
});

describe("ChatMenu", () => {
  it("shows the context size and compacts on request", async () => {
    const calls = stubApi((call) => {
      if (call.path === "/orchestrator/context") return { body: { data: fixtureContext } };
      if (call.path === "/orchestrator/compact") return { body: { data: { ...fixtureContext, last_compact_at: new Date().toISOString() } } };
      if (call.path.startsWith("/journal")) return { body: { data: { entries: [], total: 0, next_before: 0 } } };
      return undefined;
    });
    renderWithClient(<ChatMenu showThinking={false} onToggleThinking={() => {}} canRelaunch />);
    await userEvent.click(screen.getByRole("button", { name: "Conversation menu" }));
    expect(await screen.findByText("Context: 128k tokens · compacts at 150k")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("menuitem", { name: "Compact now" }));
    expect(await screen.findByText("Compacting the orchestrator")).toBeInTheDocument();
    expect(calls.some((call) => call.method === "POST" && call.path === "/orchestrator/compact")).toBe(true);
  });

  it("says why a compaction was refused", async () => {
    stubApi((call) => {
      if (call.path === "/orchestrator/context") return { body: { data: fixtureContext } };
      if (call.path === "/orchestrator/compact") return { status: 409, body: { error: { code: "CONFLICT", message: "The orchestrator is busy" } } };
      return { body: { data: { entries: [], total: 0, next_before: 0 } } };
    });
    renderWithClient(<ChatMenu showThinking={false} onToggleThinking={() => {}} canRelaunch />);
    await userEvent.click(screen.getByRole("button", { name: "Conversation menu" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Compact now" }));
    await waitFor(() => expect(screen.getByText("Not compacted")).toBeInTheDocument());
    expect(screen.getByText("The orchestrator is busy")).toBeInTheDocument();
  });

  it("opens the Schedule", async () => {
    stubApi((call) => {
      if (call.path === "/schedule") return { body: { data: fixtureSchedule } };
      if (call.path === "/orchestrator/context") return { body: { data: fixtureContext } };
      return undefined;
    });
    renderWithClient(<ChatMenu showThinking={false} onToggleThinking={() => {}} canRelaunch={false} />);
    await userEvent.click(screen.getByRole("button", { name: "Conversation menu" }));
    expect(screen.queryByRole("menuitem", { name: "Compact now" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("menuitem", { name: "Schedule" }));
    expect(await screen.findByRole("table", { name: "Scheduled tasks" })).toBeInTheDocument();
  });

  it("shows the orchestrator's own spend and opens the Spend sheet from it", async () => {
    stubApi((call) => {
      if (call.path === "/spend/summary") return { body: { data: makePeriod() } };
      if (call.path.startsWith("/spend?")) return { body: { data: makeReport() } };
      if (call.path === "/orchestrator/context") return { body: { data: fixtureContext } };
      return undefined;
    });
    renderWithClient(<ChatMenu showThinking={false} onToggleThinking={() => {}} canRelaunch />);
    await userEvent.click(screen.getByRole("button", { name: "Conversation menu" }));
    const line = await screen.findByTestId("orchestrator-spend");
    expect(line).toHaveTextContent("$0.12 est. today · $2.31 est. all");
    await userEvent.click(line);
    expect(await screen.findByRole("dialog", { name: "Spend (estimated)" })).toBeInTheDocument();
  });
});
