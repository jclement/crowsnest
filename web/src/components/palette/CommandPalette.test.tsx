import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { queryKeys } from "../../lib/api";
import { makeFleet } from "../../lib/fixtures";
import { makeJob } from "../../lib/jobFixtures";
import type { Job } from "../../lib/jobs";
import type { PaletteCommand } from "../../lib/palette";
import { makeQuestion } from "../../lib/questionFixtures";
import { renderWithClient, stubApi } from "../../test/render";
import { fixtureSnapshot } from "../../lib/attentionFixtures";
import { CommandPalette } from "./CommandPalette";

const jobs: Job[] = [
  makeJob({ id: "j_1", title: "Fix the login redirect", state: "running", pane_id: "w2:p1" }),
  makeJob({ id: "j_2", title: "Migrate the database", state: "running", pane_id: "" }),
];

const deployQuestion = makeQuestion({ id: "q_7", title: "Deploy to staging?", kind: "confirm", status: "open", options: [{ key: "y", label: "Yes" }, { key: "n", label: "No" }] });

function stub(jobList: Job[] = jobs) {
  return stubApi((call) => {
    if (call.path === "/jobs") return { body: { data: jobList } };
    if (call.path === "/attention") return { body: { data: fixtureSnapshot([], [deployQuestion]) } };
    if (call.path.startsWith("/questions")) {
      return {
        body: {
          data: [deployQuestion],
        },
      };
    }
    if (call.path === "/fleet") return { body: { data: makeFleet() } };
    if (call.path === "/auth/me") return { body: { data: { username: "jeff", features: { auth: true, voice: false, demo: true, session: "s" } } } };
    if (call.path === "/orchestrator") return { body: { data: { state: "idle", name: "orchestrator", pane_id: "w1:p1", session_id: "", dir: "", restarts: 0, updated_at: "" } } };
    return undefined;
  });
}

function setup(recents: string[] = []) {
  const run = vi.fn<(command: PaletteCommand) => void>();
  const onClose = vi.fn();
  const onRan = vi.fn();
  const inputRef = createRef<HTMLInputElement>();
  const rendered = renderWithClient(
    <CommandPalette inputRef={inputRef} onClose={onClose} run={run} recents={recents} onRan={onRan} mac speak={null} voiceSettings={false} />,
  );
  const input = screen.getByRole("combobox", { name: "Search or run a command" });
  return { ...rendered, run, onClose, onRan, input };
}

/** The rows' titles, without the subtitle, chip or the verb on the selected row. */
const titles = () => screen.getAllByRole("option").map((option) => option.querySelector("[data-part=title]")?.textContent);
/** The row whose text holds `text` (highlights split titles into several elements). */
const optionWith = (text: string) => screen.queryAllByRole("option").find((option) => option.textContent?.includes(text));
const selectedOption = () => screen.getAllByRole("option").find((option) => option.getAttribute("aria-selected") === "true");

afterEach(() => vi.unstubAllGlobals());

