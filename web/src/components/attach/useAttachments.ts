// The state behind every box that sends files to an agent (DESIGN.md 8.6): Chat's composer, the
// job box and form, a job's message box and a question's note. Each file becomes a chip that
// uploads at once, with progress; a failed one can be retried; removing one cancels its upload
// (or deletes the stored upload). Sending is blocked until every chip has finished.
//
// Also here: the paste handler (a screenshot from the clipboard is a file; a long paste becomes a
// .txt or .md file by default, with a toast to put it back as text) and the drag-and-drop hook
// with its drop target.
import { useCallback, useEffect, useMemo, useRef, useState, type ClipboardEvent, type RefObject } from "react";
import { ApiError } from "../../lib/api";
import {
  discardAttachment,
  formatBytes,
  isLongPaste,
  pasteFile,
  previewable,
  uploadAttachment,
  type Attachment,
  type AttachmentLimits,
} from "../../lib/attachments";
import { useMe } from "../../lib/queries";
import { useToast } from "../ui/toast-context";

export type ChipStatus = "uploading" | "done" | "error";

/** One file in a composer. */
export interface AttachmentChip {
  key: string;
  file: File;
  name: string;
  size: number;
  type: string;
  /** An object URL of the file itself, for a picture's preview before the upload finishes. */
  previewUrl?: string;
  status: ChipStatus;
  /** 0 to 1 while uploading. */
  progress: number;
  attachment?: Attachment;
  error?: string;
  /** Set when a long paste became this file: "paste as text" puts it back. */
  pastedText?: string;
}

export interface AttachmentsApi {
  /** False when the server has attachments off: hide the controls. */
  enabled: boolean;
  limits: AttachmentLimits | undefined;
  chips: AttachmentChip[];
  /** Adds files (each starts uploading) and returns their chip keys. */
  add: (files: File[], options?: { pastedText?: string }) => string[];
  remove: (key: string) => void;
  retry: (key: string) => void;
  /** After a successful send: the chips go, the uploads now belong to the message. */
  clear: () => void;
  /** The ids of finished uploads, in order: what a send names. */
  ids: string[];
  /** Some upload is still running: sending must wait. */
  uploading: boolean;
  /** Some chip failed: sending must wait until it is retried or removed. */
  failed: boolean;
}

let nextKey = 0;

/** Pasted screenshots arrive as "image.png"; give them a name that says when. */
function pastedName(file: File, now = new Date()): string {
  if (file.name && file.name !== "image.png" && file.name !== "image.jpeg") return file.name;
  const pad = (n: number) => String(n).padStart(2, "0");
  const ext = file.type === "image/jpeg" ? "jpg" : (file.type.split("/")[1] ?? "png");
  return `pasted-${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}.${ext}`;
}

export function useAttachments(): AttachmentsApi {
  const me = useMe();
  const limits = me.data?.features?.attachments;
  const [chips, setChips] = useState<AttachmentChip[]>([]);
  const controllers = useRef(new Map<string, AbortController>());
  const chipsRef = useRef(chips);
  useEffect(() => {
    chipsRef.current = chips;
  });

  const update = useCallback((key: string, change: Partial<AttachmentChip>) => {
    setChips((current) => current.map((chip) => (chip.key === key ? { ...chip, ...change } : chip)));
  }, []);

  const start = useCallback(
    (chip: AttachmentChip) => {
      const controller = new AbortController();
      controllers.current.set(chip.key, controller);
      uploadAttachment(chip.file, chip.name, { signal: controller.signal, onProgress: (progress) => update(chip.key, { progress }) })
        .then((attachment) => {
          if (controller.signal.aborted) {
            void discardAttachment(attachment.id).catch(() => undefined);
            return;
          }
          update(chip.key, { status: "done", progress: 1, attachment, error: undefined });
        })
        .catch((error: unknown) => {
          if (controller.signal.aborted) return;
          update(chip.key, { status: "error", error: error instanceof ApiError || error instanceof Error ? error.message : "Upload failed." });
        })
        .finally(() => controllers.current.delete(chip.key));
    },
    [update],
  );

  const add = useCallback(
    (files: File[], options: { pastedText?: string } = {}) => {
      const keys: string[] = [];
      const room = limits ? limits.max_files - chipsRef.current.length : files.length;
      const fresh: AttachmentChip[] = files.map((file, index) => {
        const key = `chip-${++nextKey}`;
        keys.push(key);
        const name = pastedName(file);
        const chip: AttachmentChip = {
          key,
          file,
          name,
          size: file.size,
          type: file.type,
          previewUrl: previewable(file.type) ? URL.createObjectURL(file) : undefined,
          status: "uploading",
          progress: 0,
          pastedText: options.pastedText,
        };
        if (index >= room) return { ...chip, status: "error", error: `At most ${limits?.max_files} files can go with one message.` };
        if (limits && file.size > limits.max_bytes) return { ...chip, status: "error", error: `Larger than ${formatBytes(limits.max_bytes)}, the most crowsnest takes.` };
        if (file.size === 0) return { ...chip, status: "error", error: "That file is empty." };
        return chip;
      });
      setChips((current) => [...current, ...fresh]);
      for (const chip of fresh) if (chip.status === "uploading") start(chip);
      return keys;
    },
    [limits, start],
  );

  const forget = (chip: AttachmentChip) => {
    if (chip.previewUrl) URL.revokeObjectURL(chip.previewUrl);
    controllers.current.get(chip.key)?.abort();
  };

  const remove = useCallback((key: string) => {
    const chip = chipsRef.current.find((item) => item.key === key);
    if (!chip) return;
    forget(chip);
    if (chip.attachment) void discardAttachment(chip.attachment.id).catch(() => undefined);
    setChips((current) => current.filter((item) => item.key !== key));
  }, []);

  const retry = useCallback(
    (key: string) => {
      const chip = chipsRef.current.find((item) => item.key === key);
      if (!chip || chip.status !== "error") return;
      if (limits && chip.size > limits.max_bytes) return;
      const restarted = { ...chip, status: "uploading" as const, progress: 0, error: undefined };
      update(key, restarted);
      start(restarted);
    },
    [limits, start, update],
  );

  const clear = useCallback(() => {
    for (const chip of chipsRef.current) if (chip.previewUrl) URL.revokeObjectURL(chip.previewUrl);
    setChips([]);
  }, []);

  // Leaving the page cancels uploads still running and frees the previews; finished uploads stay
  // on the server, and the sweep deletes them after a day if nothing sends them.
  useEffect(
    () => () => {
      for (const chip of chipsRef.current) forget(chip);
    },
    [],
  );

  return useMemo(
    () => ({
      enabled: !!limits,
      limits,
      chips,
      add,
      remove,
      retry,
      clear,
      ids: chips.flatMap((chip) => (chip.status === "done" && chip.attachment ? [chip.attachment.id] : [])),
      uploading: chips.some((chip) => chip.status === "uploading"),
      failed: chips.some((chip) => chip.status === "error"),
    }),
    [limits, chips, add, remove, retry, clear],
  );
}

