import { beforeEach, describe, expect, it } from "vitest";
import { LATE_REQUEST_MS, STREAM_SILENT_MS, noteBeat, oldestPendingMs, resetPending, serverStalled, trackRequest } from "./pending";

describe("pending requests and the stream's silence (R10 Fable B3)", () => {
  beforeEach(() => resetPending(1_000));

  it("is not stalled while requests settle and the stream beats", () => {
    const done = trackRequest(() => 1_000);
    expect(serverStalled(1_000 + LATE_REQUEST_MS)).toBe(false);
    done();
    expect(oldestPendingMs(50_000)).toBe(0);
  });

  it("is stalled when a request waits past the limit, though the stream still beats", () => {
    trackRequest(() => 1_000);
    noteBeat(1_000 + LATE_REQUEST_MS);
    expect(serverStalled(1_000 + LATE_REQUEST_MS + 1)).toBe(true);
  });

  it("is stalled when the open stream goes silent (a frozen server keeps the socket up)", () => {
    expect(serverStalled(1_000 + STREAM_SILENT_MS)).toBe(false);
    expect(serverStalled(1_000 + STREAM_SILENT_MS + 1)).toBe(true);
    noteBeat(1_000 + STREAM_SILENT_MS + 2);
    expect(serverStalled(1_000 + STREAM_SILENT_MS + 3)).toBe(false);
  });
});
