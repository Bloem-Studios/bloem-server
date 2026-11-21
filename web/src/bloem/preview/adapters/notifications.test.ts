// @vitest-environment jsdom
// Bloem: the notification inbox reads Bloem's native routes, which keep
// Bloem's announcements and alerts and their text (adapters/notifications.ts).
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import type { ProfileRequestContextSnapshot } from "@/api/client";
import { nativeApiWithProfileRequestContext } from "@/api/bloemClient";
import { useUnreadNotificationCount } from "@/hooks/queries/notifications";
import * as inbox from "./notifications";

const context = vi.hoisted(() => ({
  serverOrigin: "https://media.example",
  authContextVersion: 1,
  accessToken: "account-token",
  profileId: "profile-1",
  profileTokenGeneration: 1,
}));
vi.mock("@/api/client", async (original) => ({
  ...(await original<typeof import("@/api/client")>()),
  captureProfileRequestContext: () => context,
  isCapturedProfileAuthorityActive: () => true,
}));
vi.mock("@/api/bloemClient", async (original) => ({
  ...(await original<typeof import("@/api/bloemClient")>()),
  nativeApiWithProfileRequestContext: vi.fn(),
}));
const request = vi.mocked(nativeApiWithProfileRequestContext);
const snapshot = context as unknown as ProfileRequestContextSnapshot;

const notice = {
  id: "notice-1",
  type: "system.announcement",
  profile_id: "profile-1",
  title: "Maintenance tonight",
  body: "The server restarts at ten.",
  severity: "info",
  dismissible: true,
  reason_flags: {},
  created_at: "2026-11-21T17:56:36.899Z",
  read_at: null,
};
const episode = {
  id: "episode-1",
  type: "episode.available",
  profile_id: "profile-1",
  library_id: 3,
  series_title: "Example Show",
  reason_flags: { favorite: true, unknown: "dropped" },
  created_at: "2026-11-21T17:00:00Z",
  read_at: null,
};

beforeEach(() => request.mockReset());

it("lists the inbox from Bloem's routes, keeping an announcement's title and text", async () => {
  request.mockResolvedValueOnce({
    items: [notice, episode],
    page: { has_more: true, next_cursor: "next" },
    read_cutoff: "cutoff",
  });
  const page = await inbox.listNotifications("unread", "previous", snapshot);
  expect(request).toHaveBeenCalledWith(
    "/notifications?status=unread&limit=25&cursor=previous",
    snapshot,
  );
  expect(page.next_cursor).toBe("next");
  expect(page.read_cutoff).toBe("cutoff");
  expect(page.notifications[0]).toMatchObject({
    id: "notice-1",
    title: "Maintenance tonight",
    body: "The server restarts at ten.",
    severity: "info",
  });
  expect(page.notifications[1]).toMatchObject({ library_id: 3, reason_flags: { favorite: true } });
  expect(page.notifications[1]!.reason_flags).not.toHaveProperty("unknown");
});

it("rejects a page that does not page", async () => {
  request.mockResolvedValueOnce({ items: [], page: { has_more: true }, read_cutoff: "cutoff" });
  await expect(inbox.listNotifications("all", undefined, snapshot)).rejects.toThrow();
});

it("counts, reads one and reads all through Bloem's routes", async () => {
  request.mockResolvedValueOnce({ count: 3 }).mockResolvedValue(undefined);
  expect(await inbox.unreadNotificationCount(snapshot)).toBe(3);
  await inbox.markNotificationRead("notice-1", snapshot);
  await inbox.markAllNotificationsRead("cutoff", snapshot);
  expect(request.mock.calls).toEqual([
    ["/notifications/unread-count", snapshot],
    ["/notifications/notice-1/read", snapshot, { method: "POST" }, "none"],
    [
      "/notifications/read-all",
      snapshot,
      { method: "POST", body: JSON.stringify({ through: "cutoff" }) },
      "none",
    ],
  ]);
  await expect(inbox.markAllNotificationsRead("", snapshot)).rejects.toThrow();
});

it("keeps Silo's other notification calls", () => {
  expect(inbox.notificationPreferences).toBeTypeOf("function");
  expect(inbox.listNotificationSeries).toBeTypeOf("function");
});

it("is the module Silo's inbox hooks read through", async () => {
  request.mockResolvedValueOnce({ count: 2 });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { result } = renderHook(() => useUnreadNotificationCount(), {
    wrapper: ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client }, children),
  });
  await waitFor(() => expect(result.current.data).toBe(2));
  expect(request).toHaveBeenCalledWith("/notifications/unread-count", expect.anything());
  client.clear();
});
