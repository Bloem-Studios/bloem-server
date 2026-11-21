import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { activateAdminV2Context } from "@/api/adminV2Client";
import type { Library } from "@/api/types";
import { useLibraryForm } from "@/components/admin/libraries/useLibraryForm";
// The Go handler test (internal/api/handlers/bloem_library_locations_test.go)
// posts this same body to the library-locations create.
import storageCreateRaw from "./testdata/storage-library-create.json?raw";
const state = vi.hoisted(() => ({
  active: { key: "platform", scope: "platform" } as { key: string; scope: string },
  mutate: vi.fn(),
  chain: vi.fn(),
}));
vi.mock("@/contexts/AdminContextProvider", () => ({
  useOptionalAdminContext: () => ({ active: state.active }),
}));
vi.mock("@/hooks/queries/admin/storageSources", () => ({
  useStorageSources: () => ({ data: [] }),
  storageSourceLabel: () => "Fixture",
  locationRootName: (root: { name: string }) => root.name || "Root",
}));
vi.mock("@/hooks/queries/admin/libraries", () => ({
  useCreateLibrary: () => ({ mutate: state.mutate, isPending: false }),
  useUpdateLibrary: () => ({ mutate: state.mutate, isPending: false }),
  useSetLibraryProviders: () => ({ mutate: state.chain, isPending: false }),
  useLibraryProviders: () => ({ data: { levels: {} } }),
  useLibraryProviderDefaults: () => ({ data: { levels: {} }, isLoading: false }),
}));
const caps = {
  library_location_browsing: true,
  library_location_atomic_editing: true,
  library_location_mixing: true,
  supported_source_media: [],
  native_audio_libraries: false,
  native_audio_playback: false,
};
const snapshot = {
  revision: 1,
  edit_token: "edit",
  paths: ["/saved"],
  selections: [],
  locations: [],
};
const response = (v: unknown) => new Response(JSON.stringify(v));
beforeEach(() => {
  state.mutate.mockClear();
  state.chain.mockClear();
  state.active = { key: "platform", scope: "platform" };
  activateAdminV2Context("fixture", "platform");
});
it.each([false, true])(
  "retries a failed provider chain without another POST (reload: %s)",
  async (reload) => {
    let creates = 0;
    const created = {
      id: 7,
      name: "Books",
      type: "ebooks",
      paths: ["/saved"],
      enabled: true,
      auto_translate_metadata: false,
    } as Library;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit) => {
        if (init.method === "POST") {
          creates++;
          return response({ library: created, locations: snapshot });
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
    const close = vi.fn();
    const { result } = renderHook(() => useLibraryForm({ library: null, onClose: close }));
    await waitFor(() => expect(result.current.locations.ready).toBe(true));
    act(() => {
      result.current.setName("Books");
      result.current.updatePath(0, "/saved");
      result.current.reorderLevel("ebook", [
        {
          plugin_installation_id: 1,
          capability_id: "metadata",
          provider_slug: "fixture",
          enabled: true,
        },
      ]);
    });
    act(() => expect(result.current.submit().ok).toBe(true));
    await waitFor(() => expect(state.chain).toHaveBeenCalledTimes(1));
    expect(state.chain.mock.calls[0]![0].id).toBe(7);
    // The create carried every option the form holds, so nothing follows it.
    expect(state.mutate).not.toHaveBeenCalled();
    // The failed external mutation never invokes its success callback.
    expect(close).not.toHaveBeenCalled();
    if (reload) {
      await act(async () => result.current.locations.reload());
      await waitFor(() => expect(result.current.locations.ready).toBe(true));
    }
    act(() => expect(result.current.submit().ok).toBe(true));
    await waitFor(() => {
      expect(state.mutate.mock.calls.length + state.chain.mock.calls.length).toBeGreaterThan(1);
    });
    expect(creates).toBe(1);
    if (state.mutate.mock.calls.length) {
      expect(state.mutate.mock.calls[0]![0].id).toBe(7);
      act(() => state.mutate.mock.calls[0]![1].onSuccess());
    }
    await waitFor(() => expect(state.chain).toHaveBeenCalledTimes(2));
    expect(state.chain.mock.calls[1]![0].id).toBe(7);
    act(() => state.chain.mock.calls[1]![1].onSuccess());
    expect(close).toHaveBeenCalledTimes(1);
  },
);
it("saves locations atomically and preserves normal processing settings and metadata chain", async () => {
  let replacement!: { paths: string[] };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === "PUT") {
        replacement = JSON.parse(init.body as string);
        return response({ ...snapshot, paths: replacement.paths });
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
  const library = {
    id: 7,
    name: "Movies",
    type: "movies",
    paths: ["/old"],
    realtime_monitoring: false,
    metadata_language: "fr",
  } as Library;
  const close = vi.fn();
  const { result } = renderHook(() => useLibraryForm({ library, onClose: close }));
  await waitFor(() => expect(result.current.paths).toEqual(["/saved"]));
  act(() => {
    result.current.updatePath(0, "/new");
    result.current.reorderLevel("movie", [
      {
        plugin_installation_id: 1,
        capability_id: "metadata",
        provider_slug: "fixture",
        enabled: true,
      },
    ]);
  });
  act(() => result.current.submit());
  await waitFor(() => expect(state.mutate).toHaveBeenCalled());
  expect(replacement.paths).toEqual(["/new"]);
  const [input, callbacks] = state.mutate.mock.calls[0]!;
  expect(input.body).toMatchObject({ metadata_language: "fr", realtime_monitoring: false });
  expect(input.body.paths).toBeUndefined();
  act(() => callbacks.onSuccess());
  expect(state.chain).toHaveBeenCalledWith(expect.objectContaining({ id: 7 }), expect.anything());
});
// The members of BloemLibraryCreateOptions in contracts/api/bloem/v2/openapi.json.
// The create refuses any other with invalid_request.
const locationCreateMembers = [
  "chapter_thumbnails_enabled",
  "intro_detection_enabled",
  "metadata_language",
  "name",
  "paths",
  "realtime_monitoring",
  "trailer_kinds",
  "trickplay_enabled",
  "type",
];
const storageKey = "00000000-0000-0000-0000-000000000201";
function storageRoot(ref: string) {
  return {
    source_key: storageKey,
    configuration_revision: 1,
    selection_ref: ref,
    name: ref,
    logical_path: ref,
  };
}
// A server with one S3 source for movies and ebooks. The create answers
// library 9 with the server's defaults for enabled and auto-translate.
function stubStorageServer(type: string, ref: string) {
  const writes: { method: string; url: string; body: Record<string, unknown> }[] = [];
  const root = storageRoot(ref);
  const locations = { ...snapshot, paths: [], selections: [root] };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === "POST" || init.method === "PUT") {
        writes.push({ method: init.method, url, body: JSON.parse(init.body as string) });
        if (init.method === "PUT") return response(locations);
        return response({
          library: {
            id: 9,
            name: ref,
            type,
            paths: [],
            enabled: true,
            auto_translate_metadata: false,
          },
          locations,
        });
      }
      if (url.includes("capabilities"))
        return response({
          ...caps,
          supported_source_media: [{ source_kind: "s3", media_kinds: ["movies", "ebooks"] }],
        });
      if (url.includes("browse"))
        return response({ current: root, breadcrumbs: [], directories: [], next_cursor: "" });
      if (url.includes("sources"))
        return response({
          sources: [
            {
              source_key: storageKey,
              plugin_id: "bloem.storage.s3",
              enabled: true,
              configured: true,
            },
          ],
          next_after: null,
        });
      return response(locations);
    }),
  );
  return { writes, root };
}
it("creates a storage-source library with the create's own options, then updates the rest", async () => {
  const { writes, root } = stubStorageServer("ebooks", "books");
  const close = vi.fn();
  const { result } = renderHook(() => useLibraryForm({ library: null, onClose: close }));
  await waitFor(() => expect(result.current.locations.sources).toHaveLength(1));
  act(() => {
    result.current.setName("Books");
    result.current.handleTypeChange("ebooks");
    result.current.setEnabled(false);
    result.current.setAutoTranslateMetadata(true);
    result.current.locations.addSelections([root]);
  });
  act(() => expect(result.current.submit().ok).toBe(true));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]!.url).toMatch(/\/platform\/library-locations$/);
  expect(writes[0]!.body).toEqual(JSON.parse(storageCreateRaw));
  await waitFor(() => expect(state.mutate).toHaveBeenCalledTimes(1));
  expect(writes).toHaveLength(1);
  const [update, callbacks] = state.mutate.mock.calls[0]!;
  expect(update).toEqual({ id: 9, body: { enabled: false, auto_translate_metadata: true } });
  expect(close).not.toHaveBeenCalled();
  act(() => callbacks.onSuccess());
  expect(close).toHaveBeenCalledTimes(1);
});
it("keeps trickplay in a movie library's create and sends box sets after it", async () => {
  const { writes, root } = stubStorageServer("movies", "films");
  const { result } = renderHook(() => useLibraryForm({ library: null }));
  await waitFor(() => expect(result.current.locations.sources).toHaveLength(1));
  act(() => {
    result.current.setName("Films");
    result.current.setTrickplayEnabled(true);
    result.current.setBoxSetsEnabled(true);
    result.current.setBoxSetMinMovies(3);
    result.current.setBoxSetsInSearch(false);
    result.current.locations.addSelections([root]);
  });
  act(() => expect(result.current.submit().ok).toBe(true));
  await waitFor(() => expect(writes).toHaveLength(1));
  const library = writes[0]!.body.library as Record<string, unknown>;
  expect(Object.keys(library).filter((key) => !locationCreateMembers.includes(key))).toEqual([]);
  expect(library.trickplay_enabled).toBe(true);
  await waitFor(() => expect(state.mutate).toHaveBeenCalledTimes(1));
  expect(state.mutate.mock.calls[0]![0]).toEqual({
    id: 9,
    body: { box_sets_enabled: true, box_set_min_movies: 3, box_sets_in_search: false },
  });
});
it("edits the library it created when the update after the create fails", async () => {
  const { writes, root } = stubStorageServer("ebooks", "books");
  const close = vi.fn();
  const { result } = renderHook(() => useLibraryForm({ library: null, onClose: close }));
  await waitFor(() => expect(result.current.locations.sources).toHaveLength(1));
  act(() => {
    result.current.setName("Books");
    result.current.handleTypeChange("ebooks");
    result.current.setEnabled(false);
    result.current.locations.addSelections([root]);
  });
  act(() => expect(result.current.submit().ok).toBe(true));
  await waitFor(() => expect(state.mutate).toHaveBeenCalledTimes(1));
  // The failed update never invokes its success callback.
  expect(close).not.toHaveBeenCalled();
  act(() => expect(result.current.submit().ok).toBe(true));
  await waitFor(() => expect(state.mutate).toHaveBeenCalledTimes(2));
  expect(writes.map((w) => w.method)).toEqual(["POST", "PUT"]);
  expect(writes[1]!.url).toMatch(/\/platform\/library-locations\/9$/);
  const [retry, callbacks] = state.mutate.mock.calls[1]!;
  expect(retry.id).toBe(9);
  expect(retry.body).toMatchObject({ enabled: false });
  act(() => callbacks.onSuccess());
  expect(close).toHaveBeenCalledTimes(1);
});
it("blocks a save while saved selections are still loading", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => new Promise(() => {})),
  );
  const { result } = renderHook(() =>
    useLibraryForm({
      library: { id: 7, name: "Movies", type: "movies", paths: ["/media"] } as Library,
    }),
  );
  act(() => {
    expect(result.current.submit().ok).toBe(false);
  });
  expect(state.mutate).not.toHaveBeenCalled();
});
it("does not save the previous organization's form after switching context", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response({ ...caps, library_location_atomic_editing: false })),
  );
  const { result, rerender } = renderHook(() => useLibraryForm({ library: null }));
  act(() => {
    result.current.setName("Old draft");
    result.current.updatePath(0, "/media");
  });
  await waitFor(() => expect(result.current.locations.loading).toBe(false));
  state.active = { key: "organization:new", scope: "organization" };
  activateAdminV2Context("new", "organization:new");
  rerender();
  await waitFor(() => expect(result.current.locations.loading).toBe(false));
  act(() => {
    expect(result.current.submit().ok).toBe(false);
  });
  expect(state.mutate).not.toHaveBeenCalled();
});
it("keeps the existing save flow on a capability-off backend", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response({ ...caps, library_location_atomic_editing: false })),
  );
  const { result } = renderHook(() => useLibraryForm({ library: null }));
  await waitFor(() => expect(result.current.locations.loading).toBe(false));
  act(() => {
    result.current.setName("Local");
    result.current.updatePath(0, "/media");
  });
  act(() => result.current.submit());
  expect(state.mutate).toHaveBeenCalledWith(
    expect.objectContaining({ name: "Local", paths: ["/media"] }),
    expect.anything(),
  );
});
it("blocks capability-off saves when authority changes before React rerenders", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response({ ...caps, library_location_atomic_editing: false })),
  );
  const { result } = renderHook(() => useLibraryForm({ library: null }));
  await waitFor(() => expect(result.current.locations.loading).toBe(false));
  act(() => {
    result.current.setName("Local");
    result.current.updatePath(0, "/media");
  });
  activateAdminV2Context("different-account", "platform");
  act(() => {
    expect(result.current.submit().ok).toBe(false);
  });
  expect(state.mutate).not.toHaveBeenCalled();
});
it("allows removing the last unavailable plugin folder without provider reads", async () => {
  let replaced = false;
  const unavailable = {
    ...snapshot,
    paths: [],
    selections: [
      {
        source_key: "disabled",
        configuration_revision: 1,
        selection_ref: "",
        name: "Books",
        logical_path: "books",
      },
    ],
    locations: [{ source_key: "disabled", enabled: false, roots: [] }],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === "PUT") {
        replaced = true;
        return response({ ...unavailable, selections: [] });
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
  const { result } = renderHook(() =>
    useLibraryForm({
      library: {
        id: 7,
        name: "Books",
        type: "ebooks",
        paths: [],
      } as unknown as Library,
    }),
  );
  await waitFor(() => expect(result.current.locations.ready).toBe(true));
  act(() => result.current.locations.removeSelection(0));
  act(() => expect(result.current.submit().ok).toBe(true));
  await waitFor(() => expect(replaced).toBe(true));
  expect(vi.mocked(fetch).mock.calls.some(([url]) => String(url).includes("browse"))).toBe(false);
});
