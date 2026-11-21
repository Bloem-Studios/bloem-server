// Bloem: useLibraryForm with Bloem's library locations beside Silo's form. The
// form counts a location save as pending, and a save that finishes after the
// administrative context changed leaves the form open. The harness follows
// Silo's useLibraryForm.save.test.tsx.
import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Library } from "@/api/types";
import { useLibraryForm } from "@/components/admin/libraries/useLibraryForm";

const { update, setChain, locationState } = vi.hoisted(() => ({
  update: vi.fn(),
  setChain: vi.fn(),
  locationState: { pending: false, current: true },
}));
vi.mock("@/hooks/queries/admin/libraries", () => ({
  useCreateLibrary: () => ({ mutate: vi.fn(), isPending: false }),
  useUpdateLibrary: () => ({ mutate: update, isPending: false }),
  useSetLibraryProviders: () => ({ mutate: setChain, isPending: false }),
  useLibraryProviders: () => ({ data: { levels: {} } }),
  useLibraryProviderDefaults: () => ({ data: { levels: {} }, isLoading: false }),
}));
vi.mock("@/bloem/library-locations/useLibraryLocations", async (importActual) => {
  const actual =
    await importActual<typeof import("@/bloem/library-locations/useLibraryLocations")>();
  return {
    ...actual,
    useLibraryLocations: (...args: Parameters<typeof actual.useLibraryLocations>) => {
      const real = actual.useLibraryLocations(...args);
      return {
        ...real,
        pending: locationState.pending,
        contextCurrent: () => locationState.current && real.contextCurrent(),
      };
    },
  };
});

const library = {
  id: 7,
  name: "Movies",
  type: "movies",
  paths: ["/media/movies"],
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

beforeEach(() => {
  update.mockClear();
  setChain.mockClear();
  locationState.pending = false;
  locationState.current = true;
});

describe("useLibraryForm with Bloem's library locations", () => {
  it("is pending while a location save is", () => {
    const { result, rerender } = renderHook(() => useLibraryForm({ library }));
    expect(result.current.isPending).toBe(false);
    locationState.pending = true;
    rerender();
    expect(result.current.isPending).toBe(true);
  });

  for (const current of [true, false]) {
    it(`${current ? "closes" : "stays open"} when the provider chain saves ${current ? "in" : "after leaving"} its context`, () => {
      const onClose = vi.fn();
      const { result } = renderHook(() => useLibraryForm({ library, onClose }));
      act(() => result.current.toggleLocalMetadata(false));
      act(() => {
        result.current.submit();
      });
      const [, updateOptions] = update.mock.calls[0]! as [unknown, { onSuccess: () => void }];
      act(() => updateOptions.onSuccess());
      const [, chainOptions] = setChain.mock.calls[0]! as [unknown, { onSuccess: () => void }];
      locationState.current = current;
      act(() => chainOptions.onSuccess());
      expect(onClose).toHaveBeenCalledTimes(current ? 1 : 0);
    });
  }
});
