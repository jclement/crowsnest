import { describe, expect, it } from "vitest";
import { makeFleet } from "./fixtures";
import { makeJob } from "./jobFixtures";
import {
  buildItems,
  doItems,
  flatten,
  keepSelection,
  loadRecents,
  matchQuality,
  parseQuery,
  pushRecent,
  rankItems,
  RECENTS_KEY,
  saveRecents,
  scoreItem,
  type PaletteItem,
  type PaletteSources,
} from "./palette";
import { makeQuestion } from "./questionFixtures";

function sources(overrides: Partial<PaletteSources> = {}): PaletteSources {
  return {
    jobs: [
      makeJob({ id: "j_1", title: "Fix the login redirect", state: "running", agent_name: "job-1", dir: "~/src/web", pane_id: "" }),
      makeJob({ id: "j_2", title: "Write release notes", state: "done", settled_at: new Date().toISOString() }),
      makeJob({ id: "j_3", title: "Migrate the database", state: "running", stuck: true, cause: "limit", pane_id: "w2:p1" }),
    ],
    questions: [
      makeQuestion({ id: "q_7", title: "Deploy to staging?", kind: "confirm", status: "open", urgency: "blocking", options: [{ key: "y", label: "Yes" }, { key: "n", label: "No" }], recommend: "n" }),
      makeQuestion({ id: "q_8", title: "Which colour?", kind: "choice", multi: true, status: "open", options: [{ key: "r", label: "Red" }] }),
      makeQuestion({ id: "q_9", title: "Saw the notice", kind: "ack", status: "open" }),
    ],
    fleet: makeFleet(),
    orchestrator: true,
    auth: true,
    theme: "system",
    speak: null,
    voiceSettings: false,
    ...overrides,
  };
}

const ids = (items: { item: PaletteItem }[]) => items.map((row) => row.item.id);

describe("parseQuery", () => {
  it("forces Give job with > or a job prefix", () => {
    expect(parseQuery(">  fix it")).toEqual({ mode: "job", text: "fix it" });
    expect(parseQuery("job fix it")).toEqual({ mode: "job", text: "fix it" });
    expect(parseQuery("JOB   fix it")).toEqual({ mode: "job", text: "fix it" });
  });

  it("leaves other queries alone", () => {
    expect(parseQuery("jobs")).toEqual({ mode: "all", text: "jobs" });
    expect(parseQuery("  inbox ")).toEqual({ mode: "all", text: "inbox" });
  });
});

describe("doItems", () => {
  it("offers nothing for an empty query", () => {
    expect(doItems(parseQuery("   "))).toEqual([]);
  });

  it("offers Give job and Send to orchestrator for free text", () => {
    const items = doItems(parseQuery("tidy the readme"));
    expect(items.map((item) => item.title)).toEqual(["Give job: tidy the readme", "Send to orchestrator: tidy the readme"]);
    expect(items[0]?.command).toEqual({ kind: "giveJob", brief: "tidy the readme", open: false });
    expect(items[0]?.actions?.[0]?.command).toEqual({ kind: "jobForm", brief: "tidy the readme" });
    expect(items[1]?.command).toEqual({ kind: "ask", text: "tidy the readme" });
    // ⌘Enter: the full form for a job, Send and open Chat for a prompt.
    expect(items[0]?.alt?.command).toEqual({ kind: "jobForm", brief: "tidy the readme" });
    expect(items[1]?.alt?.command).toEqual({ kind: "ask", text: "tidy the readme", open: true });
  });

  it("offers only Give job in job mode, with the prefix removed from the brief", () => {
    const items = doItems(parseQuery("> inbox"));
    expect(items.map((item) => item.id)).toEqual(["do:job"]);
    expect(items[0]?.command).toEqual({ kind: "giveJob", brief: "inbox", open: false });
  });

  it("shortens a long text in the title but gives the whole brief", () => {
    const long = "x".repeat(200);
    const [item] = doItems(parseQuery(long));
    expect(item?.title.length).toBeLessThan(100);
    expect(item?.command).toEqual({ kind: "giveJob", brief: long, open: false });
  });
});

