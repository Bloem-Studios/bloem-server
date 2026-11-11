import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useSearchMirrorStatus } from "./searchMirror";

const adminV2Api = vi.hoisted(() => vi.fn());
const adminContext = vi.hoisted(() => ({
  value: null as { active: { scope: string; key: string } | null } | null,
}));

vi.mock("@/api/adminV2Client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/adminV2Client")>()),
  adminV2Api,
  captureAdminRequestContext: (key: string) => ({ key, generation: 7 }),
}));
vi.mock("@/contexts/AdminContextProvider", () => ({
  useOptionalAdminContext: () => adminContext.value,
}));

function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  return { client, ...renderHook(() => useSearchMirrorStatus(), { wrapper }) };
}

describe("useSearchMirrorStatus", () => {
  beforeEach(() => {
    adminV2Api.mockReset();
    adminContext.value = null;
  });

  it("reads the status in a platform context, bound to that context's authority", async () => {
    adminContext.value = { active: { scope: "platform", key: "platform" } };
    adminV2Api.mockResolvedValue({ configured: true, state: "following" });

    const { result, client } = setup();

    await waitFor(() =>
      expect(result.current.data).toEqual({ configured: true, state: "following" }),
    );
    expect(adminV2Api).toHaveBeenCalledWith(
      "/search/mirror",
      { signal: expect.any(AbortSignal) },
      "safe",
      { key: "platform", generation: 7 },
    );
    const query = client.getQueryCache().find({
      queryKey: ["admin-v2", "platform", "search-mirror", 7],
    });
    expect(query?.options.staleTime).toBe(15_000);
    expect(query?.options.refetchInterval).toBe(30_000);
  });

  it("does not ask in an organization context", () => {
    adminContext.value = { active: { scope: "organization", key: "organization:north" } };

    const { result } = setup();

    expect(result.current.fetchStatus).toBe("idle");
    expect(adminV2Api).not.toHaveBeenCalled();
  });

  it("does not ask outside an administrative context", () => {
    const { result } = setup();

    expect(result.current.fetchStatus).toBe("idle");
    expect(adminV2Api).not.toHaveBeenCalled();
  });
});
