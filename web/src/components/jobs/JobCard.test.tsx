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
    expect(screen.getByText("Stuck")).toBeInTheDocument();
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
