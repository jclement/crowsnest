import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubApi } from "../test/render";
import { SetupForm } from "./SetupForm";

const TOKEN = "SZq05xlVK_nNdajE2A3RRbKvt1VqUVAhylMcoUf3-IM";

async function fill(user: ReturnType<typeof userEvent.setup>, password = "correct horse battery", confirm = password) {
  await user.type(screen.getByLabelText("Username"), "jeff");
  await user.type(screen.getByLabelText("Password"), password);
  await user.type(screen.getByLabelText("Password again"), confirm);
}

describe("SetupForm", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("prefills the token and the name, and enables submit only for a long, matching password", async () => {
    const user = userEvent.setup();
    render(<SetupForm initialToken={TOKEN} defaultName="quark" minLength={12} onDone={vi.fn()} />);
    expect(screen.getByLabelText("Setup token")).toHaveValue(TOKEN);
    expect(screen.getByLabelText("Instance name")).toHaveValue("quark");
    const submit = screen.getByRole("button", { name: "Set up crowsnest" });
    expect(submit).toBeDisabled();

    await user.type(screen.getByLabelText("Username"), "jeff");
    await user.type(screen.getByLabelText("Password"), "short");
    expect(screen.getByText(/Too short/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Password again"), "short");
    expect(submit).toBeDisabled();

    await user.clear(screen.getByLabelText("Password"));
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    expect(screen.getByText("The two passwords differ.")).toBeInTheDocument();
    expect(submit).toBeDisabled();
    await user.clear(screen.getByLabelText("Password again"));
    await user.type(screen.getByLabelText("Password again"), "correct horse battery");
    expect(screen.queryByText("The two passwords differ.")).not.toBeInTheDocument();
    expect(submit).toBeEnabled();
  });

  it("asks for the token when the link had none", () => {
    render(<SetupForm initialToken="" defaultName="quark" minLength={12} onDone={vi.fn()} />);
    expect(screen.getByLabelText("Setup token")).toHaveValue("");
    expect(screen.getByText(/after #token=/)).toBeInTheDocument();
  });

  it("posts the form and reports the owner", async () => {
    const calls = stubApi((call) => (call.path === "/setup" ? { status: 201, body: { data: { signed_in: true, username: "jeff" } } } : undefined));
    const onDone = vi.fn();
    const user = userEvent.setup();
    render(<SetupForm initialToken={TOKEN} defaultName="quark" minLength={12} onDone={onDone} />);
    await fill(user);
    await user.clear(screen.getByLabelText("Instance name"));
    await user.type(screen.getByLabelText("Instance name"), "devbox");
    await user.click(screen.getByRole("button", { name: "Set up crowsnest" }));
    await waitFor(() => expect(onDone).toHaveBeenCalledWith("jeff"));
    expect(calls).toEqual([
      { method: "POST", path: "/setup", body: { token: TOKEN, username: "jeff", password: "correct horse battery", name: "devbox" } },
    ]);
  });

  it("shows the server's message for a wrong token, and says sign in when setup is done", async () => {
    let status = 403;
    stubApi(() =>
      status === 403
        ? { status, body: { error: { code: "BAD_TOKEN", message: "That setup token is not right. Copy the link again from crowsnest's log." } } }
        : { status, body: { error: { code: "GONE", message: "gone" } } },
    );
    const onDone = vi.fn();
    const user = userEvent.setup();
    render(<SetupForm initialToken="wrong" defaultName="quark" minLength={12} onDone={onDone} />);
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Set up crowsnest" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("That setup token is not right");
    status = 410;
    await user.click(screen.getByRole("button", { name: "Set up crowsnest" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("already set up. Sign in instead."));
    expect(onDone).not.toHaveBeenCalled();
  });
});
