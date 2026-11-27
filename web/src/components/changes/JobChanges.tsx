// The diff viewer where the app embeds it (DESIGN.md 6.20), loaded lazily so the highlighter and
// the viewer stay out of the main bundle. Fullscreen goes to the standalone route with the scope,
// file and base, and comes back here.
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { lazy, Suspense } from "react";
import { whoSearch, type DiffWho } from "../../lib/changes";
import { SkeletonRows } from "../ui/Skeleton";
import type { DiffViewerProps } from "./DiffViewer";

const DiffViewer = lazy(() => import("./DiffViewer").then((module) => ({ default: module.DiffViewer })));

/** The viewer, loaded on first use. */
export function LazyDiffViewer(props: DiffViewerProps) {
  return (
    <Suspense fallback={<SkeletonRows rows={6} label="Loading the diff viewer" />}>
      <DiffViewer {...props} />
    </Suspense>
  );
}

export function EmbeddedDiff({ who, className }: { who: DiffWho; className?: string }) {
  const navigate = useNavigate();
  const href = useRouterState({ select: (state) => state.location.href });
  return (
    <Suspense fallback={<SkeletonRows rows={6} label="Loading the diff viewer" />}>
      <DiffViewer
        who={who}
        className={className}
        onFullscreen={(on, state) => {
          if (!on) return;
          void navigate({ to: "/diff", search: { ...whoSearch(who), scope: state.scope, file: state.file, base: state.base || undefined, from: href } });
        }}
      />
    </Suspense>
  );
}
