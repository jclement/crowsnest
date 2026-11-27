import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { fixtureApprovals } from "../../lib/approvalFixtures";
import type { Approval } from "../../lib/approvals";
import { renderWithClient, stubApi } from "../../test/render";
import { BatchAllow } from "./BatchAllow";

const base = fixtureApprovals[0]!;
const call = (id: string, command: string, extra: Partial<Approval> = {}): Approval => ({
  ...base,
  id,
  job_id: "j_12",
  agent_name: "job-12",
  risk: "low",
  status: "pending",
  tool_input: { command },
  summary: `Bash: ${command}`,
  ...extra,
});

const a = call("ap_21", "ls");
const b = call("ap_22", "pwd");
const c = call("ap_23", "go vet ./...");
const elevated = call("ap_24", "npm install", { risk: "elevated" });
const otherJob = call("ap_25", "ls", { job_id: "j_99" });

function stub(pending: Approval[]) {
  return stubApi((request) => {
    if (request.method === "GET" && request.path === "/approvals?status=pending") return { body: { data: pending } };
    if (request.method === "POST" && request.path === "/approvals/batch-allow") {
      const ids = (request.body as { ids: string[] }).ids;
      return { body: { data: pending.filter((item) => ids.includes(item.id)).map((item) => ({ ...item, status: "allowed" })) } };
    }
    return undefined;
  });
}

describe("BatchAllow", () => {
  it("lists the job's low-risk calls and allows them with one confirming tap", async () => {
    const calls = stub([a, b, c, elevated, otherJob]);
    const user = userEvent.setup();
    renderWithClient(<BatchAllow approval={a} />);
    await user.click(await screen.findByRole("button", { name: "Allow 3 low-risk calls from job-12" }));
    const list = screen.getByRole("list", { name: "Calls in the batch" });
    expect(list.textContent).toContain("Bash: ls");
    expect(list.textContent).toContain("Bash: go vet ./...");
    expect(list.textContent).not.toContain("npm install");
    expect(calls.some((request) => request.method === "POST")).toBe(false);
    await user.click(screen.getByRole("button", { name: "Allow all 3" }));
    await waitFor(() => expect(calls.find((request) => request.method === "POST")?.body).toEqual({ ids: ["ap_21", "ap_22", "ap_23"] }));
  });

  it("is not offered for one call alone, or for a call that is not low risk", async () => {
    stub([a, elevated, otherJob]);
    const { unmount } = renderWithClient(<BatchAllow approval={a} />);
    await waitFor(() => expect(screen.queryByRole("button")).toBeNull());
    unmount();
    renderWithClient(<BatchAllow approval={elevated} />);
    await waitFor(() => expect(screen.queryByRole("button")).toBeNull());
  });
});
