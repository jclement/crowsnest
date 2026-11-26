import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LoginForm } from "./LoginForm";

function respond(status: number, body: unknown) {
  return vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
}

async function fillAndSubmit(username: string, password: string) {
  const user = userEvent.setup();
  await user.type(screen.getByLabelText("Username"), username);
  await user.type(screen.getByLabelText("Password"), password);
  await user.click(screen.getByRole("button", { name: "Sign in" }));
}

describe("LoginForm", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("keeps the button disabled until both fields are filled", async () => {
    render(<LoginForm onSuccess={vi.fn()} />);
    const button = screen.getByRole("button", { name: "Sign in" });
    expect(button).toBeDisabled();
    await userEvent.setup().type(screen.getByLabelText("Username"), "jeff");
    expect(button).toBeDisabled();
    await userEvent.setup().type(screen.getByLabelText("Password"), "pw");
    expect(button).toBeEnabled();
  });

  it("signs in and reports the user", async () => {
    const me = { username: "jeff", features: { auth: true, voice: false, demo: false, session: "crowsnest" } };
    const fetchMock = respond(200, { data: me });
    vi.stubGlobal("fetch", fetchMock);
    const onSuccess = vi.fn();
    render(<LoginForm onSuccess={onSuccess} />);
    await fillAndSubmit("jeff", "hunter2");
    expect(onSuccess).toHaveBeenCalledWith(me);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/auth/login");
    expect(JSON.parse(init.body as string)).toEqual({ username: "jeff", password: "hunter2" });
  });

  it("shows a wrong-password message and clears the password", async () => {
    vi.stubGlobal("fetch", respond(401, { error: { code: "UNAUTHORIZED", message: "Wrong username or password." } }));
    const onSuccess = vi.fn();
    render(<LoginForm onSuccess={onSuccess} />);
    await fillAndSubmit("jeff", "nope");
    expect(await screen.findByRole("alert")).toHaveTextContent("Wrong username or password.");
    expect(screen.getByLabelText("Password")).toHaveValue("");
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it("shows the server's lockout message", async () => {
    vi.stubGlobal("fetch", respond(429, { error: { code: "RATE_LIMITED", message: "Too many failed sign-ins. Try again in 42 seconds." } }));
    render(<LoginForm onSuccess={vi.fn()} />);
    await fillAndSubmit("jeff", "nope");
    expect(await screen.findByRole("alert")).toHaveTextContent("Try again in 42 seconds.");
  });
});
