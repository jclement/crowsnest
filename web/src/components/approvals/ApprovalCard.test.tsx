import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { fixtureApprovals } from "../../lib/approvalFixtures";
import type { Approval } from "../../lib/approvals";
import { renderWithClient, stubApi, type Call } from "../../test/render";
import { ApprovalCard } from "./ApprovalCard";

const bash = fixtureApprovals[0]!;
const edit = fixtureApprovals[1]!;
const dangerous = fixtureApprovals[2]!;

function stubDecide(): Call[] {
  return stubApi((call) => {
    const match = /^\/approvals\/(ap_\d+)\/decide$/.exec(call.path);
    if (!match || call.method !== "POST") return undefined;
    const request = call.body as { decision: string; confirm?: boolean };
    const source = fixtureApprovals.find((approval) => approval.id === match[1])!;
    if (source.risk === "dangerous" && request.decision === "allow" && !request.confirm) {
      return { status: 409, body: { error: { code: "CONFIRM_REQUIRED", message: "Confirm it" } } };
    }
    return { body: { data: { ...source, status: request.decision === "allow" ? "allowed" : "denied" } } };
  });
}

const decides = (calls: Call[]) => calls.filter((call) => call.method === "POST").map((call) => call.body);

describe("ApprovalCard", () => {
  it("shows a Bash call's command, directory and risk line", () => {
    stubDecide();
    renderWithClient(<ApprovalCard approval={bash} onOpenTerminal={() => {}} />);
    expect(screen.getByLabelText("Command").textContent).toBe("go test ./internal/fleet/...");
    expect(screen.getByText("/home/crow/src/crowsnest")).toBeInTheDocument();
    expect(screen.getByTestId("risk-line").textContent).toMatch(/No risky pattern/);
    expect(screen.getByRole("button", { name: "Allow once" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Open terminal" })).toBeInTheDocument();
    expect(screen.queryByText(/always allow/i, { selector: "button" })).toBeNull();
  });

  it("shows an Edit as a diff", () => {
    stubDecide();
    renderWithClient(<ApprovalCard approval={edit} />);
    expect(screen.getByText(/screen\.go/)).toBeInTheDocument();
    expect(screen.getAllByText(/dialogTailLines/).length).toBeGreaterThan(0);
  });

  it("shows an MCP call's server, tool and arguments, and a WebFetch's URL", () => {
    stubDecide();
    const mcp: Approval = { ...bash, id: "ap_7", tool_name: "mcp__github__create_issue", tool_input: { title: "Bug" }, risk: "elevated", risk_reasons: ["Calls a tool on an MCP server"] };
    const { unmount } = renderWithClient(<ApprovalCard approval={mcp} />);
    expect(screen.getByText("github")).toBeInTheDocument();
    expect(screen.getByText("create_issue")).toBeInTheDocument();
    expect(screen.getByLabelText("Arguments").textContent).toContain('"title": "Bug"');
    unmount();
    const fetchCall: Approval = { ...bash, id: "ap_8", tool_name: "WebFetch", tool_input: { url: "https://example.com/x", prompt: "read" } };
    renderWithClient(<ApprovalCard approval={fetchCall} />);
    expect(screen.getByLabelText("URL").textContent).toBe("https://example.com/x");
  });

  it("allows once, and denies with the note", async () => {
    const user = userEvent.setup();
    const calls = stubDecide();
    const { unmount } = renderWithClient(<ApprovalCard approval={bash} />);
    await user.click(screen.getByRole("button", { name: "Allow once" }));
    await waitFor(() => expect(decides(calls)).toEqual([{ decision: "allow" }]));
    unmount();
    renderWithClient(<ApprovalCard approval={edit} />);
    await user.type(screen.getByLabelText(/Note to the agent/), "use printf instead");
    await user.click(screen.getByRole("button", { name: "Deny" }));
    await waitFor(() => expect(decides(calls).at(-1)).toEqual({ decision: "deny", note: "use printf instead" }));
  });

  it("asks a second time before allowing a dangerous call", async () => {
    const user = userEvent.setup();
    const calls = stubDecide();
    renderWithClient(<ApprovalCard approval={dangerous} />);
    expect(screen.queryByRole("button", { name: "Allow once" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Allow…" }));
    expect(decides(calls)).toEqual([]);
    expect(screen.getByRole("alertdialog")).toHaveTextContent(/This is dangerous/);
    expect(screen.getByRole("alertdialog")).toHaveTextContent(/Force push/);
    await user.click(screen.getByRole("button", { name: "Yes, allow it" }));
    await waitFor(() => expect(decides(calls)).toEqual([{ decision: "allow", confirm: true }]));
  });

  it("answers y and n only while the card itself has focus", async () => {
    const user = userEvent.setup();
    const calls = stubDecide();
    renderWithClient(<ApprovalCard approval={bash} />);
    await user.keyboard("y");
    await user.type(screen.getByLabelText(/Note to the agent/), "yes n y");
    expect(decides(calls)).toEqual([]);
    screen.getByRole("region").focus();
    await user.keyboard("n");
    await waitFor(() => expect(decides(calls)).toEqual([{ decision: "deny", note: "yes n y" }]));
  });

  it("y on a dangerous card opens the confirmation and allows nothing", async () => {
    const user = userEvent.setup();
    const calls = stubDecide();
    renderWithClient(<ApprovalCard approval={dangerous} />);
    screen.getByRole("region").focus();
    await user.keyboard("y");
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(decides(calls)).toEqual([]);
  });

  it("shows how a settled approval ended, with no buttons", () => {
    stubDecide();
    renderWithClient(<ApprovalCard approval={{ ...bash, status: "denied", note: "not now" }} />);
    expect(screen.getByText("Denied: not now")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Allow once" })).toBeNull();
  });
});
