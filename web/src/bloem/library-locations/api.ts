import { adminV2Api, type AdminRequestContext } from "@/api/adminV2Client";
import type { CreateLibraryRequest, Library } from "@/api/types";
import type {
  LocationCapabilities,
  MixedLibraryLocations,
  RootSelection,
  StoragePage,
  StorageSource,
} from "./types";
export function locationApi(scope: "platform" | "organization", context: AdminRequestContext) {
  const base = `/${scope}`;
  function read<T>(path: string, signal?: AbortSignal) {
    return adminV2Api<T>(base + path, { signal }, "safe", context);
  }
  return {
    capabilities: (signal?: AbortSignal) =>
      read<LocationCapabilities>("/native-storage/capabilities", signal),
    async sources(signal?: AbortSignal) {
      const sources: StorageSource[] = [];
      const seen = new Set<string>();
      let after: string | null = null;
      do {
        const page: { sources: StorageSource[]; next_after: string | null } = await read(
          `/native-storage/sources?limit=100${after ? `&after=${encodeURIComponent(after)}` : ""}`,
          signal,
        );
        sources.push(...page.sources);
        after = page.next_after;
        if (after && seen.has(after))
          throw new Error("Source pagination did not advance. Reload sources.");
        if (after) seen.add(after);
      } while (after);
      return sources;
    },
    browse: (key: string, ref = "", cursor = "", signal?: AbortSignal) => {
      const query = new URLSearchParams({ limit: "100" });
      if (ref) query.set("directory_ref", ref);
      if (cursor) query.set("cursor", cursor);
      return read<StoragePage>(
        `/native-storage/sources/${encodeURIComponent(key)}/browse?${query}`,
        signal,
      );
    },
    read: (id: number, signal?: AbortSignal) =>
      read<MixedLibraryLocations>(`/library-locations/${id}`, signal),
    replace: (
      id: number,
      paths: string[],
      selections: RootSelection[],
      original: MixedLibraryLocations,
    ) =>
      adminV2Api<MixedLibraryLocations>(
        `${base}/library-locations/${id}`,
        {
          method: "PUT",
          body: JSON.stringify({
            paths,
            selections: selections.map(({ source_key, configuration_revision, selection_ref }) => ({
              source_key,
              configuration_revision,
              selection_ref,
            })),
            expected_revision: original.revision,
            expected_edit_token: original.edit_token,
          }),
        },
        "none",
        context,
      ),
    create: (library: CreateLibraryRequest, selections: RootSelection[]) =>
      adminV2Api<{ library: Library; locations: MixedLibraryLocations }>(
        `${base}/library-locations`,
        {
          method: "POST",
          body: JSON.stringify({
            library,
            selections: selections.map(({ source_key, configuration_revision, selection_ref }) => ({
              source_key,
              configuration_revision,
              selection_ref,
            })),
          }),
        },
        "none",
        context,
      ),
  };
}
export type LocationApi = ReturnType<typeof locationApi>;
