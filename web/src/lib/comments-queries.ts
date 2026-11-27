// Data for line comments on a diff (DESIGN.md 6.21). The list sits under ["changes", "comments"],
// so the `changes` event (which a comment change publishes) refetches it in every open view; each
// change answers with the new list, written straight to the cache.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import { whoKey, whoPath, type DiffWho } from "./changes";
import type { CommentList, DiffComment, NewComment, SendResult } from "./comments";

export const commentKeys = {
  list: (who: DiffWho) => ["changes", "comments", whoKey(who)] as const,
};

export const commentsApi = {
  list: (who: DiffWho) => api<CommentList>(whoPath(who) + "/comments"),
  add: (who: DiffWho, comment: NewComment) => api<DiffComment>(whoPath(who) + "/comments", { method: "POST", body: comment }),
  edit: (who: DiffWho, id: number, body: string) => api<CommentList>(`${whoPath(who)}/comments/${id}`, { method: "PATCH", body: { body } }),
  remove: (who: DiffWho, id: number) => api<CommentList>(`${whoPath(who)}/comments/${id}`, { method: "DELETE" }),
  dismiss: (who: DiffWho, id: number) => api<CommentList>(`${whoPath(who)}/comments/${id}/dismiss`, { method: "POST" }),
  resend: (who: DiffWho, id: number) => api<CommentList>(`${whoPath(who)}/comments/${id}/resend`, { method: "POST" }),
  send: (who: DiffWho, overall: string, dryRun = false, force = false) =>
    api<SendResult>(whoPath(who) + "/comments/send", { method: "POST", body: { overall, dry_run: dryRun, force } }),
};

/** A subject's notes with their status now. */
export function useComments(who: DiffWho, enabled = true) {
  return useQuery({ queryKey: commentKeys.list(who), queryFn: () => commentsApi.list(who), enabled, refetchInterval: 45_000 });
}

/** Add, edit, delete, dismiss and re-send notes, and send them all; each refreshes the list. */
export function useCommentActions(who: DiffWho) {
  const queryClient = useQueryClient();
  const key = commentKeys.list(who);
  const write = (list: CommentList) => queryClient.setQueryData(key, list);
  const refresh = () => void queryClient.invalidateQueries({ queryKey: key });
  return {
    add: useMutation({ mutationFn: (comment: NewComment) => commentsApi.add(who, comment), onSettled: refresh }),
    edit: useMutation({ mutationFn: ({ id, body }: { id: number; body: string }) => commentsApi.edit(who, id, body), onSuccess: write }),
    remove: useMutation({ mutationFn: (id: number) => commentsApi.remove(who, id), onSuccess: write }),
    dismiss: useMutation({ mutationFn: (id: number) => commentsApi.dismiss(who, id), onSuccess: write }),
    resend: useMutation({ mutationFn: (id: number) => commentsApi.resend(who, id), onSuccess: write }),
    send: useMutation({
      mutationFn: ({ overall, force }: { overall: string; force?: boolean }) => commentsApi.send(who, overall, false, force),
      onSettled: () => {
        refresh();
        void queryClient.invalidateQueries({ queryKey: ["jobs"] });
      },
    }),
  };
}
