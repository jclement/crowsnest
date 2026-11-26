import { describe, expect, it, vi } from "vitest";
import { notificationState, notifyQuestion, requestNotifications, type NotifyEnvironment } from "./notifications";
import type { QuestionEvent } from "./questions";

const asked: QuestionEvent = { id: "q_3", status: "open", title: "Ship it?", kind: "confirm", urgency: "blocking", change: "asked" };

function environment(permission: NotificationPermission, hidden: boolean) {
  const shown: { title: string; options?: NotificationOptions }[] = [];
  const FakeNotification = vi.fn(function (this: unknown, title: string, options?: NotificationOptions) {
    shown.push({ title, options });
  }) as unknown as NonNullable<NotifyEnvironment["Notification"]>;
  Object.assign(FakeNotification, { permission, requestPermission: vi.fn(async () => "granted" as const) });
  return { env: { hidden: () => hidden, Notification: FakeNotification } satisfies NotifyEnvironment, shown };
}

describe("notifyQuestion", () => {
  it("notifies a newly asked question while the tab is hidden and permission is granted", () => {
    const { env, shown } = environment("granted", true);
    expect(notifyQuestion(asked, env)).toBe(true);
    expect(shown).toEqual([{ title: "An agent is blocked on you", options: { body: "Ship it?", tag: "q_3" } }]);
  });

  it("stays quiet when the tab is visible, permission is missing, or the question was not newly asked", () => {
    expect(notifyQuestion(asked, environment("granted", false).env)).toBe(false);
    expect(notifyQuestion(asked, environment("default", true).env)).toBe(false);
    expect(notifyQuestion({ ...asked, change: "answered" }, environment("granted", true).env)).toBe(false);
  });

  it("asks for permission only when called, and reports unsupported browsers", async () => {
    const { env } = environment("default", false);
    expect(notificationState(env)).toBe("default");
    await expect(requestNotifications(env)).resolves.toBe("granted");
    expect(notificationState({ hidden: () => false })).toBe("unsupported");
  });
});
