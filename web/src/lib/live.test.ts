import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { queryKeys } from "./api";
import { invalidate, invalidations, isFleet, payloads } from "./live";
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

describe("invalidate", () => {
  // A change that lands while a query's first fetch is in flight: TanStack alone folds the
  // invalidation into that fetch, so the screen kept what the fetch had read (CI: a job's timeline
  // without "queued → running" for 30 s).
  it("fetches again once a first fetch that was in flight settles", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const releases: Array<(value: number) => void> = [];
    const queryFn = vi.fn(() => new Promise<number>((resolve) => releases.push(resolve)));
    const observer = new QueryObserver(client, { queryKey: ["jobs", "one", "j_1"], queryFn });
    const unsubscribe = observer.subscribe(() => {});
    expect(queryFn).toHaveBeenCalledTimes(1);

    invalidate(client, ["jobs"]);
    invalidate(client, ["jobs"]); // a second event meanwhile still means one more fetch
    releases.shift()?.(1); // the first fetch answers with what it read before the change
    await vi.waitFor(() => expect(queryFn).toHaveBeenCalledTimes(2));
    releases.shift()?.(2);
    await vi.waitFor(() => expect(client.getQueryData(["jobs", "one", "j_1"])).toBe(2));
    expect(queryFn).toHaveBeenCalledTimes(2);
    unsubscribe();
  });

  it("leaves a query that has data to TanStack's own cancel-and-refetch", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    let n = 0;
    const queryFn = vi.fn(async () => ++n);
    const observer = new QueryObserver(client, { queryKey: ["jobs", "list"], queryFn });
    const unsubscribe = observer.subscribe(() => {});
    await vi.waitFor(() => expect(client.getQueryData(["jobs", "list"])).toBe(1));
    invalidate(client, ["jobs"]);
    await vi.waitFor(() => expect(client.getQueryData(["jobs", "list"])).toBe(2));
    expect(queryFn).toHaveBeenCalledTimes(2);
    unsubscribe();
  });
});