describe("buildItems", () => {
  it("builds job items with state chips and the secondary actions", () => {
    const items = buildItems(sources());
    const job = items.find((item) => item.id === "job:j_3");
    expect(job?.chip).toEqual({ label: "Stuck", tone: "warning" });
    expect(job?.command).toEqual({ kind: "navigate", to: "/", search: { job: "j_3" } });
    expect(job?.actions?.map((action) => action.id)).toEqual(["open", "resume", "nudge", "message", "cancel"]);
    // ⌘Enter never does something risky by default: no alt on a job (it opens the actions instead).
    expect(job?.alt).toBeUndefined();
    const cancel = job?.actions?.find((action) => action.id === "cancel");
    expect(cancel?.command).toMatchObject({ kind: "confirm", confirm: { title: "Cancel j_3?", confirmLabel: "Cancel the job", command: { kind: "jobAction", id: "j_3", action: "cancel" } } });
    // A finished job only opens.
    expect(items.find((item) => item.id === "job:j_2")?.actions?.map((action) => action.id)).toEqual(["open"]);
    // No pane, no message.
    expect(items.find((item) => item.id === "job:j_1")?.actions?.map((action) => action.id)).toEqual(["open", "nudge", "cancel"]);
  });

  it("puts a confirm question's options as answers, and not a multi-select's", () => {
    const items = buildItems(sources());
    const confirm = items.find((item) => item.id === "q:q_7");
    expect(confirm?.group).toBe("needs");
    expect(confirm?.chip?.label).toBe("Blocking");
    expect(confirm?.actions?.map((action) => action.label)).toEqual(["Open in Inbox", "Answer: Yes", "Answer: No (recommended)"]);
    expect(confirm?.actions?.[1]?.command).toEqual({ kind: "answer", questionId: "q_7", value: "y", label: "Yes" });
    expect(items.find((item) => item.id === "q:q_8")?.actions?.map((action) => action.id)).toEqual(["open"]);
    expect(items.find((item) => item.id === "q:q_9")?.actions?.map((action) => action.label)).toEqual(["Open in Inbox", "Acknowledge"]);
  });

  it("lists dialogs, agents and plain panes from the fleet", () => {
    const fleet = makeFleet();
    const items = buildItems(sources({ fleet }));
    const blocked = fleet.agents.find((agent) => agent.needs_you);
    expect(blocked).toBeDefined();
    expect(items.find((item) => item.id === `dialog:${blocked?.pane_id}`)?.command).toEqual({ kind: "navigate", to: "/inbox", search: { q: `dialog:${blocked?.pane_id}` } });
    const withTranscript = fleet.agents.find((agent) => agent.has_transcript);
    const agentItem = items.find((item) => item.id === `agent:${withTranscript?.pane_id}`);
    expect(agentItem?.command).toEqual({ kind: "navigate", to: "/fleet", search: { agent: withTranscript?.pane_id, view: "conversation" } });
    expect(agentItem?.actions?.map((action) => action.id)).toEqual(["conversation", "terminal"]);
    expect(agentItem?.alt?.id).toBe("terminal");
    expect(items.some((item) => item.id.startsWith("pane:"))).toBe(true);
  });

  it("leaves out what does not apply", () => {
    const items = buildItems(sources({ orchestrator: false, auth: false }));
    expect(items.some((item) => item.id === "action:poke")).toBe(false);
    expect(items.some((item) => item.id === "action:restart")).toBe(false);
    expect(items.some((item) => item.id === "signout")).toBe(false);
    expect(items.some((item) => item.id === "action:speak")).toBe(false);
    expect(items.some((item) => item.id === "sheet:voice")).toBe(false);
  });

  it("offers Speak replies and Voice settings when the voice build provides them", () => {
    const items = buildItems(sources({ speak: { on: false }, voiceSettings: true }));
    const speak = items.find((item) => item.id === "action:speak");
    expect(speak?.title).toBe("Speak replies: off");
    expect(speak?.command).toEqual({ kind: "speak", on: true });
    expect(items.some((item) => item.id === "sheet:voice")).toBe(true);
    expect(buildItems(sources({ speak: { on: true } })).find((item) => item.id === "action:speak")?.title).toBe("Speak replies: on");
  });

  it("asks before relaunching the orchestrator, in the Chat menu's words", () => {
    const restart = buildItems(sources()).find((item) => item.id === "action:restart");
    expect(restart?.command).toMatchObject({ kind: "confirm", confirm: { title: "Restart the orchestrator?", command: { kind: "relaunch", action: "restart" } } });
  });

  it("marks the current theme", () => {
    const items = buildItems(sources({ theme: "dark" }));
    expect(items.find((item) => item.id === "theme:dark")?.subtitle).toBe("Current");
    expect(items.find((item) => item.id === "theme:light")?.subtitle).toBeUndefined();
  });
});

