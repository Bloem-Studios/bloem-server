// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { apiWithProfileRequestContext } from "@/api/client";
import { useCampaigns, useHomeCampaignPreference } from "./useCampaigns";
const identity = vi.hoisted(() => ({ version: 1, child: false, profileId: "viewer" }));
vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({
    user: { id: 1 },
    profile: { id: identity.profileId, is_child: identity.child },
  }),
}));
vi.mock("@/api/client", async (original) => ({
  ...(await original<typeof import("@/api/client")>()),
  captureProfileRequestContext: () => ({
    serverOrigin: "https://server.test",
    profileId: identity.profileId,
    authContextVersion: identity.version,
    profileTokenGeneration: 1,
  }),
  isCapturedProfileAuthorityActive: (scope: { authContextVersion: number; profileId: string }) =>
    scope.authContextVersion === identity.version && scope.profileId === identity.profileId,
  apiWithProfileRequestContext: vi.fn(),
}));
const card = {
  id: "message",
  headline: "Server news",
  image_url: "https://cdn.example/card.webp",
  expires_at: "2099-01-01T00:00:00Z",
  dismissible: true,
};
const request = vi.mocked(apiWithProfileRequestContext);
let clients: QueryClient[] = [];
function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}
beforeEach(() => {
  identity.version = 1;
  identity.child = false;
  identity.profileId = "viewer";
  localStorage.clear();
  request.mockReset();
  request.mockResolvedValue({ promotions: [card] });
});
afterEach(() => {
  cleanup();
  for (const client of clients) client.clear();
  clients = [];
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
it("delivers detail cards and persists per-profile dismissals without mutation replay", async () => {
  const hook = renderHook(() => useCampaigns("detail", "movie/id"), { wrapper: wrapper() });
  await waitFor(() => expect(hook.result.current.cards).toHaveLength(1));
  expect(request.mock.calls[0]?.[0]).toBe("/promotions?surface=detail&content_id=movie%2Fid");
  request.mockResolvedValue({ promotions: [] });
  await act(() => hook.result.current.dismiss("message"));
  expect(request).toHaveBeenCalledWith(
    "/home/dismissals/promo%3Adetail/message",
    expect.anything(),
    { method: "PUT", body: "{}" },
    "none",
  );
  await waitFor(() => expect(hook.result.current.cards).toHaveLength(0));
});
it("does not request or render campaigns for child profiles", () => {
  identity.child = true;
  const hook = renderHook(() => useCampaigns("detail", "movie"), { wrapper: wrapper() });
  expect(hook.result.current.eligible).toBe(false);
  expect(hook.result.current.cards).toHaveLength(0);
  expect(request).not.toHaveBeenCalled();
});
it("reads home opt-in layout and its typed promoted items at the server-selected position", async () => {
  request.mockImplementation(async (path) =>
    path.startsWith("/home/layout")
      ? {
          sections: [
            { id: "continue", section_type: "continue_watching" },
            { id: "system-promoted", section_type: "promoted" },
          ],
        }
      : { section: { items: [{ promo: card }] } },
  );
  const hook = renderHook(() => useCampaigns("home", "", true), { wrapper: wrapper() });
  await waitFor(() => expect(hook.result.current.cards).toHaveLength(1));
  expect(hook.result.current.position).toBe(1);
  expect(request.mock.calls.map((call) => call[0])).toEqual([
    "/home/layout?promoted=1",
    "/home/sections/system-promoted/items?promoted=1",
  ]);
});
it("excludes expired delivery even when the server response is cached", async () => {
  request.mockResolvedValue({ promotions: [{ ...card, expires_at: "2000-01-01T00:00:00Z" }] });
  const hook = renderHook(() => useCampaigns("detail", "movie"), { wrapper: wrapper() });
  await waitFor(() => expect(hook.result.current.query.isSuccess).toBe(true));
  expect(hook.result.current.cards).toHaveLength(0);
});
it("cannot publish a previous login's late campaign response into the new session", async () => {
  let finish!: (value: unknown) => void;
  request.mockReturnValueOnce(
    new Promise((resolve) => {
      finish = resolve;
    }),
  );
  const hook = renderHook(() => useCampaigns("detail", "movie"), { wrapper: wrapper() });
  identity.version++;
  request.mockResolvedValue({ promotions: [{ ...card, id: "current" }] });
  hook.rerender();
  await waitFor(() => expect(hook.result.current.cards[0]?.id).toBe("current"));
  await act(async () => {
    finish({ promotions: [{ ...card, id: "previous" }] });
  });
  expect(hook.result.current.cards.map((value) => value.id)).toEqual(["current"]);
});
it("defaults home promotions off and keeps opt-out usable when storage is blocked", () => {
  identity.profileId = "blocked-storage-viewer";
  const hook = renderHook(useHomeCampaignPreference);
  expect(hook.result.current.enabled).toBe(false);
  vi.stubGlobal("localStorage", {
    getItem: vi.fn(() => null),
    setItem: vi.fn(() => {
      throw new Error("Storage blocked");
    }),
    clear: vi.fn(),
  });
  act(() => {
    expect(hook.result.current.setEnabled(true)).toBe(false);
  });
  expect(hook.result.current.enabled).toBe(true);
  act(() => {
    hook.result.current.setEnabled(false);
  });
  expect(hook.result.current.enabled).toBe(false);
});
