// Fixture schedule and context size (development only, like lib/fixtures.ts): the three example
// tasks the README ships, one per cadence shape, plus a skipped file; and an orchestrator past
// four fifths of its compaction threshold.
import type { ContextInfo, ScheduleView } from "./schedule";

const now = Date.now();
const at = (minutes: number) => new Date(now + minutes * 60_000).toISOString();

export const fixtureSchedule: ScheduleView = {
  dirs: ["/home/crow/.local/share/crowsnest/orchestrator/tasks", "/home/crow/.config/crowsnest/tasks"],
  tasks: [
    {
      name: "check", title: "Anything stuck or waiting on Jeff?", path: "/home/crow/.config/crowsnest/tasks/check.md", origin: "user",
      every: "15m", schedule: "every 15m", as: "poke", enabled: true, last_run_at: at(-9), next_due_at: at(6),
      last_outcome: "carried by a poke", run_now: false,
    },
    {
      name: "tidy", title: "Update the board and notes", path: "/home/crow/.config/crowsnest/tasks/tidy.md", origin: "user",
      every: "60m", schedule: "every 60m", as: "poke", enabled: false, last_run_at: null, next_due_at: null, run_now: false,
    },
    {
      name: "digest", title: "Daily digest", path: "/home/crow/.config/crowsnest/tasks/digest.md", origin: "user",
      every: "1d", after: "17:00", schedule: "every 1d after 17:00", as: "job", enabled: true, last_run_at: at(-1440), next_due_at: at(120),
      last_outcome: "job j_31 created", job_id: "j_31", job_state: "done", run_now: false,
    },
  ],
  problems: [{ path: "/home/crow/.config/crowsnest/tasks/weekly.md", error: `every: "1w" is not understood; want 15m, 60m, 4h, 1d, 3d, poke or change` }],
};

export const fixtureContext: ContextInfo = {
  tokens: 128_400, known: true, at: at(-2), threshold: 150_000, last_compact_at: at(-300), human_at: at(-40),
};

/** Answers the schedule and context routes; undefined for any other path. */
export function scheduleFixtureApi(path: string, method: string): unknown {
  if (path === "/schedule") return structuredClone(fixtureSchedule);
  const run = /^\/schedule\/([^/]+)\/run$/.exec(path);
  if (run && method === "POST") {
    const task = fixtureSchedule.tasks.find((t) => t.name === decodeURIComponent(run[1] ?? ""));
    if (task) task.run_now = true;
    return structuredClone(fixtureSchedule);
  }
  if (path === "/orchestrator/context") return structuredClone(fixtureContext);
  if (path === "/orchestrator/compact" && method === "POST") return { ...fixtureContext, last_compact_at: new Date().toISOString() };
  return undefined;
}
