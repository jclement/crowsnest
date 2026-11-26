// TanStack Query hooks for attachments (DESIGN.md 8.6). Lists live under ["attachments"], which
// an `attachment` event invalidates, so a question card or a job's files follow the server.
import { useQuery } from "@tanstack/react-query";
import { queryKeys } from "./api";
import { listAttachments } from "./attachments";

/** An owner's files from the server ("job:j_3", optionally one role), kept live by events. */
export function useOwnerAttachments(owner: string | null, role?: string) {
  return useQuery({
    queryKey: queryKeys.attachmentList(owner ?? "", role),
    queryFn: () => listAttachments(owner ?? "", role),
    enabled: !!owner,
  });
}
