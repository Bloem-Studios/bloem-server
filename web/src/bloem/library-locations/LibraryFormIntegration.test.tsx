import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { activateAdminV2Context } from "@/api/adminV2Client";
import type { Library } from "@/api/types";
import { useLibraryForm } from "@/components/admin/libraries/useLibraryForm";
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
        storage_source: "disabled",
      } as unknown as Library,
    }),
  );
  await waitFor(() => expect(result.current.locations.ready).toBe(true));
  act(() => result.current.locations.removeSelection(0));
  act(() => expect(result.current.submit().ok).toBe(true));
  await waitFor(() => expect(replaced).toBe(true));
  expect(vi.mocked(fetch).mock.calls.some(([url]) => String(url).includes("browse"))).toBe(false);
});
