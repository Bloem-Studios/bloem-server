import { beforeEach, afterEach, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor, act, cleanup } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { setAccessToken, setProfileId, setProfileToken } from "@/api/client";
import {
  notificationInboxKeys,
  useNotifications,
  applyNotificationRead,
  applyNotificationCreated,
} from "./notifications";
const row = {
  id: "delivery-1",
  type: "episode.available",
  profile_id: "owner",
  reason_flags: {},
  created_at: "2026-01-01T00:00:00.000Z",
  read_at: null,
};
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  setAccessToken("account");
  setProfileId("owner");
  setProfileToken(null);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("invalidates cutoff events without marking ambiguous millisecond deliveries read", () => {
  const client = new QueryClient();
  const key = notificationInboxKeys.list("all");
  client.setQueryData(key, {
    pages: [{ notifications: [row, { ...row, id: "delivery-2" }], read_cutoff: "frozen" }],
    pageParams: [undefined],
  });
  client.setQueryData(notificationInboxKeys.count(), 2);
  applyNotificationRead(client, {
    profile_id: "owner",
    through_created_at: "2026-01-01T00:00:00.000100Z",
    through_id: "delivery-1",
  });
  expect(client.getQueryState(key)?.isInvalidated).toBe(true);
  expect(client.getQueryData(notificationInboxKeys.count())).toBe(2);
  expect(client.getQueryData(key)).toMatchObject({
    pages: [{ notifications: [{ read_at: null }, { read_at: null }], read_cutoff: "frozen" }],
  });
});
it("preserves single-item and legacy all-read reducers and ignores another profile", () => {
  const client = new QueryClient();
  const key = notificationInboxKeys.list("all");
  client.setQueryData(key, { pages: [{ notifications: [row] }], pageParams: [undefined] });
  client.setQueryData(notificationInboxKeys.count(), 1);
  applyNotificationRead(client, { profile_id: "other", all: true });
  expect(client.getQueryData(notificationInboxKeys.count())).toBe(1);
  applyNotificationRead(client, { profile_id: "owner", id: row.id });
  expect(client.getQueryData(notificationInboxKeys.count())).toBe(0);
  applyNotificationRead(client, { profile_id: "owner", all: true });
  expect(client.getQueryData(key)).toMatchObject({
    pages: [{ notifications: [{ id: row.id, read_at: expect.any(String) }] }],
  });
});
it("realtime prepends retain the displayed cutoff", () => {
  const client = new QueryClient();
  const key = notificationInboxKeys.list("all");
  client.setQueryData(key, {
    pages: [{ notifications: [row], read_cutoff: "original" }],
    pageParams: [undefined],
  });
  applyNotificationCreated(client, { ...row, id: "new" });
  expect(client.getQueryData(key)).toMatchObject({
    pages: [{ notifications: [{ id: "new" }, { id: row.id }], read_cutoff: "original" }],
  });
});
it("validates cursor traversal while allowing ordinary refetch of cached pages", async () => {
  const client = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  const fetch = vi.fn<typeof globalThis.fetch>(async (input) => {
    const next = new URL(String(input), "http://localhost").searchParams.has("cursor");
    return new Response(
      JSON.stringify({
        items: [{ ...row, id: next ? "two" : "one" }],
        page: next ? { has_more: false } : { has_more: true, next_cursor: "next" },
        read_cutoff: "cutoff",
      }),
      { headers: { "Content-Type": "application/json" } },
    );
  });
  vi.stubGlobal("fetch", fetch);
  const { result } = renderHook(() => useNotifications(), { wrapper });
  await waitFor(() => expect(result.current.data?.pages).toHaveLength(1));
  await act(async () => {
    await result.current.fetchNextPage();
  });
  await waitFor(() => expect(result.current.data?.pages).toHaveLength(2));
  await act(async () => {
    await result.current.refetch();
  });
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(4));
  expect(result.current.isError).toBe(false);
});
