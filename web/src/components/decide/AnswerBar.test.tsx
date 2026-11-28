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
    // The note is folded behind a link until wanted (R10 Fable F6); once it has text it stays open.
    await user.click(screen.getByRole("button", { name: "Add a note" }));
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
    // The job link lives in the packet's header now, not twice (R10 Fable F6).
    expect(screen.queryByRole("link", { name: /Open the full job/ })).toBeNull();
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
    await user.click(screen.getByRole("button", { name: "Add a note" }));
    await user.type(screen.getByLabelText(/Note/), "keep me");
    await user.click(screen.getByRole("button", { name: /Ship it/ }));
    await waitFor(() => expect(calls.some((call) => call.method === "GET" && call.path === "/questions/q_7")).toBe(true));
    expect(onAnswered).not.toHaveBeenCalled();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(localStorage.getItem("crowsnest-note:question:q_7")).toContain("keep me");
  });

  it("a gate question (it widens what agents may do) takes a second, deliberate tap to grant", async () => {
    const gate = makeQuestion({
      id: "q_8",
      title: "Land j_3 into main?",
      kind: "confirm",
      recommend: "allow",
      context: { land_for: "j_3" },
      options: [
        { key: "allow", label: "Allow: merge it" },
        { key: "deny", label: "Deny" },
      ],
    });
    const calls = stubApi((call) => {
      if (call.method === "POST") return { body: { data: { ...gate, status: "answered" } } };
      if (call.path === "/questions/q_8") return { body: { data: gate } };
      return undefined;
    });
    const user = userEvent.setup();
    const onAnswered = vi.fn();
    renderWithClient(<AnswerBar itemId="question:q_8" target={{ kind: "question", question: gate }} onAnswered={onAnswered} />);
    await user.click(screen.getByRole("button", { name: /Allow: merge it/ }));
    expect(calls.some((call) => call.method === "POST")).toBe(false);
    await user.click(screen.getByRole("button", { name: /Yes, allow: merge it/ }));
    await waitFor(() => expect(onAnswered).toHaveBeenCalled());
    expect(calls.find((call) => call.method === "POST")?.body).toEqual({ answer_value: "allow" });
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

  it("an everyday word (apply) answers in one tap: the second tap is for granting words only (R10 Opus F4)", async () => {
    const apply = makeQuestion({
      id: "q_9",
      title: "Apply the fleet cache refactor?",
      kind: "confirm",
      recommend: "yes",
      options: [
        { key: "yes", label: "Apply it" },
        { key: "no", label: "Not yet" },
      ],
    });
    const calls = stubApi((call) => {
      if (call.method === "POST") return { body: { data: { ...apply, status: "answered" } } };
      if (call.path === "/questions/q_9") return { body: { data: apply } };
      return undefined;
    });
    const user = userEvent.setup();
    const onAnswered = vi.fn();
    renderWithClient(<AnswerBar itemId="question:q_9" target={{ kind: "question", question: apply }} onAnswered={onAnswered} />);
    await user.click(screen.getByRole("button", { name: /Apply it/ }));
    await waitFor(() => expect(onAnswered).toHaveBeenCalledWith("Answered “Apply the fleet cache refactor?”"));
    expect(calls.find((call) => call.method === "POST")?.body).toEqual({ answer_value: "yes" });
    expect(screen.queryByText(/widens what agents may do/)).toBeNull();
  });

  it("says Sending at once, and a failed send says so in the bar with Retry, which sends again (R10 Fable B3)", async () => {
    let attempts = 0;
    stubApi((call) => {
      if (call.method === "POST") {
        attempts++;
        if (attempts === 1) return { status: 502, body: { error: { message: "Bad gateway", code: "HTTP_502" } } };
        return { body: { data: { ...question, status: "answered" } } };
      }
      if (call.path === "/questions/q_7") return { body: { data: question } };
      return undefined;
    });
    const user = userEvent.setup();
    const onAnswered = vi.fn();
    renderWithClient(<AnswerBar itemId="question:q_7" target={{ kind: "question", question }} onAnswered={onAnswered} />);
    await user.click(screen.getByRole("button", { name: /Ship it/ }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Not sent: “Ship it”. Bad gateway");
    expect(onAnswered).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(onAnswered).toHaveBeenCalled());
    expect(attempts).toBe(2);
  });

  it("shows Sending… on the pressed choice while the answer is on its way", async () => {
    let release: () => void = () => {};
    stubApi((call) => {
      if (call.method === "POST") return new Promise((resolve) => (release = () => resolve({ body: { data: { ...question, status: "answered" } } })));
      if (call.path === "/questions/q_7") return { body: { data: question } };
      return undefined;
    });
    const user = userEvent.setup();
    renderWithClient(<AnswerBar itemId="question:q_7" target={{ kind: "question", question }} onAnswered={() => {}} />);
    await user.click(screen.getByRole("button", { name: /Ship it/ }));
    expect(await screen.findByRole("button", { name: /Sending…/ })).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("status")).toHaveTextContent("Sending “Ship it”…");
    release();
  });

  it("ignores a tap while the auto-advance guard holds, then answers (R10 Opus B1)", async () => {
    const calls = stubApi((call) => {
      if (call.method === "POST") return { body: { data: { ...question, status: "answered" } } };
      if (call.path === "/questions/q_7") return { body: { data: question } };
      return undefined;
    });
    const user = userEvent.setup();
    const onAnswered = vi.fn();
    renderWithClient(<AnswerBar itemId="question:q_7" target={{ kind: "question", question }} onAnswered={onAnswered} armedAt={Date.now() + 400} />);
    const ship = screen.getByRole("button", { name: /Ship it/ });
    expect(ship).toHaveAttribute("aria-disabled", "true");
    await user.click(ship);
    expect(calls.some((call) => call.method === "POST")).toBe(false);
    await waitFor(() => expect(ship).not.toHaveAttribute("aria-disabled"), { timeout: 2000 });
    await user.click(ship);
    await waitFor(() => expect(onAnswered).toHaveBeenCalled());
  });

  it("presses the choices with the number keys", async () => {
    const calls = stubApi((call) => {
      if (call.method === "POST") return { body: { data: { ...question, status: "answered" } } };
      if (call.path === "/questions/q_7") return { body: { data: question } };
      return undefined;
    });
    const user = userEvent.setup();
    const onAnswered = vi.fn();
    renderWithClient(<AnswerBar itemId="question:q_7" target={{ kind: "question", question }} onAnswered={onAnswered} />);
    await user.keyboard("2");
    await waitFor(() => expect(onAnswered).toHaveBeenCalled());
    expect(calls.find((call) => call.method === "POST")?.body).toEqual({ answer_value: "no" });
  });
});
