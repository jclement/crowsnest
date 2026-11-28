// The phone header's "+" (R10 F-F3): on a phone Now's job box is 2.7 screens down, and nothing
// said the palette gives jobs. It opens the same form the Jobs board's New job opens (NewJobForm)
// in a sheet over whatever view is showing; the toast that follows links to the job.
import { Plus } from "lucide-react";
import { useState } from "react";
import { cn } from "../../lib/cn";
import { Sheet } from "../ui/Sheet";
import { NewJobForm } from "./NewJobForm";

export function NewJobButton({ className }: { className?: string }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-haspopup="dialog"
        title="Give a job"
        className={cn("inline-flex h-11 w-11 shrink-0 items-center justify-center rounded text-chrome-fg hover:bg-chrome-hover", className)}
      >
        <Plus className="h-5 w-5" aria-hidden />
        <span className="sr-only">Give a job</span>
      </button>
      <Sheet open={open} onClose={() => setOpen(false)} title="New job">
        <NewJobForm onCreated={() => setOpen(false)} autoFocus />
      </Sheet>
    </>
  );
}
