import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { agentStatuses } from "../../lib/types";
import { StatusChip, StatusDot } from "./Chip";

describe("StatusDot", () => {
  it("names every state accessibly", () => {
    render(
      <>
        {agentStatuses.map((status) => (
          <StatusDot key={status} status={status} />
        ))}
      </>,
    );
    for (const name of ["Blocked", "Working", "Done", "Idle", "Unknown"]) {
      expect(screen.getByRole("img", { name })).toBeInTheDocument();
    }
  });

  it("gives every state its own shape, not just a colour", () => {
    const { container } = render(
      <>
        {agentStatuses.map((status) => (
          <StatusDot key={status} status={status} />
        ))}
      </>,
    );
    const shapes = agentStatuses.map((status) => {
      const dot = container.querySelector(`[data-status="${status}"]`)!;
      // The shape is everything about the marker except its colour.
      return Array.from(dot.querySelectorAll("span"))
        .map((span) => span.className.replace(/\b(bg|border)-status-\w+/g, ""))
        .join("|");
    });
    expect(new Set(shapes).size).toBe(agentStatuses.length);
  });

  it("pulses only while working", () => {
    const { container } = render(
      <>
        <StatusDot status="working" />
        <StatusDot status="idle" />
      </>,
    );
    expect(container.querySelector('[data-status="working"] .animate-pulse-ring')).not.toBeNull();
    expect(container.querySelector('[data-status="idle"] .animate-pulse-ring')).toBeNull();
  });
});

describe("StatusChip", () => {
  it("shows the state as a word", () => {
    render(<StatusChip status="blocked" />);
    expect(screen.getByText("Blocked")).toBeInTheDocument();
  });
});
