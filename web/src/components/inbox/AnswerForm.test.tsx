import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { fixtureQuestions } from "../../lib/questionFixtures";
import type { Question } from "../../lib/questions";
import { renderWithClient, stubApi, type Call } from "../../test/render";
import { AnswerForm } from "./AnswerForm";

const byId = (id: string) => fixtureQuestions.find((question) => question.id === id)!;

function setup(question: Question) {
  const calls = stubApi((call) => (call.method === "POST" ? { body: { data: { ...question, status: "answered" } } } : undefined));
  renderWithClient(<AnswerForm question={question} />);
  return { user: userEvent.setup(), calls };
}

const answers = (calls: Call[]) => calls.filter((call) => call.method === "POST").map((call) => ({ path: call.path, body: call.body }));

describe("AnswerForm", () => {
  it("confirm: one press answers, with the note, and the recommended option is marked", async () => {
    const question = byId("q_1");
    const { user, calls } = setup(question);
    const options = screen.getAllByRole("button").filter((button) => button.closest("ul"));
    expect(options.map((option) => option.textContent)).toEqual([expect.stringContaining("Apply it"), expect.stringContaining("Not yet")]);
    expect(options[0]).toHaveTextContent("Recommended");
    expect(options[1]).not.toHaveTextContent("Recommended");
    await user.type(screen.getByLabelText(/Note/), "after 10pm");
    await user.click(screen.getByRole("button", { name: /Not yet/ }));
    await waitFor(() => expect(answers(calls)).toEqual([{ path: "/questions/q_1/answer", body: { answer_value: "no", answer_note: "after 10pm" } }]));
  });

  it("single choice sends the key", async () => {
    const { user, calls } = setup(byId("q_3"));
    await user.click(screen.getByRole("button", { name: /GitHub releases/ }));
    await waitFor(() => expect(answers(calls)[0]?.body).toEqual({ answer_value: "releases" }));
  });

  it("multi-select: checkboxes, Submit disabled until one is picked, then an array", async () => {
    const { user, calls } = setup(byId("q_2"));
    const submit = screen.getByRole("button", { name: /Submit/ });
    expect(submit).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: /Data race/ }));
    await user.click(screen.getByRole("checkbox", { name: /Rename helpers/ }));
    expect(submit).toHaveTextContent("Submit (2)");
    await user.click(submit);
    await waitFor(() => expect(answers(calls)[0]?.body).toEqual({ answer_value: ["race", "names"] }));
  });

  it("manual: free text is required and sent as the note", async () => {
    const { user, calls } = setup(byId("q_4"));
    const send = screen.getByRole("button", { name: "Send answer" });
    expect(send).toBeDisabled();
    await user.type(screen.getByLabelText("Your answer"), "Crowsnest runs your agents.");
    await user.click(send);
    await waitFor(() => expect(answers(calls)[0]?.body).toEqual({ answer_note: "Crowsnest runs your agents." }));
  });

  it("ack is one button", async () => {
    const { user, calls } = setup(byId("q_5"));
    await user.click(screen.getByRole("button", { name: "Acknowledge" }));
    await waitFor(() => expect(answers(calls)[0]?.body).toEqual({}));
  });

  it("notice is dismissed", async () => {
    const { user, calls } = setup(byId("q_6"));
    await user.click(screen.getByRole("button", { name: "Dismiss" }));
    await waitFor(() => expect(answers(calls)).toHaveLength(1));
  });

  it("says so when the question was answered elsewhere", async () => {
    stubApi((call) => (call.method === "POST" ? { status: 409, body: { error: { code: "CONFLICT", message: "gone" } } } : undefined));
    renderWithClient(<AnswerForm question={byId("q_5")} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "Acknowledge" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("already answered");
  });
});
