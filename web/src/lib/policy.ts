// The approval policy (DESIGN.md 8.7, "Policy"): the TypeScript mirror of internal/approvals'
// rules and previews, their endpoints and query hooks, and the pure helpers the rules card, the
// remember flow and the batch action share. Every query sits under ["approvals"], so an
// `approval` event refreshes the rules and their recent matches with the approvals.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, queryKeys } from "./api";
import type { Approval, Risk } from "./approvals";

export type RuleKind = "bash" | "edit" | "tool";

export interface PreviewMatch {
  approval_id: string;
  summary: string;
  risk: Risk;
  status: string;
  created_at: string;
}

/** What a rule would have done to the project's recent calls (last 7 days, at most 50). */
export interface RulePreview {
  considered: number;
  allowed: PreviewMatch[];
  above_max_risk: PreviewMatch[];
  dangerous: PreviewMatch[];
}

export interface ApprovalRule {
  id: string;
  scope: "project" | "job";
  project_id?: number;
  job_id?: string;
  kind: RuleKind;
  pattern: string;
  max_risk: "low" | "elevated";
  note?: string;
  created_at: string;
  created_via: string;
  revoked_at?: string | null;
  /** "Allow Bash commands matching `go test *`". */
  sentence: string;
  /** On the project list: what it matched recently. */
  recent?: RulePreview;
}

export interface RuleInput {
  kind: RuleKind;
  pattern: string;
  max_risk?: "low" | "elevated";
  note?: string;
}

export const kindLabels: Record<RuleKind, string> = { bash: "Bash command", edit: "File edits", tool: "Tool" };

/** An example pattern per kind, shown as the placeholder and under the field. */
export const kindExamples: Record<RuleKind, { placeholder: string; hint: string }> = {
  bash: { placeholder: "go test *", hint: "A whole command; * matches anything. Every part of a && or | chain must match a rule." },
  edit: { placeholder: "internal/", hint: "A directory inside the job's working directory; . is all of it." },
  tool: { placeholder: "mcp__github__*", hint: "A tool's name, or a prefix ending in *." },
};

/** "Would have matched 4 recent calls: go test ./a, go test ./b, …". */
export function previewLine(preview: RulePreview): string {
  const n = preview.allowed.length;
  if (n === 0) return preview.considered === 0 ? "No recent calls in this project to compare with." : `Would have matched none of the last ${preview.considered} calls.`;
  const shown = preview.allowed.slice(0, 3).map((match) => stripTool(match.summary));
  return `Would have matched ${n} recent call${n === 1 ? "" : "s"}: ${shown.join(", ")}${n > 3 ? ", …" : ""}`;
}

function stripTool(summary: string): string {
  const cut = summary.indexOf(": ");
  return cut > 0 ? summary.slice(cut + 2) : summary;
}

/** Two or more pending low-risk approvals from the same job, which one tap may allow together. */
export function batchable(approvals: Approval[], jobId: string | undefined): Approval[] {
  if (!jobId) return [];
  const same = approvals.filter((approval) => approval.status === "pending" && approval.risk === "low" && approval.job_id === jobId);
  return same.length >= 2 ? same : [];
}

const keys = {
  project: (ref: string) => [...queryKeys.approvals, "rules", "project", ref] as const,
  job: (id: string) => [...queryKeys.approvals, "rules", "job", id] as const,
};

export const policyEndpoints = {
  projectRules: (ref: string) => api<ApprovalRule[]>(`/projects/${encodeURIComponent(ref)}/approval-rules`),
  addProjectRule: (ref: string, input: RuleInput) => api<ApprovalRule>(`/projects/${encodeURIComponent(ref)}/approval-rules`, { method: "POST", body: input }),
  previewRule: (ref: string, input: RuleInput) => api<RulePreview>(`/projects/${encodeURIComponent(ref)}/approval-rules/preview`, { method: "POST", body: input }),
  removeRule: (id: string) => api<ApprovalRule>(`/approval-rules/${encodeURIComponent(id)}`, { method: "DELETE" }),
  jobRules: (id: string) => api<ApprovalRule[]>(`/jobs/${encodeURIComponent(id)}/approval-rules`),
  batchAllow: (ids: string[]) => api<Approval[]>(`/approvals/batch-allow`, { method: "POST", body: { ids } }),
};

export function useProjectRules(ref: string) {
  return useQuery({ queryKey: keys.project(ref), queryFn: () => policyEndpoints.projectRules(ref) });
}

export function useJobRules(id: string) {
  return useQuery({ queryKey: keys.job(id), queryFn: () => policyEndpoints.jobRules(id) });
}

/** A rule's preview as the human types it (debounced by the caller); off until the pattern is set. */
export function useRulePreview(ref: string, input: RuleInput, enabled: boolean) {
  return useQuery({
    queryKey: [...queryKeys.approvals, "rules", "preview", ref, input.kind, input.pattern, input.max_risk ?? "low"] as const,
    queryFn: () => policyEndpoints.previewRule(ref, input),
    enabled,
    retry: false,
  });
}

/** Every policy change refreshes the approvals (and the rules under them) and the jobs. */
function usePolicyMutation<TArgs, TResult>(fn: (args: TArgs) => Promise<TResult>) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.approvals });
      void queryClient.invalidateQueries({ queryKey: queryKeys.jobs });
    },
  });
}

export function useAddProjectRule(ref: string) {
  return usePolicyMutation((input: RuleInput) => policyEndpoints.addProjectRule(ref, input));
}

export function useRemoveRule() {
  return usePolicyMutation((id: string) => policyEndpoints.removeRule(id));
}

export function useBatchAllow() {
  return usePolicyMutation((ids: string[]) => policyEndpoints.batchAllow(ids));
}
