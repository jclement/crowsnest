import { describe, expect, it } from "vitest";
import { cadenceProblem, cadenceToEvery, everyToCadence, nameProblem, targetOf, targetToInput, type Cadence } from "./schedule";

describe("the cadence picker", () => {
  const cases: Array<[Cadence, string, string]> = [
    [{ kind: "minutes", count: 15 }, "15m", ""],
    [{ kind: "hours", count: 4 }, "4h", ""],
    [{ kind: "days", count: 3 }, "3d", ""],
    [{ kind: "daily", at: "18:00" }, "1d", "18:00"],
    [{ kind: "poke" }, "poke", ""],
    [{ kind: "change" }, "change", ""],
  ];

  it.each(cases)("maps %j to every %s after %s, and back", (cadence, every, after) => {
    expect(cadenceToEvery(cadence)).toEqual({ every, after });
    expect(everyToCadence(every, after)).toEqual(cadence);
  });

  it("reads 1d with no time as every 1 day, and anything unreadable as hourly", () => {
    expect(everyToCadence("1d")).toEqual({ kind: "days", count: 1 });
    expect(everyToCadence("2d", "09:00")).toEqual({ kind: "days", count: 2 });
    expect(everyToCadence("1w")).toEqual({ kind: "hours", count: 1 });
    expect(everyToCadence(" POKE ")).toEqual({ kind: "poke" });
  });

  it("refuses what the server would: under five minutes, zero, a bad time", () => {
    expect(cadenceProblem({ kind: "minutes", count: 4 })).toMatch(/5 minutes/);
    expect(cadenceProblem({ kind: "minutes", count: 5 })).toBeNull();
    expect(cadenceProblem({ kind: "hours", count: 0 })).not.toBeNull();
    expect(cadenceProblem({ kind: "daily", at: "24:00" })).not.toBeNull();
    expect(cadenceProblem({ kind: "daily", at: "7:30" })).toBeNull();
    expect(cadenceProblem({ kind: "poke" })).toBeNull();
  });
});

describe("the target picker", () => {
  it("maps the orchestrator, a lead and a job onto as and project_id", () => {
    expect(targetToInput("orchestrator", 3)).toEqual({ as: "poke", project_id: 0 });
    expect(targetToInput("lead", 3)).toEqual({ as: "poke", project_id: 3 });
    expect(targetToInput("job", 3)).toEqual({ as: "job", project_id: 3 });
  });

  it("reads a task's target, also from servers that do not send one", () => {
    expect(targetOf({ target: "lead", as: "poke", project_id: 2 })).toBe("lead");
    expect(targetOf({ as: "job" })).toBe("job");
    expect(targetOf({ as: "poke", project_id: 2 })).toBe("lead");
    expect(targetOf({ as: "poke" })).toBe("orchestrator");
  });
});

describe("schedule names", () => {
  it("are lower-case file-name words", () => {
    expect(nameProblem("evening-review")).toBeNull();
    expect(nameProblem("")).not.toBeNull();
    expect(nameProblem("Evening Review")).not.toBeNull();
    expect(nameProblem("-x")).not.toBeNull();
  });
});
