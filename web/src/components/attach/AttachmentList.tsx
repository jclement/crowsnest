// Showing attachments (DESIGN.md 8.6): pictures as thumbnails that open full size in a lightbox,
// everything else as a download chip. Every byte comes through /attachments/{id}, which decides
// what a browser may render; the web app never shows a file by its path.
import { Download, ExternalLink, FileText } from "lucide-react";
import { useState } from "react";
import { attachmentUrl, thumbUrl, type ShownFile } from "../../lib/attachments";
import { cn } from "../../lib/cn";
import { Sheet } from "../ui/Sheet";

export function AttachmentList({ files, label = "Attachments", className }: { files: ShownFile[]; label?: string; className?: string }) {
  const [open, setOpen] = useState<ShownFile | null>(null);
  if (files.length === 0) return null;
  return (
    <>
      <ul aria-label={label} className={cn("flex flex-wrap gap-2", className)}>
        {files.map((file) => (
          <li key={file.id} className="min-w-0 max-w-full">
            {file.picture ? <Thumbnail file={file} onOpen={() => setOpen(file)} /> : <FileChip file={file} />}
          </li>
        ))}
      </ul>
      <Lightbox file={open} onClose={() => setOpen(null)} />
    </>
  );
}

function Thumbnail({ file, onOpen }: { file: ShownFile; onOpen: () => void }) {
  const [broken, setBroken] = useState(false);
  if (broken) return <FileChip file={file} />;
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-label={`Open ${file.name} full size`}
      className="group block overflow-hidden rounded border border-border bg-hover focus:outline-none focus:ring-2 focus:ring-accent/40"
    >
      <img src={thumbUrl(file.id)} alt={file.name} loading="lazy" onError={() => setBroken(true)} className="block h-24 max-w-[10rem] object-cover transition group-hover:opacity-90 md:h-28" />
    </button>
  );
}

function FileChip({ file }: { file: ShownFile }) {
  return (
    <a
      href={attachmentUrl(file.id, true)}
      download={file.name}
      className="flex min-h-11 max-w-full min-w-0 items-center gap-2 rounded border border-border-strong bg-surface px-2 py-1 text-xs hover:bg-hover md:min-h-9"
    >
      <FileText className="h-4 w-4 shrink-0 text-muted" aria-hidden />
      <span className="flex min-w-0 flex-col">
        <span className="max-w-[16rem] truncate font-medium text-heading">{file.name}</span>
        <span className="truncate text-2xs text-muted">
          {file.type} · {file.sizeLabel}
        </span>
      </span>
      <Download className="h-4 w-4 shrink-0 text-muted" aria-hidden />
      <span className="sr-only">Download</span>
    </a>
  );
}

/** A picture full size, in a sheet (full screen on a phone), with download and open-original. */
export function Lightbox({ file, onClose }: { file: ShownFile | null; onClose: () => void }) {
  return (
    <Sheet
      open={!!file}
      onClose={onClose}
      title={<span className="truncate">{file?.name}</span>}
      headerExtra={
        file && (
          <span className="flex items-center gap-1">
            <a href={attachmentUrl(file.id)} target="_blank" rel="noopener noreferrer" className="flex h-11 w-11 items-center justify-center rounded text-muted hover:bg-hover hover:text-heading md:h-8 md:w-8" aria-label="Open the original">
              <ExternalLink className="h-4 w-4" aria-hidden />
            </a>
            <a href={attachmentUrl(file.id, true)} download={file.name} className="flex h-11 w-11 items-center justify-center rounded text-muted hover:bg-hover hover:text-heading md:h-8 md:w-8" aria-label="Download">
              <Download className="h-4 w-4" aria-hidden />
            </a>
          </span>
        )
      }
    >
      {file && (
        <div className="flex min-h-full items-center justify-center bg-hover p-2">
          <img src={attachmentUrl(file.id)} alt={file.name} className="max-h-[80vh] max-w-full object-contain" />
        </div>
      )}
    </Sheet>
  );
}
