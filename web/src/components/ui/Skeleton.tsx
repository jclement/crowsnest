// Skeleton: a placeholder block shown while data loads, sized by the caller. A band of light
// sweeps across it like a scan line (still under reduced motion: the global rule stops it).
import { cn } from "../../lib/cn";

export function Skeleton({ className }: { className?: string }) {
  return (
    <div
      aria-hidden
      className={cn(
        "animate-scan rounded bg-hover bg-[length:200%_100%] bg-[linear-gradient(100deg,transparent_30%,var(--color-surface-sunken)_50%,transparent_70%)]",
        className,
      )}
    />
  );
}

/** A stack of skeleton rows for a loading list. */
export function SkeletonRows({ rows = 6, label = "Loading" }: { rows?: number; label?: string }) {
  return (
    <div role="status" aria-label={label} className="flex flex-col gap-2 p-3">
      {Array.from({ length: rows }, (_, index) => (
        <div key={index} className="flex items-center gap-2">
          <Skeleton className="h-2.5 w-2.5 rounded-full" />
          <Skeleton className="h-3.5" />
          <Skeleton className={cn("h-3.5", index % 3 === 0 ? "w-2/3" : index % 3 === 1 ? "w-1/2" : "w-3/4")} />
        </div>
      ))}
    </div>
  );
}