describe("scoreItem", () => {
  const item: PaletteItem = { id: "x", group: "jobs", title: "Fix the login redirect", subtitle: "j_1 · running tests", keywords: ["j_1", "Running", "~/src/web"], icon: "job", command: { kind: "poke" }, verb: "Open" };

  it("needs every token somewhere", () => {
    expect(scoreItem(["login", "j1"], item)).not.toBeNull();
    expect(scoreItem(["login", "zzz"], item)).toBeNull();
  });

  it("highlights title hits and subtitle hits, not keyword hits", () => {
    const scored = scoreItem(["login"], item)!;
    expect(scored.titlePositions).toEqual([8, 9, 10, 11, 12]);
    expect(scored.subtitlePositions).toEqual([]);
    expect(scoreItem(["tests"], item)?.subtitlePositions).toEqual([14, 15, 16, 17, 18]);
    expect(scoreItem(["web"], item)?.titlePositions).toEqual([]);
  });

  it("counts the title more than other fields", () => {
    const inTitle: PaletteItem = { ...item, title: "Deploy", keywords: [] };
    const inKeyword: PaletteItem = { ...item, title: "Something", subtitle: undefined, keywords: ["Deploy"] };
    expect(scoreItem(["deploy"], inTitle)!.score).toBeGreaterThan(scoreItem(["deploy"], inKeyword)!.score);
  });
});

describe("rankItems", () => {
  const all = () => buildItems(sources());

  it("with no query shows recents first, then what needs you, views, live jobs and actions", () => {
    const groups = rankItems(all(), "", ["nav:/fleet", "job:j_2", "gone:item"]);
    expect(groups.map((group) => group.id)).toEqual(["recent", "needs", "navigate", "jobs", "actions"]);
    expect(ids(groups[0]!.items)).toEqual(["nav:/fleet", "job:j_2"]);
    // Not repeated below.
    expect(ids(groups[2]!.items)).not.toContain("nav:/fleet");
    // Finished jobs are not offered without a query.
    expect(ids(groups[3]!.items)).toEqual(["job:j_3", "job:j_1"]);
  });

  it("puts a strong match first and Give job second", () => {
    const groups = rankItems(all(), "inbox", []);
    expect(groups[0]?.id).toBe("navigate");
    expect(groups[0]?.items[0]?.item.id).toBe("nav:/inbox");
    expect(groups[1]?.id).toBe("do");
  });

  it("puts Give job first when nothing matches well", () => {
    const groups = rankItems(all(), "rewrite the onboarding flow for new users", []);
    expect(groups[0]?.id).toBe("do");
    expect(flatten(groups)[0]?.item.id).toBe("do:job");
    expect(flatten(groups)[1]?.item.id).toBe("do:ask");
  });

  it("always offers Give job for free text, even when nothing else matches", () => {
    const groups = rankItems(all(), "qqqq", []);
    expect(ids(flatten(groups))).toEqual(["do:job", "do:ask"]);
  });

  it("offers only Give job in job mode", () => {
    expect(ids(flatten(rankItems(all(), "> inbox", [])))).toEqual(["do:job"]);
    expect(rankItems(all(), ">", [])).toEqual([]);
  });

  it("finds jobs by id, state, agent and directory", () => {
    expect(flatten(rankItems(all(), "j_3", [])).find((row) => row.item.group === "jobs")?.item.id).toBe("job:j_3");
    expect(ids(flatten(rankItems(all(), "job-1", [])))).toContain("job:j_1");
    expect(ids(flatten(rankItems(all(), "src/web", [])))).toContain("job:j_1");
    const stuck = flatten(rankItems(all(), "stuck", [])).filter((row) => row.item.group === "jobs");
    expect(stuck[0]?.item.id).toBe("job:j_3");
  });

  it("orders within a group by score and gives recents a nudge", () => {
    const theme = rankItems(all(), "theme", []).find((group) => group.id === "navigate");
    expect(theme?.items.length).toBe(3);
    const boosted = rankItems(all(), "theme", ["theme:dark"]).find((group) => group.id === "navigate");
    expect(boosted?.items[0]?.item.id).toBe("theme:dark");
  });

  it("caps each group", () => {
    const many = Array.from({ length: 30 }, (_, index) => makeJob({ id: `j_${index + 10}`, title: `Task ${index}`, state: "running" }));
    const groups = rankItems(buildItems(sources({ jobs: many })), "task", []);
    expect(groups.find((group) => group.id === "jobs")?.items.length).toBe(10);
  });
});

