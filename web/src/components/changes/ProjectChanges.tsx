// A project's Changes (DESIGN.md 6.20): its main checkout (the lead's uncommitted work) and each
// job that still has a worktree, with its diffstat and how much of it is viewed; each opens its
// diff full screen.
import { Link } from "@tanstack/react-router";
import { FileDiff } from "lucide-react";
import { statCells, viewedText } from "../../lib/changes";
import { useProjectChanges } from "../../lib/changes-queries";
import { cn } from "../../lib/cn";
import { Card } from "../ui/Card";
import { SkeletonRows } from "../ui/Skeleton";

export function ProjectChangesCard({ slug }: { slug: string }) {
  const query = useProjectChanges(slug);
  return (
    <Card title="Changes" aria-label="Changes" actions={<FileDiff className="h-4 w-4 text-muted" aria-hidden />}>
      {query.isPending ? (
        <SkeletonRows rows={2} label="Reading the checkouts" />
      ) : query.isError ? (
        <p className="text-xs text-muted">{query.error.message}</p>
      ) : (
        <ul aria-label="Checkouts with changes" className="-mx-1 flex flex-col">
          {query.data.map((row) => (
            <li key={row.subject}>
              <Link
                to="/diff"
                search={row.kind === "job" ? { job: row.job_id } : { project: slug }}
                className="flex min-h-11 items-center gap-2 rounded px-1 text-xs hover:bg-hover md:min-h-8"
              >
                <span className="min-w-0 flex-1 truncate text-body">
                  {row.kind === "job" ? row.label : "Main checkout (the lead's)"}
                  {row.job_state && <span className="ml-1.5 text-2xs text-muted">{row.job_state.replace("_", " ")}</span>}
                </span>
                {row.error ? (
                  <span className="truncate text-2xs text-muted">{row.error}</span>
                ) : (
                  <>
                    <span className="shrink-0 text-2xs text-muted">{row.files} files</span>
                    <span className="shrink-0 font-mono text-2xs">
                      <span className="text-success">+{row.added}</span> <span className="text-danger">−{row.deleted}</span>
                    </span>
                    <span className="hidden shrink-0 gap-px sm:flex" aria-hidden>
                      {statCells(row.added, row.deleted).map((cell, index) => (
                        <span key={index} className={cn("h-2 w-1.5 rounded-[1px]", cell === "add" ? "bg-success" : cell === "del" ? "bg-danger" : "bg-border")} />
                      ))}
                    </span>
                    {row.files > 0 && <span className="shrink-0 text-2xs text-muted">{viewedText(row.viewed, row.files)}</span>}
                  </>
                )}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
