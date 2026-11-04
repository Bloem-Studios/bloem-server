import { QueryClient, QueryClientProvider, QueryObserver } from "@tanstack/react-query";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { setProfileId } from "@/api/client";
import { createEmptyQueryDefinition } from "@/api/types";
import type { V2Body } from "@/api/v2/request";
import { installPolicyStorageMocks, jsonResponse } from "@/pages/admin-policy/policyTestUtils";
import { createCatalogSearchState, useCatalogWindow } from "./catalog";
import { invalidateMediaSurfaceQueries } from "./mediaSurfaceRefresh";

// Bloem carries this fix ahead of Silo (see contracts/seams.txt). Upstream's
// copy of the test lives in catalogPaging.test.tsx; drop this file with the
// seam once the Silo change is merged.

type CatalogQueryBody = V2Body<"POST /api/v2/catalog/query">;

describe("catalog window under a scan's sweeps", () => {
  beforeEach(() => {
    installPolicyStorageMocks();
    setProfileId("p-owner");
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("lands a jump while the sweeps keep arriving, then sweeps the window", async () => {
    const requests: { body: CatalogQueryBody; signal: AbortSignal | null | undefined }[] = [];
    const heldSeeks: (() => void)[] = [];
    let generation = 0;
    const reply = (body: CatalogQueryBody) => {
      if (!body.cursor) generation++;
      const root = body.cursor?.replace(/^next:\d+:/, "") ?? `root:${generation}`;
      const offset = body.seek ?? Number(body.cursor?.match(/^next:(\d+):/)?.[1] ?? 0);
      return jsonResponse({
        window_cursor: root,
        items: [0, 1].map((index) => ({
          content_id: `item-${offset + index}`,
          title: `Item ${offset + index}`,
          type: "movie",
          status: "matched",
          genres: [],
          keywords: [],
        })),
        total: 0,
        total_exact: false,
        page: { has_more: true, next_cursor: `next:${offset + 2}:${root}` },
      });
    };
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>(async (_url, options) => {
        const body = JSON.parse(String(options?.body)) as CatalogQueryBody;
        const signal = options?.signal;
        requests.push({ body, signal });
        // Each reply arrives as its own task, as a browser's network replies do.
        if (body.seek === undefined) {
          await new Promise((resolve) => setTimeout(resolve, 5));
          return reply(body);
        }
        return new Promise((resolve, reject) => {
          heldSeeks.push(() => resolve(reply(body)));
          signal?.addEventListener(
            "abort",
            () => reject(new DOMException("Aborted", "AbortError")),
            { once: true },
          );
        });
      }),
    );
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    const state = createCatalogSearchState("query", {
      library_id: 7,
      query_definition: { ...createEmptyQueryDefinition(), sort: { field: "title", order: "asc" } },
    });
    const { result } = renderHook(
      () => useCatalogWindow(state, { limit: 2, includeTotal: false, visibleRange: [100, 105] }),
      { wrapper },
    );
    await waitFor(() => expect(heldSeeks).toHaveLength(1));
    const seek = requests.at(-1)!;
    expect(seek.body.seek).toBe(100);

    // A seek deep into a large library outlasts a scan's sweep window. A sweep
    // that cancelled it, or re-keyed it under page 0's next snapshot, would
    // start it over on every sweep, and it would never land.
    for (let sweep = 0; sweep < 3; sweep++) {
      await act(async () => {
        await invalidateMediaSurfaceQueries(queryClient, { libraryId: 7, itemMayBeNew: true });
      });
    }
    expect(seek.signal?.aborted).toBe(false);
    expect(requests.filter(({ body }) => !body.cursor)).toHaveLength(1);

    await act(async () => heldSeeks.shift()!());
    await waitFor(() => expect(result.current.data.pages.get(50)).toHaveLength(2));
    // The page above the jump seeks once the first visible page is in; it is
    // a first read too.
    await waitFor(() => expect(requests.some(({ body }) => body.seek === 98)).toBe(true));
    await act(async () => heldSeeks.splice(0).forEach((finish) => finish()));

    // Once the window's first reads are in, the held sweep reloads it under a
    // new snapshot, showing what it loaded until the reloads land.
    await waitFor(() => expect(requests.filter(({ body }) => !body.cursor)).toHaveLength(2));
    expect(result.current.data.pages.get(50)?.map((item) => item.content_id)).toEqual([
      "item-100",
      "item-101",
    ]);
    heldSeeks.splice(0).forEach((finish) => finish());
    queryClient.clear();
  });

  it("gives a first read that starts late in a hold its own read time", async () => {
    vi.useFakeTimers();
    try {
      const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      const listKey = (offset: number) => ["catalog", "list", { library_id: 7, offset }];
      // A page already on screen, which the held sweep refreshes once it runs.
      queryClient.setQueryData(listKey(0), { items: [] });
      const refreshShown = vi.fn(async () => ({ items: [] }));
      const shown = new QueryObserver(queryClient, {
        queryKey: listKey(0),
        queryFn: refreshShown,
        staleTime: Infinity,
      });
      // A first read that never lands, and one that starts 29 seconds later.
      const early = new QueryObserver(queryClient, {
        queryKey: listKey(100),
        queryFn: () => new Promise<never>(() => undefined),
      });
      let finishLate: (page: { items: never[] }) => void = () => undefined;
      const late = new QueryObserver(queryClient, {
        queryKey: listKey(200),
        queryFn: () => new Promise<{ items: never[] }>((resolve) => (finishLate = resolve)),
      });
      const unsubscribe = [shown.subscribe(() => undefined), early.subscribe(() => undefined)];

      await invalidateMediaSurfaceQueries(queryClient, { libraryId: 7, itemMayBeNew: true });
      await vi.advanceTimersByTimeAsync(29_000);
      unsubscribe.push(late.subscribe(() => undefined));

      // The early read's 30 seconds are up; the late one's are not.
      await vi.advanceTimersByTimeAsync(2_000);
      expect(refreshShown).not.toHaveBeenCalled();

      finishLate({ items: [] });
      await vi.advanceTimersByTimeAsync(0);
      expect(refreshShown).toHaveBeenCalledOnce();
      unsubscribe.forEach((stop) => stop());
      queryClient.clear();
    } finally {
      vi.useRealTimers();
    }
  });
});