describe("CommandPalette", () => {
  it("is a combobox over a listbox, focused, with the first row active and the count announced", async () => {
    stub();
    const { input } = setup();
    expect(input).toHaveFocus();
    await waitFor(() => expect(screen.getByRole("group", { name: "Needs you" })).toBeInTheDocument());
    expect(input).toHaveAttribute("aria-controls", screen.getByRole("listbox").id);
    expect(input).toHaveAttribute("aria-activedescendant", selectedOption()?.id);
    expect(screen.getByRole("status")).toHaveTextContent(/\d+ results/);
  });

  it("moves with the arrows and Ctrl+N/P, wrapping, and runs the row on Enter", async () => {
    stub();
    const { input, run, onClose, onRan } = setup();
    await userEvent.type(input, "agents");
    const first = selectedOption();
    expect(first).toHaveTextContent("Agents");
    await userEvent.keyboard("{ArrowDown}");
    expect(selectedOption()).not.toBe(first);
    await userEvent.keyboard("{Control>}p{/Control}");
    expect(selectedOption()).toHaveTextContent("Agents");
    await userEvent.keyboard("{ArrowUp}");
    expect(selectedOption()).toBe(screen.getAllByRole("option").at(-1));
    await userEvent.keyboard("{Control>}n{/Control}{Enter}");
    expect(run).toHaveBeenCalledWith({ kind: "navigate", to: "/agents" });
    expect(onClose).toHaveBeenCalled();
    expect(onRan).toHaveBeenCalledWith("nav:/agents");
  });

  it("highlights the matched characters", async () => {
    stub();
    const { input } = setup();
    await userEvent.type(input, "inbx");
    const marks = within(selectedOption()!).getAllByText((_, element) => element?.tagName === "MARK");
    expect(marks.map((mark) => mark.textContent).join("")).toBe("Inbx");
  });

  it("offers Give job for free text and gives it on Enter", async () => {
    stub();
    const { input, run } = setup();
    await userEvent.type(input, "rewrite the onboarding emails for new users");
    expect(selectedOption()).toHaveTextContent("Give job: rewrite the onboarding emails for new users");
    await userEvent.keyboard("{Enter}");
    expect(run).toHaveBeenCalledWith({ kind: "giveJob", brief: "rewrite the onboarding emails for new users", open: false });
  });

  it("opens the full form with ⌘Enter on Give job", async () => {
    stub();
    const { input, run } = setup();
    await userEvent.type(input, "> tidy up");
    await userEvent.keyboard("{Meta>}{Enter}{/Meta}");
    expect(run).toHaveBeenCalledWith({ kind: "jobForm", brief: "tidy up" });
  });

  it("opens a job's actions with →, filters them, and goes back with Esc", async () => {
    stub();
    const { input, run, onClose } = setup();
    await waitFor(() => expect(screen.getByText("Fix the login redirect")).toBeInTheDocument());
    await userEvent.type(input, "login redirect");
    expect(selectedOption()).toHaveTextContent("Fix the login redirect");
    await userEvent.keyboard("{ArrowRight}");
    expect(screen.getByRole("listbox", { name: "Actions for j_1" })).toBeInTheDocument();
    expect(titles()).toEqual(["Open job", "Nudge", "Message its agent…", "Cancel job…"]);
    expect(input).toHaveValue("");
    await userEvent.type(input, "nud");
    expect(screen.getAllByRole("option")).toHaveLength(1);
    await userEvent.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();
    expect(input).toHaveValue("login redirect");
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalled();
    expect(run).not.toHaveBeenCalled();
  });

  it("asks before cancelling a job, in the job view's words", async () => {
    stub();
    const { input, run } = setup();
    await waitFor(() => expect(screen.getByText("Migrate the database")).toBeInTheDocument());
    await userEvent.type(input, "migrate");
    await userEvent.keyboard("{Tab}");
    await userEvent.type(input, "cancel");
    await userEvent.keyboard("{Enter}");
    expect(screen.getByText("Cancel j_2?")).toBeInTheDocument();
    expect(screen.getByText("Esc is sent to the agent; its pane stays open.")).toBeInTheDocument();
    // Backing out runs nothing and returns to the actions.
    await userEvent.keyboard("{ArrowDown}{Enter}");
    expect(run).not.toHaveBeenCalled();
    expect(screen.queryByText("Cancel j_2?")).not.toBeInTheDocument();
    expect(screen.getByRole("listbox", { name: "Actions for j_2" })).toBeInTheDocument();
    await userEvent.type(input, "cancel{Enter}");
    await userEvent.keyboard("{Enter}");
    expect(run).toHaveBeenCalledWith({ kind: "jobAction", id: "j_2", action: "cancel" });
  });

  it("messages a job's agent through a text level", async () => {
    stub();
    const { input, run, onRan } = setup();
    await waitFor(() => expect(screen.getByText("Fix the login redirect")).toBeInTheDocument());
    await userEvent.type(input, "login");
    await userEvent.keyboard("{ArrowRight}");
    await userEvent.type(input, "message{Enter}");
    expect(screen.getByRole("combobox", { name: "Message j_1" })).toHaveFocus();
    await userEvent.keyboard("{Enter}");
    expect(run).not.toHaveBeenCalled();
    await userEvent.type(input, "use the staging config{Enter}");
    expect(run).toHaveBeenCalledWith({ kind: "jobMessage", id: "j_1", text: "use the staging config" });
    expect(onRan).toHaveBeenCalledWith("job:j_1");
  });

  it("answers a confirm question from its options", async () => {
    stub();
    const { input, run } = setup();
    await waitFor(() => expect(screen.getByText("Deploy to staging?")).toBeInTheDocument());
    await userEvent.type(input, "deploy");
    await userEvent.keyboard("{ArrowRight}");
    expect(titles()).toEqual(["Open in Inbox", "Answer: Yes", "Answer: No"]);
    await userEvent.keyboard("{ArrowDown}{Enter}");
    expect(run).toHaveBeenCalledWith({ kind: "answer", questionId: "q_7", value: "y", label: "Yes" });
  });

  it("opens actions from a row's chevron and runs a row on click", async () => {
    stub();
    const { input, run } = setup();
    await waitFor(() => expect(screen.getByText("Deploy to staging?")).toBeInTheDocument());
    await userEvent.type(input, "deploy");
    await userEvent.click(within(selectedOption()!).getByTitle("More actions"));
    expect(screen.getByRole("listbox", { name: "Actions for q_7" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Back" }));
    await userEvent.click(optionWith("Deploy to staging?")!);
    expect(run).toHaveBeenCalledWith({ kind: "navigate", to: "/inbox", search: { q: "q_7" } });
  });

  it("keeps the selection on the same job when the board changes under it", async () => {
    stub();
    const { input, queryClient } = setup();
    await waitFor(() => expect(screen.getByText("Migrate the database")).toBeInTheDocument());
    await userEvent.type(input, "the");
    while (!selectedOption()?.textContent?.includes("Migrate the database")) await userEvent.keyboard("{ArrowDown}");
    act(() => {
      queryClient.setQueryData(queryKeys.jobList, [makeJob({ id: "j_3", title: "The newest job", state: "needs_you" }), ...jobs]);
    });
    await waitFor(() => expect(optionWith("The newest job")).toBeDefined());
    expect(selectedOption()).toHaveTextContent("Migrate the database");
    expect(input).toHaveAttribute("aria-activedescendant", selectedOption()?.id);
  });

  it("shows recent items first with no query", async () => {
    stub();
    setup(["nav:/agents"]);
    const recent = await screen.findByRole("group", { name: "Recent" });
    expect(within(recent).getByText("Agents")).toBeInTheDocument();
  });

  it("closes on the mobile Cancel button", async () => {
    stub();
    const { onClose } = setup();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalled();
  });
});
