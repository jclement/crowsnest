import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { makeDetail } from "../../lib/jobFixtures";
import { renderWithClient, stubApi } from "../../test/render";
import { LimitBanner } from "../control/LimitBanner";
import { QuestionBody } from "../inbox/QuestionDetail";
import { makeQuestion } from "../../lib/questionFixtures";
import { BudgetCard, EvidenceCard } from "./AcceptPanels";

describe("EvidenceCard", () => {
  it("shows the check crowsnest ran, its commit and output, and runs it again", async () => {
    const calls = stubApi((call) => (call.path === "/jobs/j_1/recheck" ? { body: { data: makeDetail() } } : undefined));
    renderWithClient(
      <EvidenceCard
        job={makeDetail({
          state: "needs_you",
          cause: "check",
          accept_state: "failed",
          accept_detail: "The check still fails after 3 rounds",
          evidence: {
            command: "mise run check",
            status: "failed",
            exit_code: 1,
            duration_ms: 42_000,
            commit: "abcdef0123456789",
            output_tail: "--- FAIL: TestWidget\nFAIL",
            started_at: "2026-11-27T10:00:00Z",
            finished_at: "2026-11-27T10:00:42Z",
            stale: false,
          },
        })}
      />,
    );
    expect(screen.getByText("mise run check")).toBeInTheDocument();
    expect(screen.getByText(/exit 1 · 42 s/)).toBeInTheDocument();
    expect(screen.getByText("abcdef0123")).toBeInTheDocument();
    expect(screen.getByText(/--- FAIL: TestWidget/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Run the check again" }));
    await waitFor(() => expect(calls.some((call) => call.path === "/jobs/j_1/recheck" && call.method === "POST")).toBe(true));
  });
});

describe("BudgetCard", () => {
  it("offers raise, finish the step and stop at the limit, and says what is not measured", async () => {
    const calls = stubApi((call) => (call.path.startsWith("/jobs/j_1/") ? { body: { data: makeDetail() } } : undefined));
    renderWithClient(
      <BudgetCard
        job={makeDetail({
          state: "needs_you",
          cause: "budget",
          budget: {
            limits: { usd: 1, tokens: 0, minutes: 30, turns: 0, source: {}, fleet: false },
            used: { usd: null, tokens: null, minutes: 31, turns: 9, measured: false, children: 0 },
            share: 1.03,
            over: "minutes",
            state: "exceeded",
            detail: "Used 31 of its 30 minutes",
          },
        })}
      />,
    );
    expect(screen.getByText("not measured of $1.00")).toBeInTheDocument();
    expect(screen.getByText(/spend is not measured/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Let it finish this step" }));
    await waitFor(() => expect(calls.some((call) => call.path === "/jobs/j_1/budget/finish-step")).toBe(true));
    await userEvent.click(screen.getByRole("button", { name: "Raise the budget" }));
    await userEvent.type(screen.getByLabelText("Budget in minutes"), "90");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls.some((call) => call.path === "/jobs/j_1/budget" && call.method === "PUT")).toBe(true));
  });
});

describe("LimitBanner", () => {
  it("is one banner with the countdown", async () => {
    stubApi((call) =>
      call.path === "/limits"
        ? { body: { data: [{ engine: "claude", since: new Date().toISOString(), reset_at: new Date(Date.now() + 23 * 60_000).toISOString(), guessed: false, job: "j_4", detail: "", held: 3, resuming: false }] } }
        : undefined,
    );
    renderWithClient(<LimitBanner />);
    const banner = await screen.findByRole("status", { name: "claude usage limit" });
    expect(banner).toHaveTextContent("resets in 23 min");
    expect(banner).toHaveTextContent("3 jobs waiting");
  });
});

describe("answer receipt", () => {
  it("shows an uncertain delivery with 'Did it arrive?' before any retype", async () => {
    const calls = stubApi((call) => (call.path === "/questions/q_1/reconcile" ? { body: { data: makeQuestion({ status: "answered", receipt: "received" }) } } : undefined));
    renderWithClient(
      <QuestionBody question={makeQuestion({ status: "answered", answer_value: "yes", delivery: "type", delivery_error: "delivery uncertain: …", receipt: "uncertain", job_id: "j_1" })} />,
    );
    expect(screen.getByText("Delivery uncertain")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Did it arrive?" }));
    await waitFor(() => expect(calls.some((call) => call.path === "/questions/q_1/reconcile")).toBe(true));
    expect(screen.getByRole("button", { name: "Type it again" })).toBeInTheDocument();
  });

  it("shows received", () => {
    stubApi(() => undefined);
    renderWithClient(<QuestionBody question={makeQuestion({ status: "answered", answer_value: "yes", delivery: "type", receipt: "received", received_at: new Date().toISOString() })} />);
    expect(screen.getByText("Received")).toBeInTheDocument();
  });
});
