import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor, cleanup } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { setAccessToken, setProfileId, setProfileToken } from "@/api/client";
import { useLiveTVAccess } from "./useLiveTVAccess";
let profileId = "viewer-a";
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({ profile: { id: profileId } }) }));
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("discards a late capability response from the previous profile", async () => {
  setAccessToken("access-token");
  setProfileId("viewer-a");
  setProfileToken("pin-a");
  profileId = "viewer-a";
  let finishFirst!: (response: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn<typeof fetch>((_input, init) => {
      const profile = (init?.headers as Record<string, string>)["X-Profile-Id"];
      if (profile === "viewer-a")
        return new Promise((resolve) => {
          finishFirst = resolve;
        });
      return Promise.resolve(
        Response.json({
          supported: true,
          allowed: false,
          available: false,
          heartbeat_interval_seconds: 30,
        }),
      );
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const { result, rerender } = renderHook(() => useLiveTVAccess(), { wrapper });
  await waitFor(() => expect(finishFirst).toBeDefined());
  act(() => {
    profileId = "viewer-b";
    setProfileId("viewer-b");
    setProfileToken("pin-b");
    rerender();
  });
  await waitFor(() => expect(result.current.data?.allowed).toBe(false));
  await act(async () => {
    finishFirst(
      Response.json({
        supported: true,
        allowed: true,
        available: true,
        heartbeat_interval_seconds: 30,
      }),
    );
  });
  expect(result.current.data?.allowed).toBe(false);
  const first = client
    .getQueryCache()
    .findAll()
    .find((query) => query.queryKey.includes("viewer-a"));
  expect(first?.state.data).toBeUndefined();
  client.clear();
});
