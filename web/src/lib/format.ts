// Small pure helpers shared by components (kept out of component files so React Fast Refresh
// can hot-swap those).

/** Formats the server version: "dev" builds show DEV, tags keep their leading v. */
export function formatVersion(version: string | undefined): string {
  if (!version) return "";
  if (version === "dev") return "DEV";
  return version.startsWith("v") ? version : `v${version}`;
}

/** Keeps a post-login redirect on this site: a path, not "//evil.example" or a URL. */
export function safeRedirect(target: string | undefined): string {
  if (!target || !target.startsWith("/") || target.startsWith("//") || target.startsWith("/login")) return "/";
  return target;
}
