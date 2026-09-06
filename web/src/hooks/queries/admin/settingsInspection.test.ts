import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ v2: vi.fn() }));
vi.mock("@/api/v2/request", () => ({ v2: mocks.v2 }));
import { useAdminServerSettings, useAdminRestartKeys, useAdminSensitiveStatus } from "./settings";

function wrapper({ children }: { children: ReactNode }) {
  return createElement(
    QueryClientProvider,
    { client: new QueryClient({ defaultOptions: { queries: { retry: false } } }) },
    children,
  );
}

describe("administrator settings inspection", () => {
  it.each([
    [
      useAdminServerSettings,
      "GET /api/v2/admin/settings/effective",
      { "server.log_level": "debug" },
    ],
    [useAdminRestartKeys, "GET /api/v2/admin/settings/restart-keys", { keys: [], prefixes: [] }],
    [
      useAdminSensitiveStatus,
      "GET /api/v2/admin/settings/sensitive-status",
      { configured: [], managed_by_env: [] },
    ],
  ] as const)("uses v2 for %s", async (hook, operation, response) => {
    mocks.v2.mockResolvedValueOnce(response);
    const { result } = renderHook(() => hook(), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual(response);
    expect(mocks.v2).toHaveBeenLastCalledWith(operation);
  });
});
