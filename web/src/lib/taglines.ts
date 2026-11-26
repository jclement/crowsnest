// The taglines shared with the Go binary: one file, internal/taglines/taglines.txt, imported raw
// at build time so the login page and `crowsnest version` draw from the same set.
import raw from "../../../internal/taglines/taglines.txt?raw";

export const taglines: string[] = raw
  .split("\n")
  .map((line) => line.trim())
  .filter(Boolean);

export function randomTagline(): string {
  return taglines[Math.floor(Math.random() * taglines.length)] ?? "";
}
