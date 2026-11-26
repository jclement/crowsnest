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
