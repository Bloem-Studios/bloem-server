// Bloem: an existing library on a storage source (useLibraryForm.ts). New
// storage libraries are created through Bloem's library locations. The harness
// follows Silo's useLibraryForm.save.test.tsx.
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

beforeEach(() => mutate.mockClear());

describe("storage source libraries", () => {
  it("never sends paths when editing a storage library", () => {
    const library = {
      id: 23,
      name: "Bookwarehouse",
      type: "ebooks",
      paths: [],
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
  });

  it("creates an ebook library from folders only", () => {
    const { result } = renderHook(() => useLibraryForm({ library: null }));
    act(() => {
      result.current.setName("Books");
      result.current.handleTypeChange("ebooks");
    });
    act(() => {
      result.current.submit();
    });
    expect(mutate).not.toHaveBeenCalled();
    expect(result.current.errors.paths).toBeTruthy();
  });
});
