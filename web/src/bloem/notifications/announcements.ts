import type { AppNotification } from "@/api/types";

/** A message an administrator published to viewers (Bloem's announcements). */
export function isAnnouncement(notification: AppNotification): boolean {
  return notification.type === "system.announcement" || notification.type === "system.alert";
}
