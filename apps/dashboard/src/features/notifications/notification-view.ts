export type NotificationView = "lista" | "grid";

const storagePrefix = "dashboard.notifications.view.";

export function notificationViewStorageKey(membershipId?: string): string {
  return `${storagePrefix}${membershipId || "default"}`;
}

export function readNotificationView(membershipId?: string): NotificationView {
  if (typeof window === "undefined") return "lista";
  try {
    return window.localStorage.getItem(notificationViewStorageKey(membershipId)) === "grid" ? "grid" : "lista";
  } catch {
    return "lista";
  }
}

export function persistNotificationView(membershipId: string | undefined, view: NotificationView): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(notificationViewStorageKey(membershipId), view);
  } catch {
    // A blocked or unavailable localStorage should not prevent the dashboard from working.
  }
}
