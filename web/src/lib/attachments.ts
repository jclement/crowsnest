// Attachments (DESIGN.md 8.6): the types mirroring internal/attach, the upload (XMLHttpRequest,
// because fetch cannot report upload progress), the long-paste rule, and the parser that turns
// the "Attached: <path> (type, size)" lines crowsnest types into an agent back into chips.
//
// The server never serves a path: a typed line's file is found again by the attachment id at the
// start of its copy's file name (<state>/…/attachments/a_<24 hex>-<name>), and fetched through
// /attachments/{id}, which decides what is shown inline.
import { ApiError, api, fixturesEnabled } from "./api";

/** One attachment as GET /attachments/{id}/meta and the lists return it. */
export interface Attachment {
  id: string;
  name: string;
  /** The sniffed media type (never what the browser claimed). */
  type: string;
  size: number;
  sha256: string;
  /** The web app may show it as a picture (a raster image type; never SVG). */
  image: boolean;
  /** A server-made thumbnail exists. */
  thumb: boolean;
  /** GET serves it for display rather than as a download. */
  inline: boolean;
  origin: "human" | "agent";
  owner_kind?: string;
  owner_id?: string;
  role?: string;
  ref?: string;
  /** The agent's copy, when one was made. */
  path?: string;
  created_at: string;
  released_at?: string;
}

/** features.attachments from /auth/me. */
export interface AttachmentLimits {
  max_bytes: number;
  max_files: number;
}

/** A pasted text longer than this many characters becomes a file rather than terminal input. */
export const LONG_PASTE_CHARS = 2000;
/** …or longer than this many lines. */
export const LONG_PASTE_LINES = 40;

const API_BASE = "/api/v1";

export function attachmentUrl(id: string, download = false): string {
  return `${API_BASE}/attachments/${encodeURIComponent(id)}${download ? "?download=1" : ""}`;
}

export function thumbUrl(id: string): string {
  return `${API_BASE}/attachments/${encodeURIComponent(id)}/thumb`;
}

/** "812 B", "240 KB", "3.4 MB": the server's FormatSize, so a chip and a typed line agree. */
export function formatBytes(size: number): string {
  const unit = 1024;
  if (size < unit) return `${size} B`;
  if (size < unit * unit) return `${Math.round(size / unit)} KB`;
  const trim = (value: number) => value.toFixed(1).replace(/\.0$/, "");
  if (size < unit * unit * unit) return `${trim(size / (unit * unit))} MB`;
  return `${trim(size / (unit * unit * unit))} GB`;
}

/** True for a paste that should become a file: long pastes into a terminal are fragile. */
export function isLongPaste(text: string): boolean {
  if (text.length > LONG_PASTE_CHARS) return true;
  let lines = 1;
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 10 && ++lines > LONG_PASTE_LINES) return true;
  }
  return false;
}

