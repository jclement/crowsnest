// Browser notifications for new questions (DESIGN.md 8.3). Permission is asked only from a button
// press (browsers refuse, and users resent, a prompt on page load). A notification is shown only
// when a question is newly asked while the tab is hidden: someone looking at the page sees the
// badge and the list change already.
import type { QuestionEvent } from "./questions";

export type NotificationState = "unsupported" | "default" | "granted" | "denied";

/** The parts of the browser the notifier uses; tests pass fakes. */
export interface NotifyEnvironment {
  hidden: () => boolean;
  Notification?: {
    permission: NotificationPermission;
    requestPermission: () => Promise<NotificationPermission>;
    new (title: string, options?: NotificationOptions): Notification;
  };
}

export function browserEnvironment(): NotifyEnvironment {
  return {
    hidden: () => typeof document !== "undefined" && document.hidden,
    Notification: typeof window !== "undefined" && "Notification" in window ? window.Notification : undefined,
  };
}

export function notificationState(env: NotifyEnvironment = browserEnvironment()): NotificationState {
  return env.Notification ? env.Notification.permission : "unsupported";
}

/** Asks for permission; call only from a click. */
export async function requestNotifications(env: NotifyEnvironment = browserEnvironment()): Promise<NotificationState> {
  if (!env.Notification) return "unsupported";
  return env.Notification.requestPermission();
}

/** Shows a notification for a newly asked question when the tab is hidden and permission is granted. Returns whether it did. */
export function notifyQuestion(event: QuestionEvent, env: NotifyEnvironment = browserEnvironment()): boolean {
  if (event.change !== "asked" || !env.hidden() || env.Notification?.permission !== "granted") return false;
  const title = event.urgency === "blocking" ? "An agent is blocked on you" : event.kind === "notice" ? "Notice from an agent" : "An agent asks";
  try {
    new env.Notification(title, { body: event.title, tag: event.id });
    return true;
  } catch {
    // Some mobile browsers only allow notifications from a service worker; the badge still shows.
    return false;
  }
}
