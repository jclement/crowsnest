// The composer's side of attachments (DESIGN.md 8.6): the chips (thumbnail or file icon, name,
// size, progress, retry on failure, remove), the paperclip (and on a phone the camera), and the
// drop target's overlay.
import { Camera, FileText, Paperclip, RotateCw, Upload, X } from "lucide-react";
import { useRef, type ChangeEvent } from "react";
import { formatBytes } from "../../lib/attachments";
import { cn } from "../../lib/cn";
import { COARSE_POINTER_QUERY, useMediaQuery } from "../../lib/media";
import type { AttachmentChip, AttachmentsApi } from "./useAttachments";

/** The chips above a text box. Nothing when there are none. */
export function AttachmentTray({ attachments, className }: { attachments: AttachmentsApi; className?: string }) {
  if (attachments.chips.length === 0) return null;
  return (
    <ul aria-label="Attachments" className={cn("flex flex-wrap gap-1.5", className)}>
      {attachments.chips.map((chip) => (
        <ChipItem key={chip.key} chip={chip} onRemove={() => attachments.remove(chip.key)} onRetry={() => attachments.retry(chip.key)} />
      ))}
    </ul>
  );
}

function ChipItem({ chip, onRemove, onRetry }: { chip: AttachmentChip; onRemove: () => void; onRetry: () => void }) {
  const percent = Math.round(chip.progress * 100);
  const failed = chip.status === "error";
  return (
    <li
      data-status={chip.status}
      className={cn(
        "relative flex max-w-full min-w-0 items-center gap-2 overflow-hidden rounded border bg-surface py-1 pl-1 pr-0.5 text-xs",
        failed ? "border-danger/60" : "border-border-strong",
      )}
    >
      {chip.previewUrl ? (
        <img src={chip.previewUrl} alt="" className="h-9 w-9 shrink-0 rounded-sm border border-border object-cover" />
      ) : (
        <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-sm bg-hover text-muted">
          <FileText className="h-4 w-4" aria-hidden />
        </span>
      )}
      <span className="flex min-w-0 flex-col">
        <span className="max-w-[14rem] truncate font-medium text-heading" title={chip.name}>
          {chip.name}
        </span>
        <span className={cn("truncate text-2xs", failed ? "text-danger" : "text-muted")} role={failed ? "alert" : undefined}>
          {failed ? chip.error : chip.status === "uploading" ? `${formatBytes(chip.size)} · uploading ${percent}%` : formatBytes(chip.size)}
        </span>
      </span>
      {failed && (
        <button type="button" onClick={onRetry} aria-label={`Retry ${chip.name}`} className="flex h-11 w-11 shrink-0 items-center justify-center rounded text-muted hover:bg-hover hover:text-heading md:h-7 md:w-7">
          <RotateCw className="h-4 w-4" aria-hidden />
        </button>
      )}
      <button type="button" onClick={onRemove} aria-label={`Remove ${chip.name}`} className="flex h-11 w-11 shrink-0 items-center justify-center rounded text-muted hover:bg-hover hover:text-heading md:h-7 md:w-7">
        <X className="h-4 w-4" aria-hidden />
      </button>
      {chip.status === "uploading" && (
        <span
          role="progressbar"
          aria-label={`Uploading ${chip.name}`}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={percent}
          className="absolute inset-x-0 bottom-0 h-0.5 bg-accent/20"
        >
          <span className="block h-full bg-accent transition-[width]" style={{ width: `${percent}%` }} />
        </span>
      )}
    </li>
  );
}

/**
 * The paperclip: a file picker that on a phone offers the photo library, the camera and files
 * (no `capture`, so the system shows all three). On a touch screen a camera button sits beside
 * it with `capture`, which goes straight to the camera.
 */
export function AttachButtons({ attachments, disabled }: { attachments: AttachmentsApi; disabled?: boolean }) {
  const files = useRef<HTMLInputElement>(null);
  const camera = useRef<HTMLInputElement>(null);
  const touch = useMediaQuery(COARSE_POINTER_QUERY);
  if (!attachments.enabled) return null;
  const onPick = (event: ChangeEvent<HTMLInputElement>) => {
    const picked = Array.from(event.target.files ?? []);
    event.target.value = ""; // the same file can be picked again
    if (picked.length > 0) attachments.add(picked);
  };
  const button = "flex h-11 w-11 shrink-0 items-center justify-center rounded border border-border-strong bg-surface text-body hover:bg-hover hover:text-heading disabled:opacity-50 md:h-9 md:w-9";
  return (
    <>
      <button type="button" className={button} onClick={() => files.current?.click()} disabled={disabled} aria-label="Attach files" title="Attach files (or paste, or drop them here)">
        <Paperclip className="h-4 w-4" aria-hidden />
      </button>
      <input ref={files} type="file" multiple hidden onChange={onPick} data-testid="attach-input" tabIndex={-1} />
      {touch && (
        <>
          <button type="button" className={button} onClick={() => camera.current?.click()} disabled={disabled} aria-label="Take a photo">
            <Camera className="h-4 w-4" aria-hidden />
          </button>
          <input ref={camera} type="file" accept="image/*" capture="environment" hidden onChange={onPick} data-testid="camera-input" tabIndex={-1} />
        </>
      )}
    </>
  );
}

/** The drop target's overlay: shown over its (relatively positioned) parent while files hover. */
export function DropOverlay({ active, label = "Drop files to attach" }: { active: boolean; label?: string }) {
  if (!active) return null;
  return (
    <div
      data-testid="drop-overlay"
      className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center rounded border-2 border-dashed border-accent bg-accent/10 backdrop-blur-[1px]"
    >
      <span className="flex items-center gap-2 rounded bg-surface px-3 py-2 text-sm font-medium text-heading shadow">
        <Upload className="h-4 w-4 text-accent" aria-hidden />
        {label}
      </span>
    </div>
  );
}
