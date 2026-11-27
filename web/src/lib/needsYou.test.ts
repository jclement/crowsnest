import { describe, expect, it } from "vitest";
import { fixtureApprovals } from "./approvalFixtures";
import { activeItems, itemLink, kindLabel, plainPreview, whereLine, type AttentionItem, type AttentionSnapshot } from "./attention";
import { fixtureSnapshot } from "./attentionFixtures";
import { trayItemOf, trayItems, trayTitle } from "./needsYou";
import { fixtureQuestions } from "./questionFixtures";

const snapshot = (): AttentionSnapshot => fixtureSnapshot(fixtureApprovals, fixtureQuestions);

describe("trayItems", () => {
  it("keeps the server's order: the dangerous approval first, blocking before can-wait", () => {
    const items = trayItems(snapshot());
    expect(items[0]?.id).toBe("approval:ap_3");
    const firstCanWait = items.findIndex((item) => item.item.urgency === "can_wait");
    expect(items.slice(firstCanWait).every((item) => item.item.urgency === "can_wait")).toBe(true);
  });

  it("is exactly the unsnoozed items, so the tray and the badge show one number", () => {
    const data = snapshot();
    const snoozedId = data.items[1]!.id;
    data.items[1] = { ...data.items[1]!, snoozed_until: "2099-01-01T00:00:00Z" };
    const items = trayItems(data);
    expect(items.map((item) => item.id)).not.toContain(snoozedId);
    expect(items).toHaveLength(activeItems(data).length);
  });

  it("gives each kind the payload its card needs", () => {
    const [approval] = trayItems(snapshot());
    expect(approval?.kind).toBe("approval");
    if (approval?.kind === "approval") expect(approval.approval.id).toBe("ap_3");
    expect(trayTitle(approval!)).toBe("Bash: git push --force origin main");
    const agent: AttentionItem = {
      id: "agent:lead-x", kind: "agent", urgency: "blocking", rank: 48, since: "", title: "The lead of X needs you", project: null,
      action: { kind: "terminal", label: "Open terminal" }, standing: { name: "lead-x", label: "The lead of X", project_id: 3, pane_id: "w9:p1", state: "needs_terminal" },
    };
    expect(trayItemOf(agent)?.kind).toBe("agent");
    expect(trayItemOf({ ...agent, standing: undefined })).toBeNull();
  });
});

describe("attention helpers", () => {
  const item: AttentionItem = {
    id: "dialog:w3:p1", kind: "dialog", urgency: "blocking", rank: 40, since: "", title: "Dialog: Rebuild the docs", project: { id: 2, slug: "website", name: "Website" },
    job_id: "j_5", job_title: "Rebuild the docs", action: { kind: "dialog", label: "Answer the dialog" },
  };

  it("says where an item belongs: the project, then the job when the title is not already it", () => {
    expect(whereLine(item)).toBe("Website · Rebuild the docs");
    expect(whereLine({ ...item, project: null, job_title: item.title })).toBe("Unfiled");
  });

  it("links each kind to where it opens in full", () => {
    expect(itemLink(item)).toEqual({ to: "/inbox", search: { q: "dialog:w3:p1" } });
    expect(itemLink({ ...item, id: "job:j_5", kind: "job" })).toEqual({ to: "/jobs", search: { job: "j_5" } });
  });

  it("names kinds the same everywhere", () => {
    expect(kindLabel(item)).toBe("Terminal dialog");
    expect(kindLabel({ ...item, kind: "question", urgency: "can_wait" })).toBe("Question");
  });

  it("strips markdown for a preview", () => {
    expect(plainPreview("The refresh now `**coalesces**` events.\n\n```go\nx := 1\n```\n- **bold** and [a link](http://x)")).toBe(
      "The refresh now coalesces events. bold and a link",
    );
  });
});
