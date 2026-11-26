// A brief handed from the command palette to the Jobs view's new-job form ("Open the full form"
// with what was typed). The palette sets it and navigates to Jobs; NewJobForm takes it when it
// mounts or, if it is already on screen, when the draft arrives. Taking it clears it, so a draft
// fills the form once.

export interface JobDraft {
  brief: string;
}

let pending: JobDraft | null = null;
const listeners = new Set<() => void>();

/** Hands a draft to the form (and to a form already on screen). */
export function offerJobDraft(draft: JobDraft): void {
  pending = draft;
  for (const listener of listeners) listener();
}

/** Takes the pending draft, if any; it is then gone. */
export function takeJobDraft(): JobDraft | null {
  const draft = pending;
  pending = null;
  return draft;
}

/** Calls listener whenever a draft is offered. Returns the unsubscribe function. */
export function onJobDraft(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
