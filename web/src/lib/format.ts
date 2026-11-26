// Small pure helpers shared by components (kept out of component files so React Fast Refresh
// can hot-swap those).

/** Formats the server version: "dev" builds show DEV, a bare semver gains a leading v, and
 * anything else (a tag, or a commit hash from an untagged build) is shown as is. */
export function formatVersion(version: string | undefined): string {
  if (!version) return "";
  if (version === "dev") return "DEV";
  return /^\d+\.\d+/.test(version) ? `v${version}` : version;
}

/** Keeps a post-login redirect on this site: a path, not "//evil.example" or a URL. */
export function safeRedirect(target: string | undefined): string {
  if (!target || !target.startsWith("/") || target.startsWith("//") || target.startsWith("/login")) return "/";
  return target;
}

/** "just now", "5 min ago", "in 3 h", "2 d ago": a compact distance from now for a timestamp. */
export function relativeTime(iso: string, now: number = Date.now()): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return "";
  const seconds = Math.round((then - now) / 1000);
  const abs = Math.abs(seconds);
  if (abs < 45) return "just now";
  const [value, unit] =
    abs < 3600 ? [Math.round(abs / 60), "min"] : abs < 86_400 ? [Math.round(abs / 3600), "h"] : [Math.round(abs / 86_400), "d"];
  return seconds < 0 ? `${value} ${unit} ago` : `in ${value} ${unit}`;
}
