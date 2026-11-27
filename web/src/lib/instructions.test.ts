import { describe, expect, it } from "vitest";
import { sourceBadge } from "./instructions";

describe("sourceBadge", () => {
  it("says loudly that a role file is unread or changed on disk (R5-S14)", () => {
    expect(sourceBadge({ scope: "role", source: "file", file_changed: true })).toEqual({ label: "from file: changed, review", tone: "danger" });
    expect(sourceBadge({ scope: "role", source: "file", file_changed: false })).toEqual({ label: "from file", tone: "warning" });
  });
});
