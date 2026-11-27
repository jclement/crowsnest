// The remember flow on ApprovalCard: "Allow and remember for this job" opens an inline confirm with
// the proposed pattern, editable, and the sentence saying what it does until the job ends.
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { fixtureApprovals } from "../../lib/approvalFixtures";
import type { Approval } from "../../lib/approvals";
import { renderWithClient, stubApi } from "../../test/render";
import { ApprovalCard } from "./ApprovalCard";

const bash: Approval = { ...fixtureApprovals[0]!, job_id: "j_12", remember: { kind: "bash", pattern: "go test *" } };
const dangerous: Approval = { ...fixtureApprovals[2]!, job_id: "j_13", remember: undefined };

function stubDecide() {
  return stubApi((call) => {
    if (call.method === "POST" && call.path === `/approvals/${bash.id}/decide`) return { body: { data: { ...bash, status: "allowed", remember: undefined } } };
    return undefined;
  });
}

describe("Allow and remember for this job", () => {
  it("confirms an editable pattern and sends it with the allow", async () => {
    const calls = stubDecide();
    const user = userEvent.setup();
    renderWithClient(<ApprovalCard approval={bash} />);
    await user.click(screen.getByRole("button", { name: "Allow and remember for this job" }));
    const field = screen.getByRole("textbox", { name: /Pattern/ });
    expect(field).toHaveValue("go test *");
    expect(screen.getByTestId("remember-sentence").textContent).toBe(
      "Until j_12 ends, crowsnest will allow Bash commands matching `go test *` without asking, but only while each call is low risk. Elevated and dangerous calls are always asked.",
    );
    await user.clear(field);
    await user.type(field, "go test ./internal/*");
    expect(screen.getByTestId("remember-sentence").textContent).toContain("`go test ./internal/*`");
    await user.click(screen.getByRole("button", { name: "Allow and remember" }));
    await waitFor(() => expect(calls.find((call) => call.method === "POST")?.body).toEqual({ decision: "allow", remember: { pattern: "go test ./internal/*" } }));
  });

  it("offers an elevated call only as itself, read-only (R6-S1)", async () => {
    stubDecide();
    const user = userEvent.setup();
    const push: Approval = { ...bash, risk: "elevated", remember: { kind: "bash", pattern: "git push origin feature/x", exact: true } };
    renderWithClient(<ApprovalCard approval={push} />);
    await user.click(screen.getByRole("button", { name: "Allow and remember for this job" }));
    const field = screen.getByRole("textbox", { name: /Pattern/ });
    expect(field).toHaveValue("git push origin feature/x");
    expect(field).toHaveAttribute("readonly");
    const sentence = screen.getByTestId("remember-sentence").textContent ?? "";
    expect(sentence).toContain("exactly this command, word for word");
    expect(sentence).toContain("an interpreter");
  });

  it("can be cancelled without deciding anything", async () => {
    const calls = stubDecide();
    const user = userEvent.setup();
    renderWithClient(<ApprovalCard approval={bash} />);
    await user.click(screen.getByRole("button", { name: "Allow and remember for this job" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByRole("button", { name: "Allow once" })).toBeInTheDocument();
    expect(calls).toHaveLength(0);
  });

  it("is never offered for a dangerous call, nor without a job", () => {
    stubDecide();
    const { unmount } = renderWithClient(<ApprovalCard approval={dangerous} />);
    expect(screen.queryByRole("button", { name: "Allow and remember for this job" })).toBeNull();
    unmount();
    renderWithClient(<ApprovalCard approval={{ ...bash, job_id: undefined }} />);
    expect(screen.queryByRole("button", { name: "Allow and remember for this job" })).toBeNull();
  });

  it("shows why a settled call was allowed by a rule", () => {
    stubDecide();
    renderWithClient(
      <ApprovalCard approval={{ ...bash, status: "allowed", decided_via: "policy", status_reason: "Allowed by rule r_1 (project Crowsnest): Bash commands matching `go test *`" }} />,
    );
    expect(screen.getByText("Allowed by rule r_1 (project Crowsnest): Bash commands matching `go test *`")).toBeInTheDocument();
  });
});
