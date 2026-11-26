// Risky dialog options (DESIGN.md section 8.2). Jeff reads the real screen before pressing, so
// nothing is refused; an option that widens permissions for good or destroys work gets a
// warning badge so it is not pressed on autopilot. The list is the front door's refuse and
// deny lists turned into patterns; add to it when a new phrasing turns up.

interface RiskRule {
  pattern: RegExp;
  reason: string;
}

const riskRules: RiskRule[] = [
  { pattern: /\balways allow\b/i, reason: "Grants this permission for good" },
  { pattern: /\b(don'?t|do not) ask again\b/i, reason: "Stops asking for this in future" },
  { pattern: /\ballow all\b/i, reason: "Grants a broad permission" },
  { pattern: /\bbypass(es)? permissions?\b/i, reason: "Turns permission checks off" },
  { pattern: /\bforce[- ]?push\b|\bpush\b[^\n]*\s(--force(-with-lease)?|-f)\b/i, reason: "Rewrites remote history" },
  { pattern: /\brm\s+-[a-z]*r[a-z]*f|\brm\s+-[a-z]*f[a-z]*r/i, reason: "Deletes files recursively" },
  { pattern: /\breset\s+--hard\b/i, reason: "Discards local changes" },
  { pattern: /\b(drop|truncate)\s+(table|database|schema)\b/i, reason: "Destroys data" },
  { pattern: /\bgit\s+clean\s+-[a-z]*f/i, reason: "Deletes untracked files" },
];

/** Why an option's text looks risky, or null when it does not. */
export function riskReason(text: string): string | null {
  return riskRules.find((rule) => rule.pattern.test(text))?.reason ?? null;
}
