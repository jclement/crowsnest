// Skeleton: a placeholder block shown while data loads, sized by the caller.
import { cn } from "../../lib/cn";

export function Skeleton({ className }: { className?: string }) {
  return <div aria-hidden className={cn("animate-shimmer rounded bg-hover", className)} />;
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
