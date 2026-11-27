import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ProjectView } from "../../lib/projects";
import { renderWithClient, stubApi } from "../../test/render";
import { VoiceJob } from "./VoiceJob";

// The recorder is the fake STT here: the test hands the component a transcript directly.
const recorder = vi.hoisted(() => ({ send: (_text: string): Promise<void> => Promise.resolve() }));
vi.mock("../../lib/voice/recorder", () => ({
  usePushToTalk: (send: (text: string) => Promise<void>) => {
    recorder.send = send;
    return { state: { phase: "idle" }, press: vi.fn(), release: vi.fn(), cancel: vi.fn(), sendNow: vi.fn(), dismiss: vi.fn() };
  },
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: React.ReactNode }) => <a href="#job">{children}</a>,
}));

afterEach(() => vi.unstubAllGlobals());

const projects = [
  { id: 2, slug: "website", name: "Website", archived: false },
  { id: 3, slug: "mobile", name: "Mobile", archived: false },
] as unknown as ProjectView[];

function stub() {
  return stubApi((call) => {
    if (call.path === "/auth/me") return { body: { data: { username: "jeff", features: { voice: true, auth: true, demo: true, session: "s", name: "quark" } } } };
    if (call.method === "POST" && call.path === "/jobs") return { status: 201, body: { data: { id: "j_42", state: "queued" } } };
    if (call.method === "POST" && call.path === "/jobs/j_42/cancel") return { body: { data: { id: "j_42", state: "cancelled" } } };
    return undefined;
  });
}

describe("VoiceJob", () => {
  it("reads back what it heard, creates nothing until Create, then offers Undo", async () => {
    const calls = stub();
    const user = userEvent.setup();
    renderWithClient(<VoiceJob projects={projects} chosen="" />);
    expect(await screen.findByRole("button", { name: /Give a job by voice/ })).toBeInTheDocument();
    await act(() => recorder.send("Website: fix the checkout validation"));
    expect(screen.getByTestId("readback")).toHaveTextContent("Website: Fix the checkout validation. Create the job?");
    expect(calls.some((call) => call.method === "POST")).toBe(false);
    await user.click(screen.getByRole("button", { name: /Create the job/ }));
    await waitFor(() => expect(calls.find((call) => call.method === "POST")?.body).toEqual({ brief: "Fix the checkout validation", project_id: 2, to: "lead" }));
    expect(await screen.findByTestId("voice-job-created")).toHaveTextContent("Created j_42 for Website");
    await user.click(screen.getByRole("button", { name: /Undo/ }));
    await waitFor(() => expect(calls.some((call) => call.path === "/jobs/j_42/cancel")).toBe(true));
  });

  it("asks which project when the speech names two, and only then allows Create", async () => {
    const calls = stub();
    const user = userEvent.setup();
    renderWithClient(<VoiceJob projects={projects} chosen="" />);
    await screen.findByRole("button", { name: /Give a job by voice/ });
    await act(() => recorder.send("copy the website footer into mobile"));
    expect(screen.getByRole("alert")).toHaveTextContent("more than one project");
    expect(screen.getByRole("button", { name: /Create the job/ })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Mobile" }));
    await user.click(screen.getByRole("button", { name: /Create the job/ }));
    await waitFor(() => expect(calls.find((call) => call.method === "POST")?.body).toMatchObject({ project_id: 3 }));
  });
});
