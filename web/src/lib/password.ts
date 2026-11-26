// A password strength hint for the setup form and the Account sheet. The server's only rule is a
// minimum length (no composition rules: they make passwords worse); this estimate exists to nudge
// toward longer passwords, never to refuse one. It counts length and the variety of characters,
// and marks down repeats and the username, which is all a hint needs.

export type Strength = "short" | "weak" | "fair" | "strong";

export interface StrengthHint {
  strength: Strength;
  label: string;
  hint: string;
  /** 0 (short) to 3 (strong), for the meter. */
  score: 0 | 1 | 2 | 3;
}

/** A rough entropy estimate in bits: length times log2 of the character pool in use. */
export function estimateBits(password: string): number {
  if (!password) return 0;
  let pool = 0;
  if (/[a-z]/.test(password)) pool += 26;
  if (/[A-Z]/.test(password)) pool += 26;
  if (/[0-9]/.test(password)) pool += 10;
  if (/[^A-Za-z0-9]/.test(password)) pool += 33;
  const chars = [...password];
  // Repeated characters add little: count each distinct character once, plus a third of repeats.
  const distinct = new Set(chars).size;
  const effective = distinct + (chars.length - distinct) / 3;
  return effective * Math.log2(Math.max(pool, 2));
}

export function passwordStrength(password: string, username: string, minLength: number): StrengthHint {
  const length = [...password].length;
  if (length < minLength) {
    return { strength: "short", score: 0, label: "Too short", hint: `At least ${minLength} characters. A few unrelated words make a good one.` };
  }
  if (username && password.toLowerCase().includes(username.toLowerCase())) {
    return { strength: "weak", score: 1, label: "Weak", hint: "It contains the username. Pick something nobody could guess from it." };
  }
  const bits = estimateBits(password);
  if (bits < 55) return { strength: "weak", score: 1, label: "Weak", hint: "Longer is stronger: add another word or two." };
  if (bits < 75) return { strength: "fair", score: 2, label: "Fair", hint: "Good enough. One more word would make it strong." };
  return { strength: "strong", score: 3, label: "Strong", hint: "Strong." };
}
