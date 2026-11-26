import { describe, expect, it } from "vitest";
import { formatBytes, isLongPaste, looksLikeMarkdown, parseAttachedLines, pasteFile } from "./attachments";

describe("attachments", () => {
  it("decides when a paste is long enough to become a file", () => {
    expect(isLongPaste("x".repeat(2000))).toBe(false);
    expect(isLongPaste("x".repeat(2001))).toBe(true);
    expect(isLongPaste(Array.from({ length: 40 }, () => "a").join("\n"))).toBe(false);
    expect(isLongPaste(Array.from({ length: 41 }, () => "a").join("\n"))).toBe(true);
  });

  it("names a long paste by its shape and time", () => {
    const at = new Date(2026, 10, 26, 10, 2, 3);
    expect(pasteFile("plain log line\n", at).name).toBe("pasted-20261126-100203.txt");
    const markdown = pasteFile("# Heading\n\n- a\n", at);
    expect(markdown.name).toBe("pasted-20261126-100203.md");
    expect(markdown.type).toBe("text/markdown");
    expect(looksLikeMarkdown("see [the docs](https://x)")).toBe(true);
    expect(looksLikeMarkdown("error: exit 1")).toBe(false);
  });

  it("formats sizes as the server does", () => {
    expect([0, 812, 1024, 240 * 1024, 3_500_000, 1 << 20, 25 << 20, 1 << 30].map(formatBytes)).toEqual(["0 B", "812 B", "1 KB", "240 KB", "3.3 MB", "1 MB", "25 MB", "1 GB"]);
  });

  it("finds the files a typed message names, and only crowsnest's copies", () => {
    const id = "a_0123456789abcdef01234567";
    const text = [
      "what word is in this?",
      `Attached: /home/crow/.local/state/crowsnest/crowsnest/orchestrator/attachments/${id}-Screen-Shot.png (image/png, 240 KB)`,
      "Attached: /etc/passwd (text/plain, 2 KB)",
    ].join("\n");
    const parsed = parseAttachedLines(text);
    expect(parsed.refs).toEqual([
      { id, name: "Screen-Shot.png", path: `/home/crow/.local/state/crowsnest/crowsnest/orchestrator/attachments/${id}-Screen-Shot.png`, type: "image/png", sizeLabel: "240 KB" },
    ]);
    expect(parsed.text).toBe("what word is in this?\nAttached: /etc/passwd (text/plain, 2 KB)");
    expect(parseAttachedLines(`Attached: /s/${id}-a.txt (text/plain, 9 B)`).text).toBe("");
  });
});
