import type { CreateLibraryRequest } from "@/api/types";
import type { RootSelection } from "./types";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { activateAdminV2Context, clearAdminV2Context } from "@/api/adminV2Client";
import { useLibraryLocations } from "./useLibraryLocations";
const state = vi.hoisted(() => ({
  active: { key: "platform", scope: "platform" } as { key: string; scope: string } | null,
}));
vi.mock("@/contexts/AdminContextProvider", () => ({
  useOptionalAdminContext: () => ({ active: state.active }),
}));
const caps = {
  library_location_browsing: true,
  library_location_atomic_editing: true,
  library_location_mixing: false,
  native_audio_libraries: false,
  native_audio_playback: false,
  supported_source_media: [{ source_kind: "s3", media_kinds: ["movies", "ebooks"] }],
};
const snapshot = {
  revision: 1,
  edit_token: "original-token",
  paths: [],
  selections: [
    {
      source_key: "s3",
      configuration_revision: 1,
      selection_ref: "old-ref",
      name: "Books",
      logical_path: "books",
    },
  ],
  locations: [{ source_key: "s3", roots: [], enabled: true }],
};
function response(value: unknown) {
  return new Response(JSON.stringify(value));
}
beforeEach(() => {
  state.active = { key: "platform", scope: "platform" };
  activateAdminV2Context("fixture", "platform");
  vi.restoreAllMocks();
});
it("loads all source pages including an empty nonterminal page", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) =>
      response(
        url.includes("capabilities")
          ? caps
          : url.includes("after=")
            ? { sources: [{ source_key: "s3" }], next_after: null }
            : { sources: [], next_after: "next" },
      ),
    ),
  );
  const { result } = renderHook(() => useLibraryLocations(null, "movies"));
  await waitFor(() => expect(result.current.sources).toHaveLength(1));
});
it("ignores a delayed response from the previous organization", async () => {
  let release!: (value: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) =>
      url.includes("capabilities")
        ? new Promise<Response>((resolve) => {
            release = resolve;
          })
        : Promise.resolve(response({ sources: [], next_after: null })),
    ),
  );
  const { result, rerender } = renderHook(() => useLibraryLocations(null, "movies"));
  await waitFor(() => expect(release).toBeDefined());
  state.active = null;
  clearAdminV2Context();
  rerender();
  await act(async () => release(response(caps)));
  expect(result.current.enabled).toBe(false);
});
it("refreshes retained references at save and keeps original edit token", async () => {
  let reads = 0;
  let saved!: { paths: string[]; selections: RootSelection[]; expected_edit_token: string };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === "PUT") {
        saved = JSON.parse(init.body as string);
        return response(snapshot);
      }
      if (url.includes("capabilities")) return response(caps);
      if (url.includes("sources")) return response({ sources: [], next_after: null });
      return response({
        ...snapshot,
        edit_token: reads === 0 ? "original-token" : "fresh-token",
        selections: [
          { ...snapshot.selections[0], selection_ref: ++reads === 1 ? "old-ref" : "fresh-ref" },
        ],
      });
    }),
  );
  const { result } = renderHook(() => useLibraryLocations(7, "ebooks"));
  await waitFor(() => expect(result.current.ready).toBe(true));
  await act(async () => {
    await result.current.save({ name: "Books", type: "ebooks", paths: [] } as CreateLibraryRequest);
  });
  expect(saved).toMatchObject({
    expected_edit_token: "original-token",
    selections: [{ selection_ref: "fresh-ref" }],
  });
});
it("requires explicit reload on conflict without retrying a write", async () => {
  let writes = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === "PUT") {
        writes++;
        return new Response(
          JSON.stringify({ error: "revision_conflict", message: "Reload locations" }),
          { status: 409 },
        );
      }
      return response(
        url.includes("capabilities")
          ? caps
          : url.includes("sources")
            ? { sources: [], next_after: null }
            : snapshot,
      );
    }),
  );
  const { result } = renderHook(() => useLibraryLocations(7, "ebooks"));
  await waitFor(() => expect(result.current.ready).toBe(true));
  await act(async () => {
    await result.current.save({ name: "Books", type: "ebooks", paths: [] });
  });
  expect(result.current.conflict).toBe(true);
  expect(result.current.ready).toBe(false);
  expect(writes).toBe(1);
  await act(async () => {
    await result.current.reload();
  });
  expect(result.current.conflict).toBe(false);
  expect(writes).toBe(1);
});
it("removes an unavailable selection without reading the disabled provider", async () => {
  const unavailable = {
    ...snapshot,
    selections: [{ ...snapshot.selections[0], selection_ref: "" }],
  };
  let saved!: { paths: string[]; selections: RootSelection[]; expected_edit_token: string };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === "PUT") {
        saved = JSON.parse(init.body as string);
        return response(unavailable);
      }
      return response(
        url.includes("capabilities")
          ? caps
          : url.includes("sources")
            ? { sources: [], next_after: null }
            : unavailable,
      );
    }),
  );
  const { result } = renderHook(() => useLibraryLocations(7, "ebooks"));
  await waitFor(() => expect(result.current.ready).toBe(true));
  act(() => result.current.removeSelection(0));
  await act(async () => {
    await result.current.save({ name: "Books", type: "ebooks", paths: [] });
  });
  expect(saved.selections).toEqual([]);
});
it("rejects saves captured before account or organization changes", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) =>
      response(
        url.includes("capabilities")
          ? caps
          : url.includes("sources")
            ? { sources: [], next_after: null }
            : snapshot,
      ),
    ),
  );
  const { result } = renderHook(() => useLibraryLocations(7, "ebooks"));
  await waitFor(() => expect(result.current.ready).toBe(true));
  const save = result.current.save;
  activateAdminV2Context("other", "organization:other");
  await act(async () => {
    expect(await save({ name: "Books", type: "ebooks", paths: [] })).toBeNull();
  });
  expect(vi.mocked(fetch).mock.calls.some(([, init]) => init?.method === "PUT")).toBe(false);
});
it("does not silently adopt a new source configuration during reference refresh", async () => {
  let reads = 0;
  let writes = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === "PUT") writes++;
      return response(
        url.includes("capabilities")
          ? caps
          : url.includes("sources")
            ? { sources: [], next_after: null }
            : {
                ...snapshot,
                selections: [
                  { ...snapshot.selections[0], configuration_revision: ++reads === 1 ? 1 : 2 },
                ],
              },
      );
    }),
  );
  const { result } = renderHook(() => useLibraryLocations(7, "ebooks"));
  await waitFor(() => expect(result.current.ready).toBe(true));
  await act(async () => {
    expect(await result.current.save({ name: "Books", type: "ebooks", paths: [] })).toBeNull();
  });
  expect(writes).toBe(0);
  expect(result.current.conflict).toBe(true);
});
it("mixing off counts retained sources even after their roots are removed", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) =>
      response(
        url.includes("capabilities")
          ? caps
          : url.includes("sources")
            ? { sources: [], next_after: null }
            : snapshot,
      ),
    ),
  );
  const { result } = renderHook(() => useLibraryLocations(7, "ebooks"));
  await waitFor(() => expect(result.current.ready).toBe(true));
  act(() => result.current.removeSelection(0));
  expect(result.current.shapeError(["/new"])).toContain("not ready");
  act(() => result.current.addSelections([{ ...snapshot.selections[0]!, source_key: "another" }]));
  expect(result.current.shapeError([])).toContain("not ready");
});
it("creates filesystem plus two S3 prefixes and a Bookwarehouse collection when mixing is advertised", async () => {
  const roots = [
    { ...snapshot.selections[0]!, name: "First", logical_path: "first", selection_ref: "first" },
    { ...snapshot.selections[0]!, name: "Second", logical_path: "second", selection_ref: "second" },
    {
      ...snapshot.selections[0]!,
      source_key: "bw",
      name: "Collection",
      logical_path: "collection",
      selection_ref: "collection",
    },
  ];
  let created!: { library: CreateLibraryRequest; selections: RootSelection[] };
  const sources = [
    { source_key: "s3", plugin_id: "bloem.storage.s3", enabled: true, configured: true },
    { source_key: "bw", plugin_id: "bloem.storage.bookwarehouse", enabled: true, configured: true },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === "POST") {
        created = JSON.parse(init.body as string);
        return response({
          library: { id: 9, name: "Mixed books" },
          locations: { ...snapshot, paths: ["/books"], selections: roots },
        });
      }
      if (url.includes("browse")) {
        const ref = new URL(url, "http://fixture").searchParams.get("directory_ref");
        const root = roots.find((r) => r.selection_ref === ref)!;
        return response({ current: root, breadcrumbs: [], directories: [], next_cursor: "" });
      }
      return response(
        url.includes("capabilities")
          ? {
              ...caps,
              library_location_mixing: true,
              supported_source_media: [
                ...caps.supported_source_media,
                { source_kind: "bookwarehouse", media_kinds: ["ebooks"] },
              ],
            }
          : { sources, next_after: null },
      );
    }),
  );
  const { result } = renderHook(() => useLibraryLocations(null, "ebooks"));
  await waitFor(() => expect(result.current.sources).toHaveLength(2));
  act(() => result.current.addSelections(roots));
  await act(async () => {
    await result.current.save({
      name: "Mixed books",
      type: "ebooks",
      paths: ["/books"],
      metadata_language: "fr",
    } as CreateLibraryRequest);
  });
  expect(created.library).toMatchObject({ paths: ["/books"], metadata_language: "fr" });
  expect(created.selections).toHaveLength(3);
  expect(created.selections[0]).not.toHaveProperty("logical_path");
});
it("does not permit another create after an unknown mutation outcome even on reload", async () => {
  let writes = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === "POST") {
        writes++;
        return new Response(
          JSON.stringify({ error: "mutation_outcome_unknown", message: "Reconcile creation" }),
          { status: 503 },
        );
      }
      return response(url.includes("capabilities") ? caps : { sources: [], next_after: null });
    }),
  );
  const { result } = renderHook(() => useLibraryLocations(null, "ebooks"));
  await waitFor(() => expect(result.current.ready).toBe(true));
  await act(async () => {
    await result.current.save({
      name: "Books",
      type: "ebooks",
      paths: ["/books"],
    } as CreateLibraryRequest);
  });
  await act(async () => {
    await result.current.reload();
  });
  await act(async () => {
    await result.current.save({
      name: "Books",
      type: "ebooks",
      paths: ["/books"],
    } as CreateLibraryRequest);
  });
  expect(writes).toBe(1);
});
it("keeps distinct opaque directories even when their display labels match", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) =>
      response(
        url.includes("capabilities")
          ? { ...caps, library_location_mixing: true }
          : {
              sources: [
                {
                  source_key: "s3",
                  plugin_id: "bloem.storage.s3",
                  enabled: true,
                  configured: true,
                },
              ],
              next_after: null,
            },
      ),
    ),
  );
  const { result } = renderHook(() => useLibraryLocations(null, "ebooks"));
  await waitFor(() => expect(result.current.sources).toHaveLength(1));
  act(() =>
    result.current.addSelections([
      { ...snapshot.selections[0]!, selection_ref: "first-directory" },
    ]),
  );
  act(() =>
    result.current.addSelections([
      { ...snapshot.selections[0]!, selection_ref: "different-directory" },
    ]),
  );
  expect(result.current.selections).toHaveLength(2);
});
it("refreshes a new opaque directory instead of reusing a retained root with identical labels", async () => {
  let saved!: { selections: RootSelection[] };
  let browsed = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === "PUT") {
        saved = JSON.parse(init.body as string);
        return response(snapshot);
      }
      if (url.includes("browse")) {
        browsed = true;
        return response({ current: { ...snapshot.selections[0], selection_ref: "new-fresh-ref" } });
      }
      return response(
        url.includes("capabilities")
          ? caps
          : url.includes("sources")
            ? {
                sources: [
                  {
                    source_key: "s3",
                    plugin_id: "bloem.storage.s3",
                    enabled: true,
                    configured: true,
                  },
                ],
                next_after: null,
              }
            : snapshot,
      );
    }),
  );
  const { result } = renderHook(() => useLibraryLocations(7, "ebooks"));
  await waitFor(() => expect(result.current.ready).toBe(true));
  act(() => {
    result.current.removeSelection(0);
    result.current.addSelections([{ ...snapshot.selections[0]!, selection_ref: "new-directory" }]);
  });
  await act(async () => {
    await result.current.save({ name: "Books", type: "ebooks", paths: [] });
  });
  expect(browsed).toBe(true);
  expect(saved.selections[0]!.selection_ref).toBe("new-fresh-ref");
});
