// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { v2 } from "@/api/v2/request";
import { useCheckPlexPin, useCreateHistoryImportRun, useHistoryImportRuns } from "./history-import";

vi.mock("@/api/v2/request", () => ({ v2: vi.fn() }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

afterEach(() => vi.clearAllMocks());

describe("history import v2 hooks", () => {
  it("reads the bounded runs envelope and adapts opaque account identifiers", async () => {
    vi.mocked(v2).mockResolvedValueOnce({ items: [{ id: "run", user_id: "42", mapping_id: "3", status: "completed" }], page: { has_more: false } } as never);
    const { result } = renderHook(() => useHistoryImportRuns(10), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(v2).toHaveBeenCalledWith("GET /api/v2/history-imports/runs", { query: { limit: 10 } });
    expect(result.current.data?.[0]).toMatchObject({ id: "run", user_id: 42, mapping_id: 3 });
  });

  it("starts a run with its target profile and string source identifier", async () => {
    vi.mocked(v2).mockResolvedValueOnce({ id: "new", user_id: "42", profile_id: "target", status: "queued" } as never);
    const { result } = renderHook(() => useCreateHistoryImportRun(), { wrapper: wrapper() });
    await act(async () => {
      await result.current.mutateAsync({ profile_id: "target", source: "plex", source_id: 9 });
    });
    expect(v2).toHaveBeenCalledWith("POST /api/v2/history-imports/runs", { body: { profile_id: "target", source: "plex", source_id: "9" } });
  });

  it("surfaces an ambiguous Plex exchange error without a retry", async () => {
    vi.mocked(v2).mockRejectedValueOnce(new Error("connection closed"));
    const { result } = renderHook(() => useCheckPlexPin("session"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.failureCount).toBe(1);
    expect(v2).toHaveBeenCalledTimes(1);
  });
});
