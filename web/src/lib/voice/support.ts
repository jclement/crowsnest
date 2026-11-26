// Can this page use the microphone, and what to tell the human when it cannot. Browsers give
// getUserMedia only to a secure context (HTTPS, or localhost): plain HTTP on a tailnet address
// has no microphone at all, and the fix is `tailscale serve` (DESIGN.md 4.13, 13).

export type MicSupport = "ok" | "insecure" | "unsupported";

/** Whether recording is possible on this page. */
export function micSupport(): MicSupport {
  if (typeof window === "undefined") return "unsupported";
  if (!window.isSecureContext) return "insecure";
  if (!navigator.mediaDevices?.getUserMedia || typeof window.MediaRecorder === "undefined") return "unsupported";
  return "ok";
}

export const insecureNotice =
  "The microphone needs a secure page: this one is plain HTTP on a network address, so the browser offers no microphone. Open crowsnest over HTTPS (for example `tailscale serve`) or on localhost. Spoken replies still work.";

export const unsupportedNotice = "This browser cannot record audio here. Spoken replies still work.";

/** A sentence for a getUserMedia or MediaRecorder failure. */
export function micErrorMessage(error: unknown): string {
  const name = error instanceof DOMException || error instanceof Error ? error.name : "";
  switch (name) {
    case "NotAllowedError":
    case "SecurityError":
      return "Microphone access is blocked. Allow the microphone for this site in the browser's settings (the icon by the address bar), then try again.";
    case "NotFoundError":
    case "OverconstrainedError":
      return "No microphone was found. Connect one, or check the system's sound settings.";
    case "NotReadableError":
    case "AbortError":
      return "The microphone is busy or failed to start. Close other apps using it and try again.";
    default:
      return "Could not start the microphone. Try again.";
  }
}

/**
 * The recording format: what MediaRecorder offers here that ElevenLabs accepts as is. Chrome and
 * Firefox record Opus in WebM, Safari AAC in MP4; nothing is transcoded. Empty means the browser
 * default.
 */
export function recordingType(): string {
  if (typeof MediaRecorder === "undefined" || typeof MediaRecorder.isTypeSupported !== "function") return "";
  for (const type of ["audio/webm;codecs=opus", "audio/webm", "audio/mp4", "audio/ogg;codecs=opus"]) {
    if (MediaRecorder.isTypeSupported(type)) return type;
  }
  return "";
}
