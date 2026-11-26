import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { makeJob } from "../../lib/jobFixtures";
import { renderWithClient, stubApi } from "../../test/render";
import { offerJobDraft } from "../../lib/jobDraft";
import { NewJobForm } from "./NewJobForm";

function stubForm() {
  return stubApi((call) => {
    if (call.path === "/jobs" && call.method === "POST") {
      const body = call.body as { brief: string };
      if (!body.brief.trim()) return { status: 400, body: { error: { code: "VALIDATION_ERROR", message: "brief is required" } } };
      return { status: 201, body: { data: makeJob({ id: "j_9", title: body.brief }) } };
    }
    if (call.path === "/roles") return { body: { data: [{ name: "builder", summary: "Builds", custom: false }, { name: "reviewer", summary: "Reviews", custom: false }] } };
    if (call.path === "/dirs") return { body: { data: [{ path: "/src/app", label: "app", source: "remembered" }] } };
    return undefined;
  });
}

describe("NewJobForm", () => {
  it("gives a job to the orchestrator from the text box alone", async () => {
    const calls = stubForm();
    const onCreated = vi.fn();
    renderWithClient(<NewJobForm onCreated={onCreated} />);
    const button = screen.getByRole("button", { name: "Give job" });
    expect(button).toBeDisabled();
    expect(screen.getByRole("button", { name: /To the orchestrator/ })).toHaveAttribute("aria-expanded", "false");
    await userEvent.type(screen.getByLabelText("What needs doing?"), "Write the changelog");
    await userEvent.click(button);
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(expect.objectContaining({ id: "j_9" })));
    const post = calls.find((call) => call.method === "POST");
    expect(post?.body).toEqual({ brief: "Write the changelog", engine: "claude" });
    expect(screen.getByLabelText("What needs doing?")).toHaveValue("");
  });

  it("sends where, role, done-when and engine from behind the disclosure", async () => {
    const calls = stubForm();
    renderWithClient(<NewJobForm />);
    await userEvent.click(screen.getByRole("button", { name: /To the orchestrator/ }));
    await userEvent.type(screen.getByLabelText(/Where/), "/src/app");
    await waitFor(() => expect(screen.getByRole("option", { name: "reviewer" })).toBeInTheDocument());
    await userEvent.selectOptions(screen.getByLabelText("Role"), "reviewer");
    await userEvent.type(screen.getByLabelText("Done when"), "tests pass");
    await userEvent.selectOptions(screen.getByLabelText("Engine"), "codex");
    expect(screen.getByRole("button", { name: /To app · reviewer · codex/ })).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("What needs doing?"), "Review the parser{Meta>}{Enter}{/Meta}");
    await waitFor(() => expect(calls.some((call) => call.method === "POST")).toBe(true));
    expect(calls.find((call) => call.method === "POST")?.body).toEqual({
      brief: "Review the parser", dir: "/src/app", role: "reviewer", done_when: "tests pass", engine: "codex",
    });
  });

  it("keeps the brief and says why when the server refuses", async () => {
    stubApi((call) => (call.path === "/jobs" ? { status: 400, body: { error: { code: "VALIDATION_ERROR", message: "dir /nope does not exist" } } } : undefined));
    renderWithClient(<NewJobForm />);
    await userEvent.type(screen.getByLabelText("What needs doing?"), "Do it");
    await userEvent.click(screen.getByRole("button", { name: "Give job" }));
    expect(await screen.findByText("dir /nope does not exist")).toBeInTheDocument();
    expect(screen.getByLabelText("What needs doing?")).toHaveValue("Do it");
  });

  it("takes a draft from the command palette: fills the box and opens the options", async () => {
    stubForm();
    offerJobDraft({ brief: "Tidy the README" });
    renderWithClient(<NewJobForm />);
    expect(screen.getByLabelText("What needs doing?")).toHaveValue("Tidy the README");
    expect(screen.getByRole("button", { name: /To the orchestrator/ })).toHaveAttribute("aria-expanded", "true");
    // A draft offered while the form is on screen lands too, once.
    act(() => offerJobDraft({ brief: "Second thought" }));
    expect(screen.getByLabelText("What needs doing?")).toHaveValue("Second thought");
  });
});

describe("NewJobForm in a project", () => {
  it("gives a job to the project's lead by default, with no directory", async () => {
    const { makeProject } = await import("../../lib/projectFixtures");
    const calls = stubForm();
    renderWithClient(<NewJobForm project={makeProject()} />);
    expect(screen.getByRole("radio", { name: "To the lead" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("button", { name: /To Crowsnest's lead/ })).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("What needs doing?"), "Plan the release");
    await userEvent.click(screen.getByRole("button", { name: "Give job" }));
    await waitFor(() => expect(calls.some((call) => call.method === "POST")).toBe(true));
    expect(calls.find((call) => call.method === "POST")?.body).toEqual({ brief: "Plan the release", project_id: 1, to: "lead" });
  });

  it("as a job, prefills the project's directory and default role", async () => {
    const { makeProject } = await import("../../lib/projectFixtures");
    const calls = stubForm();
    renderWithClient(<NewJobForm project={makeProject()} />);
    await userEvent.click(screen.getByRole("radio", { name: "As a job" }));
    await userEvent.click(screen.getByRole("button", { name: /To crowsnest · builder/ }));
    expect(screen.getByLabelText(/Where/)).toHaveValue("/src/crowsnest");
    await userEvent.type(screen.getByLabelText("What needs doing?"), "Fix the flaky test");
    await userEvent.click(screen.getByRole("button", { name: "Give job" }));
    await waitFor(() => expect(calls.some((call) => call.method === "POST")).toBe(true));
    expect(calls.find((call) => call.method === "POST")?.body).toEqual({
      brief: "Fix the flaky test", project_id: 1, to: "job", dir: "/src/crowsnest", role: "builder", engine: "claude",
    });
  });

  it("says when the lead is off and will not start by itself", async () => {
    const { makeProjectList } = await import("../../lib/projectFixtures");
    stubForm();
    renderWithClient(<NewJobForm project={makeProjectList().projects[2]} />);
    expect(screen.getByText(/The lead is off; it holds the job once you start it/)).toBeInTheDocument();
  });
});
