// Bloem: libraries on a storage source (useLibraryForm.ts). The harness follows
// Silo's useLibraryForm.save.test.tsx.
import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Library } from "@/api/types";
import { useLibraryForm } from "@/components/admin/libraries/useLibraryForm";

const { mutate } = vi.hoisted(() => ({ mutate: vi.fn() }));
vi.mock("@/hooks/queries/admin/libraries", () => ({
  useCreateLibrary: () => ({ mutate, isPending: false }),
  useUpdateLibrary: () => ({ mutate, isPending: false }),
  useSetLibraryProviders: () => ({ mutate: vi.fn(), isPending: false }),
  useLibraryProviders: () => ({ data: { levels: {} } }),
  useLibraryProviderDefaults: () => ({ data: { levels: {} }, isLoading: false }),
}));
const storageSourcesMock = vi.hoisted(() => ({ sources: [] as unknown[] }));
vi.mock("@/hooks/queries/admin/storageSources", () => ({
  useStorageSources: () => ({ data: storageSourcesMock.sources }),
  storageSourceLabel: (source: { provider_source_id: string; plugin_id: string }) =>
    `${source.provider_source_id} (${source.plugin_id})`,
}));

beforeEach(() => mutate.mockClear());

describe("storage source libraries", () => {
  const source = {
    source_key: "413183ec-ece5-4805-aac4-6c31f6ddf0df",
    owner_kind: "platform",
    plugin_id: "bloem.storage.bookwarehouse",
    provider_source_id: "bookwarehouse",
    enabled: true,
    state: "attached",
    configured: true,
  };
  beforeEach(() => {
    storageSourcesMock.sources = [source];
  });

  it("creates an ebook library on a storage source without paths", () => {
    const { result } = renderHook(() => useLibraryForm({ library: null }));
    expect(result.current.storageOffered).toBe(false);
    act(() => {
      result.current.setName("Bookwarehouse");
      result.current.handleTypeChange("ebooks");
    });
    expect(result.current.storageOffered).toBe(true);
    act(() => result.current.setLocationKind("storage"));
    act(() => {
      result.current.submit();
    });
    expect(result.current.errors.storageSource).toBe("Choose a storage source.");
    expect(mutate).not.toHaveBeenCalled();
    act(() => result.current.setStorageSource(source.source_key));
    act(() => {
      result.current.submit();
    });
    expect(mutate.mock.calls[0]![0]).toMatchObject({
      type: "ebooks",
      paths: [],
      storage_source: source.source_key,
    });
  });

  it("never sends paths or a source when editing a storage library", () => {
    const library = {
      id: 23,
      name: "Bookwarehouse",
      type: "ebooks",
      paths: [],
      storage_source: source.source_key,
      enabled: true,
      metadata_language: "en",
      auto_translate_metadata: false,
      chapter_thumbnails_enabled: false,
      chapter_thumbnails_supported: false,
      intro_detection_enabled: false,
      trailer_kinds: [],
      realtime_monitoring: true,
      sort_order: 0,
      last_scanned_at: null,
    } satisfies Library;
    const { result } = renderHook(() => useLibraryForm({ library }));
    expect(result.current.usesStorage).toBe(true);
    act(() => result.current.setName("Books"));
    act(() => {
      result.current.submit();
    });
    const { body } = mutate.mock.calls[0]![0] as { body: Record<string, unknown> };
    expect(body.name).toBe("Books");
    expect(body.paths).toBeUndefined();
    expect(body.storage_source).toBeUndefined();
  });
});
