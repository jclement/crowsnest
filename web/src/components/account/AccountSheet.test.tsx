import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Account } from "../../lib/api";
import { renderWithClient, stubApi } from "../../test/render";
import { AccountBody } from "./AccountSheet";

const stored: Account = { username: "jeff", source: "database", name: "quark", name_managed: false, min_password_length: 12 };

describe("AccountBody", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("changes the password with the current one, then clears the form", async () => {
    const calls = stubApi((call) => (call.path === "/account/password" ? { body: { data: { changed: true, signed_out_others: true } } } : undefined));
    const user = userEvent.setup();
    renderWithClient(<AccountBody account={stored} />);
    const submit = screen.getByRole("button", { name: "Change password" });
    await user.type(screen.getByLabelText("Current password"), "old password here");
    await user.type(screen.getByLabelText("New password"), "a brand new password");
    expect(submit).toBeDisabled();
    await user.type(screen.getByLabelText("New password again"), "a brand new password");
    await user.click(submit);
    expect(await screen.findByText("Password changed")).toBeInTheDocument();
    expect(calls).toEqual([
      { method: "POST", path: "/account/password", body: { current_password: "old password here", new_password: "a brand new password" } },
    ]);
    await waitFor(() => expect(screen.getByLabelText("Current password")).toHaveValue(""));
  });

  it("shows a wrong current password without signing out", async () => {
    stubApi(() => ({ status: 403, body: { error: { code: "WRONG_PASSWORD", message: "The current password is not right." } } }));
    const user = userEvent.setup();
    renderWithClient(<AccountBody account={stored} />);
    await user.type(screen.getByLabelText("Current password"), "nope nope nope");
    await user.type(screen.getByLabelText("New password"), "a brand new password");
    await user.type(screen.getByLabelText("New password again"), "a brand new password");
    await user.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The current password is not right.");
  });

  it("shows a configured login and a --name read-only", () => {
    renderWithClient(<AccountBody account={{ ...stored, source: "config", name_managed: true }} />);
    expect(screen.getByText("Managed outside crowsnest")).toBeInTheDocument();
    expect(screen.queryByLabelText("Current password")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Name")).not.toBeInTheDocument();
    expect(screen.getByText(/CROWSNEST_NAME/)).toBeInTheDocument();
  });

  it("saves a new instance name", async () => {
    const calls = stubApi((call) => (call.path === "/account/name" ? { body: { data: { name: "devbox" } } } : { body: { data: {} } }));
    const user = userEvent.setup();
    renderWithClient(<AccountBody account={stored} />);
    const save = screen.getByRole("button", { name: "Save name" });
    expect(save).toBeDisabled();
    await user.clear(screen.getByLabelText("Name"));
    await user.type(screen.getByLabelText("Name"), "devbox");
    await user.click(save);
    expect(await screen.findByText("Name saved")).toBeInTheDocument();
    expect(calls[0]).toEqual({ method: "PUT", path: "/account/name", body: { name: "devbox" } });
  });
});
