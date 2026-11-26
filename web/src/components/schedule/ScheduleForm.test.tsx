import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { makeProjectList } from "../../lib/projectFixtures";
import type { ScheduleTask } from "../../lib/schedule";
import { renderWithClient, stubApi } from "../../test/render";
import { ScheduleForm } from "./ScheduleForm";

afterEach(() => vi.unstubAllGlobals());

const projects = makeProjectList().projects;

function serve() {
  stubApi((call) => {
    if (call.path === "/roles") return { body: { data: [{ name: "tester", summary: "Tests", custom: false }] } };
    if (call.path === "/dirs") return { body: { data: [] } };
    return undefined;
  });
}

describe("ScheduleForm", () => {
  it("makes a daily poke for a project's lead: daily at → every 1d after HH:MM", async () => {
    serve();
    const onSubmit = vi.fn();
    renderWithClient(<ScheduleForm projects={projects} defaultProjectId={2} pending={false} onSubmit={onSubmit} onCancel={() => {}} />);
    expect(screen.getByRole("radio", { name: "A project's lead" })).toBeChecked();
    await userEvent.type(screen.getByLabelText("Name"), "evening-review");
    await userEvent.clear(screen.getByLabelText("Time of day"));
    await userEvent.type(screen.getByLabelText("Time of day"), "18:30");
    await userEvent.type(screen.getByRole("textbox", { name: /What to tell the agent/ }), "Review the day.");
    await userEvent.click(screen.getByRole("button", { name: "Create schedule" }));
    expect(onSubmit).toHaveBeenCalledWith(
      { name: "evening-review", every: "1d", after: "18:30", as: "poke", project_id: 2, body_md: "Review the day.", dir: "", role: "", engine: "" },
      true,
    );
  });

  it("makes a job every N minutes with a role, and does not send under five minutes", async () => {
    serve();
    const onSubmit = vi.fn();
    renderWithClient(<ScheduleForm projects={projects} pending={false} onSubmit={onSubmit} onCancel={() => {}} />);
    await userEvent.type(screen.getByLabelText("Name"), "lint");
    await userEvent.selectOptions(screen.getByLabelText("Cadence"), "minutes");
    await userEvent.clear(screen.getByLabelText("Minutes"));
    await userEvent.type(screen.getByLabelText("Minutes"), "3");
    await userEvent.click(screen.getByRole("radio", { name: "A job" }));
    await userEvent.selectOptions(screen.getByLabelText("Project", { selector: "select" }), "1");
    await screen.findByRole("option", { name: "tester" });
    await userEvent.selectOptions(screen.getByLabelText("Role"), "tester");
    await userEvent.type(screen.getByRole("textbox", { name: /brief/ }), "Run the linters.");
    await userEvent.click(screen.getByRole("button", { name: "Create schedule" }));
    // The browser's own check (min 5) or the form's stops it either way.
    expect(onSubmit).not.toHaveBeenCalled();
    await userEvent.clear(screen.getByLabelText("Minutes"));
    await userEvent.type(screen.getByLabelText("Minutes"), "30");
    await userEvent.click(screen.getByRole("button", { name: "Create schedule" }));
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ name: "lint", every: "30m", after: "", as: "job", project_id: 1, role: "tester" }), true);
  });

  it("edits an app schedule with its name fixed and its cadence read back", () => {
    serve();
    const task = { name: "link-check", every: "4h", as: "job", target: "job", project_id: 2, body_md: "# Links", enabled: false, origin: "app" } as ScheduleTask;
    renderWithClient(<ScheduleForm task={task} projects={projects} pending={false} onSubmit={() => {}} onCancel={() => {}} />);
    expect(screen.queryByRole("textbox", { name: "Name" })).not.toBeInTheDocument();
    expect(screen.getByText("link-check")).toBeInTheDocument();
    expect(screen.getByLabelText("Cadence")).toHaveValue("hours");
    expect(screen.getByLabelText("Hours")).toHaveValue(4);
    expect(screen.getByRole("radio", { name: "A job" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Enabled" })).not.toBeChecked();
  });
});
