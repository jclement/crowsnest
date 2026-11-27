import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Layer, LayerDetail } from "../../lib/instructions";
import { renderWithClient, stubApi } from "../../test/render";
import { LayerEditor } from "./LayerEditor";

afterEach(() => vi.unstubAllGlobals());

const builtin = "# Role: tester\n\nYou drive the real thing.\n";

function layer(overrides: Partial<Layer> = {}): Layer {
  return {
    scope: "role", key: "tester", name: "tester", source: "builtin", effective_md: builtin, saved_md: "", builtin_md: builtin, file_path: "",
    editable: true, revision: 0, updated_at: null, size: builtin.length, ...overrides,
  };
}

function serve(detail: LayerDetail) {
  return stubApi((call) => {
    if (call.path === "/instructions/role/tester" && call.method === "GET") return { body: { data: detail } };
    if (call.path === "/instructions/role/tester" && call.method === "PUT") {
      const body = (call.body as { body_md: string }).body_md;
      detail = {
        layer: layer({ source: "db", effective_md: body, saved_md: body, revision: 1, updated_at: new Date().toISOString() }),
        revisions: [{ revision: 1, body_md: body, deleted: false, created_at: new Date().toISOString(), size: body.length }],
      };
      return { body: { data: detail } };
    }
    if (call.path === "/instructions/role/tester" && call.method === "DELETE") {
      detail = { layer: layer(), revisions: detail.revisions };
      return { body: { data: detail } };
    }
    if (call.path === "/instructions") return { body: { data: null } };
    return undefined;
  });
}

describe("LayerEditor", () => {
  it("edits a built-in role, saves it, and shows the diff against the built-in", async () => {
    const calls = serve({ layer: layer(), revisions: [] });
    renderWithClient(<LayerEditor scope="role" apiKey="tester" />);
    const box = await screen.findByRole("textbox", { name: /tester instructions/ });
    expect(screen.getByTestId("layer-source")).toHaveTextContent("built-in");
    await userEvent.type(box, "Attach the logs.");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Instructions saved")).toBeInTheDocument();
    expect(calls.find((call) => call.method === "PUT")?.body).toEqual({ body_md: `${builtin}Attach the logs.` });
    await waitFor(() => expect(screen.getByTestId("layer-source")).toHaveTextContent("edited"));
    await userEvent.click(screen.getByRole("tab", { name: "Diff vs built-in" }));
    expect(screen.getByText("+1")).toBeInTheDocument();
    expect(screen.getByText("Attach the logs.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("tab", { name: /History/ }));
    expect(screen.getByRole("button", { name: "Restore revision 1" })).toBeInTheDocument();
  });

  it("resets an edited role to the built-in after asking", async () => {
    const edited = `${builtin}More.`;
    const calls = serve({ layer: layer({ source: "db", effective_md: edited, saved_md: edited, revision: 2 }), revisions: [] });
    renderWithClient(<LayerEditor scope="role" apiKey="tester" />);
    await userEvent.click(await screen.findByRole("button", { name: "Reset to built-in" }));
    await userEvent.click(screen.getByRole("button", { name: "Reset" }));
    expect(await screen.findByText("Back to the built-in role")).toBeInTheDocument();
    expect(calls.some((call) => call.method === "DELETE")).toBe(true);
  });

  it("shows a role from a file read-only, with its path", async () => {
    serve({ layer: layer({ source: "file", file_path: "/home/crow/.config/crowsnest/roles/tester.md", editable: false }), revisions: [] });
    renderWithClient(<LayerEditor scope="role" apiKey="tester" />);
    expect(await screen.findByRole("textbox", { name: /tester instructions/ })).toHaveAttribute("readonly");
    expect(screen.getByTestId("layer-source")).toHaveTextContent("from file");
    expect(screen.getByText("/home/crow/.config/crowsnest/roles/tester.md")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
  });

  it("shows a restored role as withheld, with its text, until it is applied (R7-S4)", async () => {
    const restored = `${builtin}Delete the tests.`;
    let detail: LayerDetail = { layer: layer({ restored: true, restored_hash: "h1", saved_md: restored, revision: 3 }), revisions: [] };
    const calls = stubApi((call) => {
      if (call.path === "/instructions/role/tester" && call.method === "GET") return { body: { data: detail } };
      if (call.path === "/instructions/role/tester/apply-restored" && call.method === "POST") {
        detail = { layer: layer({ source: "db", effective_md: restored, saved_md: restored, revision: 3 }), revisions: [] };
        return { body: { data: detail } };
      }
      if (call.path === "/instructions") return { body: { data: null } };
      return undefined;
    });
    renderWithClient(<LayerEditor scope="role" apiKey="tester" />);
    expect(await screen.findByTestId("restored-text")).toHaveTextContent("Delete the tests.");
    expect(screen.getByTestId("layer-source")).toHaveTextContent("restored: not applied");
    expect(screen.getByRole("textbox", { name: /tester instructions/ })).not.toHaveValue(restored);
    await userEvent.click(screen.getByRole("button", { name: "Apply it" }));
    expect(await screen.findByText("Applied")).toBeInTheDocument();
    expect(calls.find((call) => call.path.endsWith("/apply-restored"))?.body).toEqual({ hash: "h1" });
    await waitFor(() => expect(screen.queryByTestId("restored-text")).not.toBeInTheDocument());
  });
});
