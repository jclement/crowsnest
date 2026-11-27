// Data for the diff viewer (DESIGN.md 6.21). Everything sits under ["changes"]: a `changes` event
// refreshes the summaries; a file's diff is keyed by its content hash, so a file that did not
// change keeps its cached diff and one that did gets a new key (and a fresh fetch) by itself.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import { whoKey, whoPath, type ChangeScope, type DiffProgress, type DiffSummary, type DiffWho, type FileView, type ProjectChangeRow } from "./changes";

export const changesKeys = {
  all: ["changes"] as const,
  summary: (who: DiffWho, scope: ChangeScope, base: string) => ["changes", "summary", whoKey(who), scope, base] as const,
  file: (who: DiffWho, path: string, hash: string, scope: ChangeScope, options: FileOptions) =>
    ["changes", "file", whoKey(who), path, hash, scope, options.context, options.ignoreWhitespace, options.since] as const,
  project: (ref: string) => ["changes", "project", ref] as const,
  progress: ["changes", "progress"] as const,
};

export interface FileOptions {
  context: number;
  ignoreWhitespace: boolean;
  since: boolean;
}

function query(params: Record<string, string | undefined>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) if (value) search.set(key, value);
  const text = search.toString();
  return text ? "?" + text : "";
}

export const changesApi = {
  summary: (who: DiffWho, scope: ChangeScope, base: string, refresh = false) =>
    api<DiffSummary>(whoPath(who) + query({ scope, base: who.kind === "job" ? undefined : base, refresh: refresh ? "1" : undefined })),
  file: (who: DiffWho, path: string, scope: ChangeScope, options: FileOptions, base: string) =>
    api<FileView>(
      whoPath(who) +
        "/file" +
        query({
          path,
          scope,
          context: options.context ? String(options.context) : undefined,
          ws: options.ignoreWhitespace ? "ignore" : undefined,
          since: options.since ? "1" : undefined,
          base: who.kind === "job" ? undefined : base,
        }),
    ),
  mark: (who: DiffWho, scope: ChangeScope, files: { path: string; hash: string }[], all = false, base = "") =>
    api<DiffSummary>(whoPath(who) + "/viewed" + query({ base: who.kind === "job" ? undefined : base }), { method: "PUT", body: { scope, files, all } }),
  unmark: (who: DiffWho, scope: ChangeScope, paths: string[], base = "") =>
    api<DiffSummary>(whoPath(who) + "/viewed" + query({ base: who.kind === "job" ? undefined : base }), { method: "DELETE", body: { scope, paths } }),
  project: (ref: string) => api<ProjectChangeRow[]>(`/changes/projects/${encodeURIComponent(ref)}/jobs`),
  progress: () => api<Record<string, DiffProgress>>("/changes/progress"),
  demoTouch: (id: string) => api<{ touched: boolean }>(`/changes/jobs/${encodeURIComponent(id)}/demo-touch`, { method: "POST" }),
};

/** One comparison of a diff, with its marks. Kept while a new scope loads; refetched every 45 s
 * too, which keeps the server watching the subject while the view is open. */
export function useDiffSummary(who: DiffWho, scope: ChangeScope, base: string, enabled = true) {
  return useQuery({
    queryKey: changesKeys.summary(who, scope, base),
    queryFn: () => changesApi.summary(who, scope, base),
    enabled,
    placeholderData: keepPreviousData,
    refetchInterval: 45_000,
  });
}

/** One file's diff; fetched when its section comes near the screen. */
export function useDiffFile(who: DiffWho, path: string, hash: string, scope: ChangeScope, options: FileOptions, base: string, enabled: boolean) {
  return useQuery({
    queryKey: changesKeys.file(who, path, hash, scope, options),
    queryFn: () => changesApi.file(who, path, scope, options, base),
    enabled,
    staleTime: Infinity,
    placeholderData: keepPreviousData,
  });
}

/** Mark files viewed, or clear marks; the answer is the new summary, written to the cache. */
export function useMarks(who: DiffWho, scope: ChangeScope, base: string) {
  const queryClient = useQueryClient();
  const settle = (summary: DiffSummary) => {
    queryClient.setQueryData(changesKeys.summary(who, scope, base), summary);
    void queryClient.invalidateQueries({ queryKey: ["changes", "summary", whoKey(who)] });
    void queryClient.invalidateQueries({ queryKey: changesKeys.progress });
  };
  // The tick shows at once (optimistic); the server's answer replaces it, and a refusal (the file
  // changed meanwhile) puts the summary back as the server has it.
  const optimistic = (viewed: boolean, paths: string[] | "all") => {
    const key = changesKeys.summary(who, scope, base);
    void queryClient.cancelQueries({ queryKey: key });
    queryClient.setQueryData<DiffSummary>(key, (summary) => {
      if (!summary) return summary;
      const files = summary.files.map((file) => (paths === "all" || paths.includes(file.path) ? { ...file, viewed, changed_since_viewed: viewed ? false : file.changed_since_viewed } : file));
      return { ...summary, files, viewed: files.filter((file) => file.viewed).length };
    });
  };
  const restore = () => void queryClient.invalidateQueries({ queryKey: ["changes", "summary", whoKey(who)] });
  const mark = useMutation({
    mutationFn: ({ files, all }: { files: { path: string; hash: string }[]; all?: boolean }) => changesApi.mark(who, scope, files, all, base),
    onMutate: ({ files, all }) => optimistic(true, all ? "all" : files.map((file) => file.path)),
    onSuccess: settle,
    onError: restore,
  });
  const unmark = useMutation({
    mutationFn: (paths: string[]) => changesApi.unmark(who, scope, paths, base),
    onMutate: (paths) => optimistic(false, paths.length === 0 ? "all" : paths),
    onSuccess: settle,
    onError: restore,
  });
  return { mark, unmark };
}

/** A project's checkouts with their diffstats (its main checkout first). */
export function useProjectChanges(ref: string, enabled = true) {
  return useQuery({ queryKey: changesKeys.project(ref), queryFn: () => changesApi.project(ref), enabled, refetchInterval: 60_000 });
}

/** Jobs' viewed counts, for the cards' progress bars (only jobs whose diff was opened). */
export function useDiffProgress() {
  return useQuery({ queryKey: changesKeys.progress, queryFn: changesApi.progress, staleTime: 15_000 });
}
