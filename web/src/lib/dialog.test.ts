import { describe, expect, it } from "vitest";
import { riskReason } from "./dialog";

describe("riskReason", () => {
  it.each([
    "Yes, and don't ask again for git push commands",
    "Yes, and do not ask again this session",
    "Always allow Bash(rm:*)",
    "Allow all edits",
    "Run git push --force origin main",
    "git push -f",
    "force-push the branch",
    "rm -rf node_modules",
    "rm -fr build",
    "git reset --hard HEAD~3",
    "DROP TABLE users",
    "git clean -fdx",
    "Bypass permissions for this session",
  ])("flags %j", (text) => {
    expect(riskReason(text)).not.toBeNull();
  });

  it.each(["Yes", "No, and tell Claude what to do differently (esc)", "Yes, allow reading this file", "git push -u origin feature/x", "rm notes.txt"])(
    "leaves %j alone",
    (text) => {
      expect(riskReason(text)).toBeNull();
    },
  );
});
