import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ApprovalRule, RulePreview } from "../../lib/policy";
import { renderWithClient, stubApi } from "../../test/render";
import { ApprovalRulesCard } from "./ApprovalRulesCard";

const recent: RulePreview = {
  considered: 6,
  allowed: [
    { approval_id: "ap_4", summary: "Bash: go test ./internal/ratelimit/...", risk: "low", status: "allowed", created_at: "2026-11-27T08:00:00Z" },
    { approval_id: "ap_9", summary: "Bash: go test ./...", risk: "low", status: "pending", created_at: "2026-11-27T08:01:00Z" },
  ],
  above_max_risk: [],
  dangerous: [],
};

const rule: ApprovalRule = {
  id: "r_1",
  scope: "project",
  project_id: 1,
  kind: "bash",
  pattern: "go test *",
  max_risk: "low",
  note: "Tests are safe to run unattended.",
  created_at: new Date().toISOString(),
  created_via: "demo",
  sentence: "Allow Bash commands matching `go test *`",
  recent,
};

function stubRules(initial: ApprovalRule[]) {
  let rules = [...initial];
  return stubApi((call) => {
    if (call.path === "/projects/crowsnest/approval-rules" && call.method === "GET") return { body: { data: rules } };
    if (call.path === "/projects/crowsnest/approval-rules/preview") {
      const body = call.body as { pattern: string };
      const preview: RulePreview =
        body.pattern === "rm *"
          ? { considered: 6, allowed: [], above_max_risk: [], dangerous: [{ approval_id: "ap_3", summary: "Bash: rm -rf build", risk: "dangerous", status: "pending", created_at: "" }] }
          : recent;
      return { body: { data: preview } };
    }
    if (call.path === "/projects/crowsnest/approval-rules" && call.method === "POST") {
      const body = call.body as { kind: "bash"; pattern: string; max_risk: "low" | "elevated" };
      const added: ApprovalRule = { ...rule, id: "r_2", pattern: body.pattern, max_risk: body.max_risk, created_via: "browser", note: "", sentence: `Allow Bash commands matching \`${body.pattern}\`` };
      rules = [...rules, added];
      return { status: 201, body: { data: added } };
    }
    const removed = /^\/approval-rules\/(r_\d+)$/.exec(call.path);
    if (removed && call.method === "DELETE") {
      rules = rules.filter((item) => item.id !== removed[1]);
      return { body: { data: rule } };
    }
    return undefined;
  });
}

describe("ApprovalRulesCard", () => {
  it("lists each rule as a sentence with what it matched recently", async () => {
    stubRules([rule]);
    renderWithClient(<ApprovalRulesCard projectRef="crowsnest" />);
    const row = await screen.findByText((_, element) => element?.getAttribute("data-rule") === "r_1");
    expect(row.textContent).toContain("Allow Bash commands matching go test *");
    expect(within(row).getByTestId("rule-preview").textContent).toContain("Would have matched 2 recent calls: go test ./internal/ratelimit/..., go test ./...");
    expect(row.textContent).toContain("Tests are safe to run unattended.");
  });

  it("previews a pattern before it is saved, flags a dangerous match, then adds it", async () => {
    const calls = stubRules([]);
    const user = userEvent.setup();
    renderWithClient(<ApprovalRulesCard projectRef="crowsnest" />);
    await screen.findByText(/No rules/);
    const pattern = screen.getByLabelText("Pattern");
    await user.type(pattern, "rm *");
    expect(await screen.findByText(/never\s+auto-allowed/)).toBeInTheDocument();
    expect(calls.some((call) => call.method === "POST" && call.path.endsWith("/approval-rules"))).toBe(false);
    await user.clear(pattern);
    await user.type(pattern, "go test *");
    await user.click(screen.getByRole("switch", { name: "Also when elevated" }));
    await user.click(screen.getByRole("button", { name: "Add rule" }));
    await waitFor(() =>
      expect(calls.find((call) => call.method === "POST" && call.path === "/projects/crowsnest/approval-rules")?.body).toEqual({ kind: "bash", pattern: "go test *", max_risk: "elevated" }),
    );
    expect(await screen.findByText("also when elevated")).toBeInTheDocument();
  });

  it("removes a rule", async () => {
    const calls = stubRules([rule]);
    const user = userEvent.setup();
    renderWithClient(<ApprovalRulesCard projectRef="crowsnest" />);
    await user.click(await screen.findByRole("button", { name: /Remove r_1/ }));
    await waitFor(() => expect(calls.some((call) => call.method === "DELETE" && call.path === "/approval-rules/r_1")).toBe(true));
    expect(await screen.findByText(/No rules/)).toBeInTheDocument();
  });

  it("shows a refused rule's reason", async () => {
    stubApi((call) => {
      if (call.method === "GET") return { body: { data: [] } };
      if (call.path.endsWith("/preview")) return { status: 400, body: { error: { code: "VALIDATION_ERROR", message: "`*` alone would allow every command." } } };
      return { status: 400, body: { error: { code: "VALIDATION_ERROR", message: "`*` alone would allow every command." } } };
    });
    const user = userEvent.setup();
    renderWithClient(<ApprovalRulesCard projectRef="crowsnest" />);
    await user.type(await screen.findByLabelText("Pattern"), "*");
    await user.click(screen.getByRole("button", { name: "Add rule" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("would allow every command");
  });
});