describe("matchQuality", () => {
  it("is about 1 for a word-start run and lower for scattered letters", () => {
    const tight = scoreItem(["inbox"], { id: "a", group: "navigate", title: "Inbox", icon: "inbox", command: { kind: "poke" }, verb: "Go" })!;
    const loose = scoreItem(["ibx"], { id: "a", group: "navigate", title: "Inbox", icon: "inbox", command: { kind: "poke" }, verb: "Go" })!;
    expect(matchQuality(tight.score, ["inbox"])).toBeGreaterThan(1);
    expect(matchQuality(loose.score, ["ibx"])).toBeLessThan(matchQuality(tight.score, ["inbox"]));
    expect(matchQuality(10, [])).toBe(0);
  });
});

describe("keepSelection", () => {
  const rows = (...list: string[]) => list.map((id) => ({ item: { id } }));

  it("follows the selected item when rows move", () => {
    expect(keepSelection(rows("a", "b", "c"), "b", 1)).toBe(1);
    expect(keepSelection(rows("new", "a", "b", "c"), "b", 1)).toBe(2);
  });

  it("stays at the same index, clamped, when the item is gone", () => {
    expect(keepSelection(rows("a", "c"), "b", 1)).toBe(1);
    expect(keepSelection(rows("a"), "b", 3)).toBe(0);
    expect(keepSelection([], "b", 0)).toBe(-1);
    expect(keepSelection(rows("a", "b"), null, -1)).toBe(0);
  });
});

describe("recents", () => {
  it("keeps the newest first, without duplicates, and skips one-off items", () => {
    let recents: string[] = [];
    recents = pushRecent(recents, "nav:/chat");
    recents = pushRecent(recents, "job:j_1");
    recents = pushRecent(recents, "nav:/chat");
    recents = pushRecent(recents, "do:job");
    recents = pushRecent(recents, "q:q_7");
    expect(recents).toEqual(["nav:/chat", "job:j_1"]);
    for (let index = 0; index < 20; index++) recents = pushRecent(recents, `nav:${index}`);
    expect(recents.length).toBe(12);
  });

  it("round-trips through storage and survives junk", () => {
    const store = new Map<string, string>();
    const storage = { getItem: (key: string) => store.get(key) ?? null, setItem: (key: string, value: string) => void store.set(key, value) };
    saveRecents(["nav:/chat"], storage);
    expect(loadRecents(storage)).toEqual(["nav:/chat"]);
    store.set(RECENTS_KEY, "{not json");
    expect(loadRecents(storage)).toEqual([]);
    store.set(RECENTS_KEY, JSON.stringify([1, "nav:/", null]));
    expect(loadRecents(storage)).toEqual(["nav:/"]);
  });
});
