// The setup token arrives in the URL fragment (/setup#token=…): a browser never sends a fragment
// to the server, so the token stays out of request logs, proxies and Referer headers. It is
// taken out of the address bar as soon as the app starts (main.tsx), before anything renders or
// navigates, and kept only in memory for the setup form to prefill.

let captured: string | null = null;

/** Reads the token from the fragment once and removes it from the URL. Safe to call again. */
export function captureSetupToken(): string {
  if (captured !== null) return captured;
  captured = "";
  if (typeof window === "undefined") return captured;
  const hash = window.location.hash.replace(/^#/, "");
  const token = new URLSearchParams(hash).get("token");
  if (token) {
    captured = token;
    window.history.replaceState(window.history.state, "", window.location.pathname + window.location.search);
  }
  return captured;
}

/** For tests: forget what was captured. */
export function resetCapturedSetupToken(): void {
  captured = null;
}
