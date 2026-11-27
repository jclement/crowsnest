import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { makeJob } from "../../lib/jobFixtures";
import { JobCard } from "./JobCard";

function renderCard(job = makeJob(), onSelect = vi.fn()) {
  render(
    <ul>
      <JobCard job={job} onSelect={onSelect} />
    </ul>,
  );
  return onSelect;
}

describe("JobCard", () => {
  it("shows title, state, what it is doing, activity and the agent", async () => {
    const onSelect = renderCard(makeJob({ last_activity_at: new Date(Date.now() - 2 * 60_000).toISOString() }));
    expect(screen.getByText("Add retry back-off to the webhook sender")).toBeInTheDocument();
    expect(screen.getByText("Running")).toBeInTheDocument();
    expect(screen.getByText("Writing table tests")).toBeInTheDocument();
    expect(screen.getByText("active 2 min ago")).toBeInTheDocument();
    expect(screen.getByText("job-1")).toBeInTheDocument();
    expect(screen.queryByText(/nudge/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button"));
    expect(onSelect).toHaveBeenCalledWith("j_1");
  });

  it("shows nudges, open questions and a retry countdown when there are any", () => {
    renderCard(makeJob({ nudges: 2, open_questions: 1, stuck: true, cause: "limit", cause_text: "Hit a usage or rate limit", retry_at: new Date(Date.now() + 4 * 60_000 + 5000).toISOString() }));
    // One chip, what happens next: crowsnest retries the limit by itself.
    expect(screen.getByText("Retrying")).toBeInTheDocument();
    expect(screen.getByText("2 nudges")).toBeInTheDocument();
    expect(screen.getByText("1 question")).toBeInTheDocument();
    expect(screen.getByText("Retry in 4 min")).toBeInTheDocument();
    expect(screen.getByText("Hit a usage or rate limit")).toBeInTheDocument();
  });

  it("shows how a finished job ended", () => {
    renderCard(makeJob({ state: "failed", fail_reason: "Not released yet." }));
    expect(screen.getByText("Failed")).toBeInTheDocument();
    expect(screen.getByText("Not released yet.")).toBeInTheDocument();
  });
});

describe("JobCard: acceptance, budgets and waits (DESIGN.md 6.15 to 6.18)", () => {
  it("shows one chip saying what happens next, not the state and the check apart (R8)", () => {
    const { unmount } = render(
      <ul>
        <JobCard job={makeJob({ state: "done", accept_state: "accepted", result_md: "Added it" })} onSelect={vi.fn()} />
        <JobCard job={makeJob({ id: "j_2", state: "done", accept_state: "unverified", result_md: "Added it" })} onSelect={vi.fn()} />
        <JobCard job={makeJob({ id: "j_3", state: "running", accept_state: "failed", check_rounds: 1 })} onSelect={vi.fn()} />
      </ul>,
    );
    expect(screen.getAllByText("Ready to land")).toHaveLength(2);
    expect(screen.getAllByText("Ready to land")[0]).toHaveAttribute("title", "Next: Ready to land. Waiting on you.");
    expect(screen.getByText("Fixing check")).toBeInTheDocument();
    // The facts (Accepted, Unverified, the round) are the detail's, not the card's.
    expect(screen.queryByText("Accepted")).toBeNull();
    expect(screen.queryByText("Unverified")).toBeNull();
    expect(screen.queryByText("Done")).toBeNull();
    unmount();
  });

  it("shows what a stuck or waiting agent last said, who owns the wait, and its budget", () => {
    renderCard(
      makeJob({
        state: "blocked",
        blocked_reason: "staging is down",
        wake: "rechecks at 14:10",
        wait_owner: "job-1",
        last_message: "Staging returns 503; I will look again at 14:10.",
        budget: {
          limits: { usd: 2, tokens: 0, minutes: 0, turns: 0, source: {}, fleet: false },
          used: { usd: 1.8, tokens: 1, minutes: 1, turns: 1, measured: true, children: 0 },
          share: 0.9,
          over: "usd",
          state: "warned",
          detail: "",
        },
      }),
    );
    expect(screen.getByText(/Waits on job-1: rechecks at 14:10/)).toBeInTheDocument();
    expect(screen.getByRole("note", { name: "What the agent last said" })).toHaveTextContent("Staging returns 503");
    expect(screen.getByText("Budget 90%")).toBeInTheDocument();
  });

  it("shows a job held by the fleet's usage limit as held, not stuck", () => {
    renderCard(makeJob({ stuck: true, cause: "limit", cause_text: "Hit a usage or rate limit", cause_detail: "claude's usage limit resets at 15:00" }));
    expect(screen.getByText("Held: usage limit")).toBeInTheDocument();
    expect(screen.queryByText("Stuck")).not.toBeInTheDocument();
  });
});
