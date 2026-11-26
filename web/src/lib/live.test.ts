import { describe, expect, it } from "vitest";
import { queryKeys } from "./api";
import { invalidations, isFleet, payloads } from "./live";
import { makeFleet } from "./fixtures";
import { eventNames } from "./types";

describe("invalidations", () => {
  it("has an entry for every event name", () => {
    expect(Object.keys(invalidations).sort()).toEqual([...eventNames].sort());
  });

  it("refetches everything on hello and the fleet on fleet", () => {
    expect(invalidations.hello).toBeNull();
    expect(invalidations.fleet).toContainEqual(queryKeys.fleet);
    expect(invalidations.job).toContainEqual(queryKeys.jobs);
  });
});

describe("payloads", () => {
  it("writes a fleet event's data straight into the fleet query", () => {
    expect(payloads.fleet?.key).toEqual(queryKeys.fleet);
    expect(isFleet(makeFleet())).toBe(true);
  });

  it("refuses data that is not a whole fleet, so the fallback refetch runs", () => {
    expect(isFleet({ changed: true })).toBe(false);
    expect(isFleet(null)).toBe(false);
    expect(isFleet({ workspaces: [], agents: [] })).toBe(false);
  });
});
