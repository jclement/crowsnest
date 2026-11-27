// The manual as a query: the first caller (the Help view, a "?" sheet, a palette search) fetches
// its chunk; everyone after reads the cache. It never changes while the app runs.
import { useQuery } from "@tanstack/react-query";

export const manualQueryKey = ["manual"] as const;

export function useManual(enabled = true) {
  return useQuery({
    queryKey: manualQueryKey,
    queryFn: async () => (await import("../manual/content")).loadManual(),
    enabled,
    staleTime: Infinity,
    gcTime: Infinity,
  });
}
