// useProjectFilter: the Jobs view's project choice (DESIGN.md 21.1), from the URL (?project=)
// when it names one, else as this browser last left it (lib/projects.ts); setting it does both.
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useCallback, useState } from "react";
import { filterToParam, loadProjectFilter, resolveProjectFilter, saveProjectFilter, type ProjectFilter, type ProjectList } from "../../lib/projects";

/** The board's project filter: the URL's, else the remembered one; setting it does both. */
export function useProjectFilter(list: ProjectList | undefined): [ProjectFilter, (filter: ProjectFilter) => void] {
  const search = useSearch({ strict: false }) as { project?: string; job?: string };
  const navigate = useNavigate();
  const [remembered, setRemembered] = useState(loadProjectFilter);
  const filter = resolveProjectFilter(search.project, remembered, list?.projects);
  const set = useCallback(
    (next: ProjectFilter) => {
      saveProjectFilter(next);
      setRemembered(next);
      void navigate({ to: "/jobs", search: (previous: Record<string, unknown>) => ({ ...previous, project: filterToParam(next) || undefined }) } as never);
    },
    [navigate],
  );
  return [filter, set];
}

