// Bloem: the notification inbox reads Bloem's native routes
// (/api/bloem/v2/notifications). They serve every row whole, Bloem's
// announcements and alerts included, with their title and message. Silo's
// /api/v2 routes serve Silo's apps the inbox a Silo server would, so they
// leave those out (docs/architecture/admin-announcements.md).
//
// Only the inbox list, unread count and read marks change; preferences,
// series and channels stay Silo's. bloem-preview-plugin.ts gives this module
// to hooks/queries/notifications.ts in place of Silo's.
import { t } from "@/i18n";
import type { AppNotification, NotificationReasonFlags } from "@/api/types";
import { nativeApiWithProfileRequestContext } from "@/api/bloemClient";
import {
  captureNotificationAuthority,
  requireNotificationAuthority,
} from "../../../api/v2/notifications";

export * from "../../../api/v2/notifications";

interface NativePage {
  items?: unknown;
  page?: { has_more?: unknown; next_cursor?: unknown };
  read_cutoff?: unknown;
}

type NativeRow = Omit<AppNotification, "library_id" | "reason_flags"> & {
  library_id?: unknown;
  reason_flags?: unknown;
};

/** Silo's row mapping (api/v2/notifications.ts): a known library ID and only
 * the reason flags the inbox reads. */
function notification(row: NativeRow): AppNotification {
  const libraryID = row.library_id === undefined ? undefined : Number(row.library_id);
  if (libraryID !== undefined && (!Number.isSafeInteger(libraryID) || libraryID <= 0))
    throw new Error(t("web.common.errors.notificationLibraryIdUnsupported"));
  const reasonFlags: NotificationReasonFlags = {};
  const value = row.reason_flags;
  if (value && typeof value === "object") {
    const raw = value as Record<string, unknown>;
    for (const key of [
      "favorite",
      "watchlist",
      "continue_watching",
      "next_up",
      "follower",
    ] as const)
      if (key in raw && typeof raw[key] === "boolean") reasonFlags[key] = raw[key];
    for (const key of ["request_id", "media_type", "title", "reason"] as const)
      if (key in raw && typeof raw[key] === "string") reasonFlags[key] = raw[key];
    for (const key of ["tmdb_id", "year"] as const)
      if (key in raw && typeof raw[key] === "number") reasonFlags[key] = raw[key];
  }
  return { ...row, library_id: libraryID, reason_flags: reasonFlags };
}

export async function listNotifications(
  status: "all" | "unread",
  cursor?: string,
  profileContext = captureNotificationAuthority(),
) {
  requireNotificationAuthority(profileContext);
  const query = new URLSearchParams({ status, limit: "25" });
  if (cursor) query.set("cursor", cursor);
  const body = await nativeApiWithProfileRequestContext<NativePage>(
    `/notifications?${query}`,
    profileContext,
  );
  requireNotificationAuthority(profileContext);
  const page = body?.page;
  if (
    !body ||
    !Array.isArray(body.items) ||
    !page ||
    typeof page.has_more !== "boolean" ||
    (page.has_more &&
      (typeof page.next_cursor !== "string" || !page.next_cursor || page.next_cursor === cursor)) ||
    (!page.has_more && page.next_cursor) ||
    typeof body.read_cutoff !== "string" ||
    !body.read_cutoff
  )
    throw new Error(t("web.common.errors.notificationPageInvalid"));
  return {
    notifications: (body.items as NativeRow[]).map(notification),
    next_cursor: page.has_more ? (page.next_cursor as string) : undefined,
    read_cutoff: body.read_cutoff,
  };
}

export async function unreadNotificationCount(profileContext = captureNotificationAuthority()) {
  requireNotificationAuthority(profileContext);
  const body = await nativeApiWithProfileRequestContext<{ count?: unknown }>(
    "/notifications/unread-count",
    profileContext,
  );
  requireNotificationAuthority(profileContext);
  if (typeof body?.count !== "number")
    throw new Error(t("web.common.errors.notificationPageInvalid"));
  return body.count;
}

export async function markNotificationRead(
  id: string,
  profileContext = captureNotificationAuthority(),
) {
  requireNotificationAuthority(profileContext);
  await nativeApiWithProfileRequestContext(
    `/notifications/${encodeURIComponent(id)}/read`,
    profileContext,
    { method: "POST" },
    "none",
  );
  requireNotificationAuthority(profileContext);
}

export async function markAllNotificationsRead(
  through: string,
  profileContext = captureNotificationAuthority(),
) {
  requireNotificationAuthority(profileContext);
  if (!through) throw new Error(t("web.common.errors.notificationsReloadBeforeMarkingRead"));
  await nativeApiWithProfileRequestContext(
    "/notifications/read-all",
    profileContext,
    { method: "POST", body: JSON.stringify({ through }) },
    "none",
  );
  requireNotificationAuthority(profileContext);
}
