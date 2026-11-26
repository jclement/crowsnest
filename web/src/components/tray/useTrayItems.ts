// The tray's items and its snooze store, shared by the desktop panel and the phone bar
// (DESIGN.md 8.7).
import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { readSnoozed, SNOOZE_MS, trayItems, writeSnoozed, type TrayItem } from "../../lib/needsYou";
import { useApprovals, useFleet, useJobs, useQuestions } from "../../lib/queries";

// ---- Snooze store (shared by the bar and the panel) ------------------------------------------

let snoozeListeners: (() => void)[] = [];
let snoozeCache: Record<string, number> | null = null;

function snoozeSnapshot(): Record<string, number> {
  snoozeCache ??= readSnoozed();
  return snoozeCache;
}

function subscribeSnooze(listener: () => void): () => void {
  snoozeListeners.push(listener);
  return () => {
    snoozeListeners = snoozeListeners.filter((entry) => entry !== listener);
  };
}

export function snooze(id: string): void {
  const next = { ...readSnoozed(), [id]: Date.now() + SNOOZE_MS };
  writeSnoozed(next);
  snoozeCache = next;
  for (const listener of snoozeListeners) listener();
}

/** The tray's items, refreshed as data changes and as snoozes run out. */
export function useTrayItems(): TrayItem[] {
  const approvals = useApprovals("pending");
  const questions = useQuestions("open");
  const fleet = useFleet();
  const jobs = useJobs();
  const snoozed = useSyncExternalStore(subscribeSnooze, snoozeSnapshot, snoozeSnapshot);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => {
      snoozeCache = readSnoozed();
      setNow(Date.now());
    }, 30_000);
    return () => window.clearInterval(timer);
  }, []);
  return useMemo(
    () => trayItems({ approvals: approvals.data, questions: questions.data, agents: fleet.data?.agents, jobs: jobs.data }, snoozed, now),
    [approvals.data, questions.data, fleet.data?.agents, jobs.data, snoozed, now],
  );
}

