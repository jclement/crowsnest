// The standalone diff (DESIGN.md 6.21): /diff?job=j_12, /diff?project=<slug> or /diff?orchestrator=1,
// with &scope=, &file= and &base= keeping where the reader is. The app's chrome (header, nav, the
// needs-you tray, the bottom bar on a phone) is hidden here (AppFrame's bare mode), so this is the
// viewer's full screen: the fullscreen toggle and "Open in a new tab" both come here. Leaving goes
// back to where fullscreen was entered (&from=, an in-app path), else back in history, else to
// the job or project.
import { useNavigate, useRouter } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { useCallback, useEffect } from "react";
import { type ChangeScope } from "../lib/changes";
import { whoFromSearch, type DiffSearch } from "../lib/diffSearch";
import { LazyDiffViewer as DiffViewer } from "../components/changes/JobChanges";
import { Banner } from "../components/ui/Banner";
import { Button } from "../components/ui/Button";

export function DiffView({ search }: { search: DiffSearch }) {
  const navigate = useNavigate();
  const router = useRouter();
  const who = whoFromSearch(search);
  const label = who?.kind === "job" ? who.id : who?.kind === "project" ? who.ref : "orchestrator";
  useEffect(() => {
    document.title = `Changes: ${label} · crowsnest`;
  }, [label]);
  const leave = useCallback(() => {
    if (search.from) {
      router.history.push(search.from);
      return;
    }
    if (window.history.length > 1 && document.referrer.startsWith(window.location.origin)) {
      router.history.back();
      return;
    }
    if (who?.kind === "job") void navigate({ to: "/jobs", search: { job: who.id, project: undefined } });
    else if (who?.kind === "project") void navigate({ to: "/projects/$slug", params: { slug: who.ref } });
    else void navigate({ to: "/" });
  }, [search.from, router, navigate, who]);
  const onState = useCallback(
    (state: { scope: ChangeScope; file?: string; base: string }) => {
      if (state.scope === (search.scope ?? "all") && state.file === search.file && state.base === (search.base ?? "")) return;
      void navigate({ to: "/diff", search: { ...search, scope: state.scope, file: state.file, base: state.base || undefined }, replace: true });
    },
    [navigate, search],
  );
  if (!who) {
    return (
      <div className="p-4">
        <Banner tone="warning">Say whose changes: /diff?job=j_12 or /diff?project=&lt;slug&gt;.</Banner>
      </div>
    );
  }
  return (
    <div className="flex h-full flex-col bg-surface pt-[env(safe-area-inset-top)]">
      <div className="flex shrink-0 items-center gap-2 border-b border-border px-2 py-1">
        <Button compact variant="ghost" icon={ArrowLeft} onClick={leave}>
          Back
        </Button>
        <h1 className="min-w-0 truncate text-sm font-semibold text-heading">Changes: {label}</h1>
      </div>
      <div className="min-h-0 flex-1">
        <DiffViewer
          who={who}
          fullscreen
          initialScope={search.scope}
          initialFile={search.file}
          initialBase={search.base}
          onState={onState}
          onFullscreen={(on) => {
            if (!on) leave();
          }}
        />
      </div>
    </div>
  );
}
