// What a spoken job means, before it is committed (R4 Codex P3/V1, Fable 3.5): which project it
// is for, the brief with the project's name taken off the front, and the sentence read back so a
// mis-heard repository or negation is caught before a job exists. Pure, so it is unit-tested; the
// voice job box (components/jobs/VoiceJob.tsx) is its only user.

/** What the guess needs of a project. */
export interface ProjectChoice {
  slug: string;
  name: string;
}

/** The interpretation of one transcript. */
export interface JobIntent {
  /** The brief to give: the transcript, minus a leading "Website:" or "for Website," when that named the project. */
  brief: string;
  /** The project named, or null (the orchestrator, or not yet known). */
  slug: string | null;
  /** True when the speech named more than one project, or none while none is chosen: ask. */
  ambiguous: boolean;
  /** Every project the speech named, for the "which one?" buttons. */
  candidates: string[];
}

/** Lower-cased words, so "Mobile app" matches "the mobile app needs…". */
function words(text: string): string {
  return ` ${text.toLowerCase().replace(/[^a-z0-9]+/g, " ").trim()} `;
}

/**
 * Interprets a transcript. `chosen` is the project already picked in the box ("" for the
 * orchestrator, undefined for none picked): a picked project stands unless the speech names
 * another, which makes it ambiguous rather than silently switching.
 */
export function interpretJob(transcript: string, projects: ProjectChoice[], chosen?: string): JobIntent {
  const text = transcript.trim();
  const spoken = words(text);
  const named = projects.filter((project) => spoken.includes(words(project.name)) || spoken.includes(words(project.slug)));
  const candidates = named.map((project) => project.slug);
  const brief = stripPrefix(text, named);
  if (named.length === 1) {
    const only = named[0]?.slug ?? null;
    const conflicts = chosen !== undefined && chosen !== "" && chosen !== only;
    return { brief, slug: only, ambiguous: conflicts, candidates: conflicts && chosen ? [chosen, ...candidates] : candidates };
  }
  if (named.length > 1) return { brief, slug: null, ambiguous: true, candidates };
  if (chosen !== undefined) return { brief, slug: chosen || null, ambiguous: false, candidates };
  return { brief, slug: null, ambiguous: true, candidates };
}

/** Takes "Website:", "for Website," or "in Website -" off the front when that is all it says. */
function stripPrefix(text: string, named: ProjectChoice[]): string {
  for (const project of named) {
    for (const label of [project.name, project.slug]) {
      const pattern = new RegExp(`^(?:for |in |on )?${escape(label)}\\s*[:,.-]\\s*`, "i");
      if (pattern.test(text)) {
        const rest = text.replace(pattern, "").trim();
        if (rest) return rest[0]?.toUpperCase() + rest.slice(1);
      }
    }
  }
  return text;
}

function escape(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** The sentence read back (and shown) before the job is created. */
export function readback(brief: string, projectName: string | null): string {
  const shortened = brief.length > 140 ? `${brief.slice(0, 139)}…` : brief;
  return `${projectName ?? "The orchestrator"}: ${shortened.replace(/[.?!]+$/, "")}. Create the job?`;
}