/** Markdown-looking text (headings, fences, lists, links) is named .md, anything else .txt. */
export function looksLikeMarkdown(text: string): boolean {
  return /^(#{1,6} |```|\s*[-*+] |\s*\d+\. |> )|\[[^\]]+\]\([^)]+\)/m.test(text);
}

/** The file a long paste becomes: pasted-20261126-1002.md (or .txt). */
export function pasteFile(text: string, now = new Date()): File {
  const pad = (n: number) => String(n).padStart(2, "0");
  const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`;
  const markdown = looksLikeMarkdown(text);
  return new File([text], `pasted-${stamp}.${markdown ? "md" : "txt"}`, { type: markdown ? "text/markdown" : "text/plain" });
}

/** True when a type can be shown as a picture from the browser's own file (a composer preview). */
export function previewable(type: string): boolean {
  return /^image\/(png|jpeg|gif|webp|bmp)$/.test(type);
}

// ---- The typed line ----------------------------------------------------------------------

/** A file named by an "Attached:" line in a message. */
export interface AttachedRef {
  id: string;
  name: string;
  path: string;
  type: string;
  sizeLabel: string;
}

const linePattern = /^Attached: (\/.+) \(([a-z0-9.+-]+\/[a-z0-9.+-]+), (\d+(?:\.\d+)? (?:B|KB|MB|GB))\)$/i;
const copyName = /^(a_[0-9a-f]{24})-(.+)$/;

/**
 * Splits a message into its words and the files its "Attached:" lines name. A line whose path is
 * not one of crowsnest's copies (no attachment id in the file name) stays in the text: anyone
 * can type "Attached: …", and only an id the server knows will fetch anything.
 */
export function parseAttachedLines(text: string): { text: string; refs: AttachedRef[] } {
  const refs: AttachedRef[] = [];
  const kept: string[] = [];
  for (const line of text.split("\n")) {
    const [, path = "", type = "", sizeLabel = ""] = linePattern.exec(line.trim()) ?? [];
    const [, id = "", name = ""] = copyName.exec(path.slice(path.lastIndexOf("/") + 1)) ?? [];
    if (path && id) {
      refs.push({ id, name, path, type: type.toLowerCase(), sizeLabel });
    } else {
      kept.push(line);
    }
  }
  return { text: kept.join("\n").trimEnd(), refs };
}

// ---- Showing files ----------------------------------------------------------------------

/** What a list item needs: a stored attachment, or a file named by a typed line. */
export interface ShownFile {
  id: string;
  name: string;
  type: string;
  sizeLabel: string;
  /** Show a thumbnail (an inline-safe picture). */
  picture: boolean;
}

export function shownFromAttachment(attachment: Attachment): ShownFile {
  return { id: attachment.id, name: attachment.name, type: attachment.type, sizeLabel: formatBytes(attachment.size), picture: attachment.image && attachment.thumb };
}

/** A typed line's file: a thumbnail is tried for raster image types and falls back to a chip. */
export function shownFromRef(ref: AttachedRef): ShownFile {
  return { id: ref.id, name: ref.name, type: ref.type, sizeLabel: ref.sizeLabel, picture: /^image\/(png|jpeg|gif)$/.test(ref.type) };
}

// ---- Calls -------------------------------------------------------------------------------

export interface UploadOptions {
  onProgress?: (fraction: number) => void;
  signal?: AbortSignal;
}

/**
 * Uploads one file as the raw body (its name in X-Filename), reporting progress. Resolves with
 * the pending attachment; rejects with an ApiError (TOO_LARGE, QUOTA_FULL, …) or an AbortError.
 */
export function uploadAttachment(file: Blob, name: string, options: UploadOptions = {}): Promise<Attachment> {
  if (import.meta.env.DEV && fixturesEnabled()) return fixtureUpload(file, name, options);
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    request.open("POST", `${API_BASE}/attachments`);
    request.setRequestHeader("Accept", "application/json");
    request.setRequestHeader("X-Crowsnest", "1");
    request.setRequestHeader("X-Filename", encodeURIComponent(name));
    request.setRequestHeader("Content-Type", file.type || "application/octet-stream");
    request.withCredentials = true;
    request.upload.onprogress = (event) => {
      if (event.lengthComputable && event.total > 0) options.onProgress?.(event.loaded / event.total);
    };
    request.onload = () => {
      let payload: unknown = null;
      try {
        payload = JSON.parse(request.responseText);
      } catch {
        // an empty or non-JSON body: the status says enough
      }
      if (request.status >= 200 && request.status < 300) {
        resolve((payload as { data: Attachment }).data);
        return;
      }
      const error = (payload as { error?: { code?: string; message?: string } } | null)?.error;
      reject(new ApiError(request.status, error?.code ?? `HTTP_${request.status}`, error?.message ?? (request.statusText || "Upload failed.")));
    };
    request.onerror = () => reject(new ApiError(0, "NETWORK_ERROR", "Cannot reach crowsnest. Check the connection."));
    request.onabort = () => reject(new DOMException("The upload was cancelled.", "AbortError"));
    options.signal?.addEventListener("abort", () => request.abort(), { once: true });
    request.send(file);
  });
}

/** Removes an upload that was never sent (the chip's ×). */
export function discardAttachment(id: string): Promise<void> {
  return api<void>(`/attachments/${encodeURIComponent(id)}`, { method: "DELETE" });
}

/** An owner's files: "job:j_3", "question:q_7", optionally one role. */
export function listAttachments(owner: string, role?: string): Promise<Attachment[]> {
  const query = new URLSearchParams({ owner });
  if (role) query.set("role", role);
  return api<Attachment[]>(`/attachments?${query.toString()}`);
}

/** One attachment's record (a chip from a typed line wants its name and thumbnail state). */
export function attachmentMeta(id: string): Promise<Attachment> {
  return api<Attachment>(`/attachments/${encodeURIComponent(id)}/meta`);
}

/** Fixtures mode answers an upload in memory, with a little simulated progress. */
async function fixtureUpload(file: Blob, name: string, options: UploadOptions): Promise<Attachment> {
  for (const step of [0.3, 0.7, 1]) {
    await new Promise((resolve) => setTimeout(resolve, 60));
    options.onProgress?.(step);
  }
  const id = `a_${Math.random().toString(16).slice(2).padEnd(24, "0").slice(0, 24)}`;
  const image = previewable(file.type);
  return { id, name, type: file.type || "application/octet-stream", size: file.size, sha256: "", image, thumb: false, inline: image, origin: "human", created_at: new Date().toISOString() };
}
