import { afterEach, describe, expect, it } from "vitest";
import { passwordStrength } from "./password";
import { captureSetupToken, resetCapturedSetupToken } from "./setupToken";

describe("passwordStrength", () => {
  it("grades by length and variety, never by rules", () => {
    expect(passwordStrength("short", "jeff", 12).strength).toBe("short");
    expect(passwordStrength("aaaaaaaaaaaa", "jeff", 12).strength).toBe("weak");
    expect(passwordStrength("jeff-is-my-name", "jeff", 12).strength).toBe("weak");
    expect(passwordStrength("correct horse battery", "jeff", 12).strength).not.toBe("short");
    expect(passwordStrength("correct horse battery staple glue", "jeff", 12).strength).toBe("strong");
  });
});

describe("captureSetupToken", () => {
  afterEach(() => {
    resetCapturedSetupToken();
    window.history.replaceState(null, "", "/");
  });

  it("takes the token out of the fragment once and keeps it", () => {
    window.history.replaceState(null, "", "/setup#token=abc_DEF-123");
    expect(captureSetupToken()).toBe("abc_DEF-123");
    expect(window.location.hash).toBe("");
    expect(window.location.pathname).toBe("/setup");
    expect(captureSetupToken()).toBe("abc_DEF-123");
  });

  it("is empty without a token", () => {
    window.history.replaceState(null, "", "/setup");
    expect(captureSetupToken()).toBe("");
  });
});
