import { describe, expect, it } from "vitest";
import { queryKeys } from "./api";
import { invalidations } from "./live";
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
