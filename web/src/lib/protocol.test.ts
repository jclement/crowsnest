// The shapes here are copied from real transcripts under ~/.claude/projects (read-only), with ids
// and paths generalised: R10 asked for the strip to be tested on what crowsnest really sends.
import { describe, expect, it } from "vitest";
import { crowsnestHeader, quotedReply, systemLine, withoutHeaders } from "./protocol";

describe("crowsnest's headers", () => {
  it.each([
    ["[crowsnest job j_10] You hold this job. State: queued.", "job", "j_10", "Job j_10 handed to this agent"],
    ["[crowsnest job j_4] Your brief for job j_4 is in /tmp/cn-stop-1/state/jobs/j_4/brief.md (job j_4)", "job", "j_4", "Job j_4 handed to this agent"],
    ["[crowsnest check] The check failed: see /tmp/out.log", "check", "", "crowsnest's check result sent"],
    ["[crowsnest review j_19] Round 1 asks for changes", "review", "j_19", "Review comments on j_19 sent"],
    ["[crowsnest nudge] You ended a turn without declaring the job's state.", "nudge", "", "Nudged: asked to say where the job stands"],
    ["[crowsnest restart] You were restarted. ", "restart", "", "Restarted on the same conversation"],
    ["[crowsnest upkeep] Write a hand-over file", "upkeep", "", "Asked to write a hand-over before a fresh start"],
  ])("%s is crowsnest's, shown as one line", (text, kind, ref, line) => {
    const header = crowsnestHeader(text);
    expect(header?.kind).toBe(kind);
    expect(header?.ref).toBe(ref);
    expect(systemLine(header!)).toBe(line);
  });

  it("keeps the human's own words as theirs: a job message, an answer, a voice prompt, a poke", () => {
    for (const text of ["[crowsnest message j_3] please also update the README", "[crowsnest answer q_7] Ship it? → yes", "[crowsnest voice: en]\nship it", "[crowsnest poke 2026-11-28T10:00:00Z talk=off]"]) {
      expect(systemLine(crowsnestHeader(text)!)).toBeNull();
    }
    expect(crowsnestHeader("[crowsnest message j_3] please also update the README")?.rest).toBe("please also update the README");
  });

  it("is not fooled by a person's text", () => {
    expect(crowsnestHeader("Can you look at [crowsnest job j_1]?")).toBeNull();
    expect(crowsnestHeader("crowsnest job j_1")).toBeNull();
  });
});

describe("a reply quoted outside Chat", () => {
  it("drops the SAY line, keeping its sentence when it is all there is", () => {
    expect(quotedReply("SAY: The word in the image is PELICAN.\n\nPELICAN")).toBe("PELICAN");
    expect(quotedReply("SAY: M1 to M3 are merged; M4 is next.")).toBe("M1 to M3 are merged; M4 is next.");
  });
  it("takes out quoted headers", () => {
    expect(quotedReply("On **[crowsnest job j_10] You hold job j_10 (role lead).**: here is where things stand.")).toBe(
      "On **You hold job j_10 (role lead).**: here is where things stand.",
    );
    expect(withoutHeaders("plain words")).toBe("plain words");
  });
});
