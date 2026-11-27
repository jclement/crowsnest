// useDiffFile caches a file's body forever under its hash, so it must be that version's body
// (R7-C12): the request names the hash, and an answer for another hash is not cached; the summary
// is read again instead.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubApi } from "../test/render";
import { changesKeys, useDiffFile } from "./changes-queries";

afterEach(() => vi.unstubAllGlobals());

const options = { context: 0, ignoreWhitespace: false, since: false };

function wrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useDiffFile", () => {
  it("asks for the hash it caches under, and does not cache a body of another version", async () => {
    const calls = stubApi((call) => {
      if (call.path.startsWith("/changes/jobs/j_7/file")) {
        // The agent edited the file since the list was read: the server answers version B.
        return { body: { data: { path: "a.ts", status: "modified", hash: "hash-B", diff: "", binary: false, too_large: false, cut: false, old_text: null, new_text: null, since: false, context: 0 } } };
      }
      if (call.path.startsWith("/changes/jobs/j_7")) return { body: { data: null } };
      return undefined;
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const who = { kind: "job", id: "j_7" } as const;
    const { result } = renderHook(() => useDiffFile(who, "a.ts", "hash-A", "all", options, "", true), { wrapper: wrapper(client) });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(calls[0]?.path).toContain("hash=hash-A");
    expect(client.getQueryData(changesKeys.file(who, "a.ts", "hash-A", "all", options))).toBeUndefined();
  });

  it("keys a since-viewed diff by when the file was viewed", () => {
    const who = { kind: "job", id: "j_7" } as const;
    const first = changesKeys.file(who, "a.ts", "h", "all", { ...options, since: true, baseline: "2026-11-27T10:00:00Z" });
    const later = changesKeys.file(who, "a.ts", "h", "all", { ...options, since: true, baseline: "2026-11-27T11:00:00Z" });
    expect(first).not.toEqual(later);
  });
});