/** The sentence under a composer while uploads run or failed, when sending is held. */
export function uploadHold(attachments: AttachmentsApi): string | null {
  if (attachments.uploading) return "Waiting for uploads to finish before sending.";
  if (attachments.failed) return "Retry or remove the failed file to send.";
  return null;
}

// ---- Paste ---------------------------------------------------------------------------------

/** The files on a clipboard or drop, from either list a browser fills. */
export function filesOf(data: DataTransfer | null): File[] {
  if (!data) return [];
  const files = Array.from(data.files ?? []);
  if (files.length > 0) return files;
  return Array.from(data.items ?? []).flatMap((item) => {
    const file = item.kind === "file" ? item.getAsFile() : null;
    return file ? [file] : [];
  });
}

/**
 * A textarea's onPaste: files (a screenshot) become chips; a long text becomes a file chip, with
 * a toast whose action puts it back as text through insertText. Anything else pastes as usual.
 */
export function usePasteToAttach(attachments: AttachmentsApi, insertText: (text: string) => void) {
  const toast = useToast();
  return useCallback(
    (event: ClipboardEvent<HTMLElement>) => {
      if (!attachments.enabled) return;
      const files = filesOf(event.clipboardData);
      if (files.length > 0) {
        event.preventDefault();
        attachments.add(files);
        return;
      }
      const text = event.clipboardData.getData("text/plain");
      if (!text || !isLongPaste(text)) return;
      event.preventDefault();
      const file = pasteFile(text);
      const [key = ""] = attachments.add([file], { pastedText: text });
      toast.show({
        tone: "info",
        title: `Long paste attached as ${file.name}`,
        description: "A long paste is fragile in a terminal, so it goes to the agent as a file.",
        action: {
          label: "Paste as text instead",
          onClick: () => {
            attachments.remove(key);
            insertText(text);
          },
        },
      });
    },
    [attachments, insertText, toast],
  );
}

// ---- Drag and drop -------------------------------------------------------------------------

function carriesFiles(event: DragEvent): boolean {
  return Array.from(event.dataTransfer?.types ?? []).includes("Files");
}

/**
 * Makes an element a drop target for files: dragging shows the overlay (see DropOverlay), a drop
 * adds the files. Text and links dragged in are left to the browser. Returns whether files are
 * over the target.
 */
export function useFileDrop(target: RefObject<HTMLElement | null>, onFiles: (files: File[]) => void, enabled = true): boolean {
  const [over, setOver] = useState(false);
  const onFilesRef = useRef(onFiles);
  useEffect(() => {
    onFilesRef.current = onFiles;
  });
  useEffect(() => {
    const element = target.current;
    if (!element || !enabled) return;
    let depth = 0;
    const enter = (event: DragEvent) => {
      if (!carriesFiles(event)) return;
      event.preventDefault();
      depth++;
      setOver(true);
    };
    const overHandler = (event: DragEvent) => {
      if (!carriesFiles(event)) return;
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
    };
    const leave = (event: DragEvent) => {
      if (!carriesFiles(event)) return;
      depth = Math.max(0, depth - 1);
      if (depth === 0) setOver(false);
    };
    const drop = (event: DragEvent) => {
      if (!carriesFiles(event)) return;
      event.preventDefault();
      depth = 0;
      setOver(false);
      const files = filesOf(event.dataTransfer);
      if (files.length > 0) onFilesRef.current(files);
    };
    element.addEventListener("dragenter", enter);
    element.addEventListener("dragover", overHandler);
    element.addEventListener("dragleave", leave);
    element.addEventListener("drop", drop);
    return () => {
      element.removeEventListener("dragenter", enter);
      element.removeEventListener("dragover", overHandler);
      element.removeEventListener("dragleave", leave);
      element.removeEventListener("drop", drop);
    };
  }, [target, enabled]);
  return over;
}

/**
 * Stops a file dropped anywhere else in the app from making the browser leave the page to show
 * it (its default for a drop nobody handled). Drop targets handle their own drops first.
 */
export function useNoStrayFileDrops() {
  useEffect(() => {
    const stop = (event: DragEvent) => {
      if (carriesFiles(event) && !event.defaultPrevented) event.preventDefault();
    };
    window.addEventListener("dragover", stop);
    window.addEventListener("drop", stop);
    return () => {
      window.removeEventListener("dragover", stop);
      window.removeEventListener("drop", stop);
    };
  }, []);
}
