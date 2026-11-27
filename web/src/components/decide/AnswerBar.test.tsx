import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { makeQuestion } from "../../lib/questionFixtures";
import { renderWithClient, stubApi } from "../../test/render";
import { AnswerBar } from "./AnswerBar";

// TanStack Router's Link needs a router; the bar's link is plain navigation, so stub it.
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, className }: { children: React.ReactNode; className?: string }) => (
    <a href="#job" className={className}>
      {children}
    </a>
  ),
}));

const question = makeQuestion({
  id: "q_7",
  title: "Ship the migration tonight?",
  kind: "confirm",
  recommend: "no",
  options: [
    { key: "yes", label: "Ship it" },
    { key: "no", label: "Wait for Monday" },
  ],
});

beforeEach(() => localStorage.clear());
afterEach(() => vi.unstubAllGlobals());

describe("AnswerBar", () => {
  it("keeps the note across a remount (rotation, a discarded page) and sends it with the answer", async () => {
    const calls = stubApi((call) => {
      if (call.method === "POST" && call.path === "/questions/q_7/answer") return { body: { data: { ...question, status: "answered" } } };
      if (call.path === "/questions/q_7") return { body: { data: question } };
      return undefined;
    });
    const user = userEvent.setup();
    const first = renderWithClient(<AnswerBar itemId="question:q_7" jobId="j_12" target={{ kind: "question", question }} onAnswered={() => {}} />);
    await user.type(screen.getByLabelText(/Note/), "only after the backup");
    first.unmount();

    const onAnswered = vi.fn();
    renderWithClient(<AnswerBar itemId="question:q_7" jobId="j_12" target={{ kind: "question", question }} onAnswered={onAnswered} />);
    expect(screen.getByLabelText(/Note/)).toHaveValue("only after the backup");
    expect(screen.getByRole("button", { name: /Wait for Monday/ })).toHaveTextContent("Recommended");
    await user.click(screen.getByRole("button", { name: /Wait for Monday/ }));
    await waitFor(() => expect(onAnswered).toHaveBeenCalled());
    expect(calls.find((call) => call.method === "POST")?.body).toEqual({ answer_value: "no", answer_note: "only after the backup" });
    expect(localStorage.getItem("crowsnest-note:question:q_7")).toBeNull();
    expect(screen.getByRole("link", { name: /Open the full job \(j_12\)/ })).toBeInTheDocument();
  });

  it("a 409 refetches (the packet turns stale) and keeps the note instead of showing an error", async () => {
    const calls = stubApi((call) => {
      if (call.method === "POST") return { status: 409, body: { error: { message: "already answered", code: "CONFLICT" } } };
      if (call.path === "/questions/q_7") return { body: { data: { ...question, status: "answered" } } };
      return undefined;
    });
    const user = userEvent.setup();
    const onAnswered = vi.fn();
    renderWithClient(<AnswerBar itemId="question:q_7" target={{ kind: "question", question }} onAnswered={onAnswered} />);
    await user.type(screen.getByLabelText(/Note/), "keep me");
    await user.click(screen.getByRole("button", { name: /Ship it/ }));
    await waitFor(() => expect(calls.some((call) => call.method === "GET" && call.path === "/questions/q_7")).toBe(true));
    expect(onAnswered).not.toHaveBeenCalled();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(localStorage.getItem("crowsnest-note:question:q_7")).toContain("keep me");
  });

  it("a dangerous approval asks again before allowing", async () => {
    const approval = {
      id: "ap_3",
      tool_name: "Bash",
      tool_input: { command: "git push --force" },
      suggestions: [],
      risk: "dangerous" as const,
      risk_reasons: ["force push"],
      status: "pending" as const,
      created_at: "2026-11-27T01:00:00Z",
      expires_at: "2026-11-27T13:00:00Z",
      summary: "Bash: git push --force",
      job_id: "j_13",
    };
    const calls = stubApi((call) => {
      if (call.method === "POST" && call.path === "/approvals/ap_3/decide") return { body: { data: { ...approval, status: "allowed" } } };
      if (call.path === "/approvals/ap_3") return { body: { data: approval } };
      return undefined;
    });
    const user = userEvent.setup();
    const onAnswered = vi.fn();
    renderWithClient(<AnswerBar itemId="approval:ap_3" target={{ kind: "approval", approval }} onAnswered={onAnswered} />);
    await user.click(screen.getByRole("button", { name: /Allow once/ }));
    expect(calls.some((call) => call.method === "POST")).toBe(false);
    await user.click(screen.getByRole("button", { name: /Yes, allow it/ }));
    await waitFor(() => expect(onAnswered).toHaveBeenCalled());
    expect(calls.find((call) => call.method === "POST")?.body).toEqual({ decision: "allow", confirm: true });
  });
});
