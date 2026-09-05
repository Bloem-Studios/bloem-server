import { api } from "@/api/client";
import { useQuery } from "@tanstack/react-query";
import type {
  BrowseResponse,
  LibraryCollection,
  LibraryTabCollection,
  LibraryTabResponse,
  ServerVisibleUserCollection,
} from "@/api/types";
import { libraryCollectionTabFromV2, userCollectionFromV2 } from "@/api/v2/catalog";
import { v2 } from "@/api/v2/request";
import { libraryCollectionKeys } from "./keys";

export function libraryCollectionsQueryOptions(libraryId: number) {
  return {
    queryKey: libraryCollectionKeys.list(libraryId),
    queryFn: ({ signal }: { signal?: AbortSignal }): Promise<LibraryTabResponse> =>
      v2("GET /api/v2/library/{id}/collections", { path: { id: String(libraryId) }, signal }).then(
        libraryCollectionTabFromV2,
      ),
    enabled: Number.isFinite(libraryId) && libraryId > 0,
  };
}

export function useLibraryCollections(libraryId: number) {
  return useQuery(libraryCollectionsQueryOptions(libraryId));
}

export function flattenLibraryCollections(
  resp: LibraryTabResponse | undefined,
): LibraryTabCollection[] {
  if (!resp) return [];
  const out: LibraryTabCollection[] = [];
  for (const group of resp.groups ?? []) {
    out.push(...(group.collections ?? []));
  }
  out.push(...(resp.ungrouped?.collections ?? []));
  return out;
}

export function useLibraryUserCollections(libraryId: number) {
  return useQuery({
    queryKey: libraryCollectionKeys.userContributed(libraryId),
    queryFn: ({ signal }): Promise<ServerVisibleUserCollection[]> =>
      v2("GET /api/v2/library/{id}/user-collections", {
        path: { id: String(libraryId) },
        signal,
      }).then((data) => data.items.map(userCollectionFromV2)),
    enabled: Number.isFinite(libraryId) && libraryId > 0,
  });
}

export function getLibraryCollectionList(
  resp: LibraryTabResponse | undefined,
): LibraryCollection[] {
  return resp?.collections ?? [];
}

// Collection item reads stay on v1 until v2 provides stable continuation.
export function useLibraryCollectionItems(libraryId: number, collectionId: string | null) {
  return useQuery({
    queryKey: libraryCollectionKeys.items(libraryId, collectionId ?? ""),
    queryFn: ({ signal }) =>
      api<BrowseResponse>(
        `/library/${libraryId}/collections/${encodeURIComponent(collectionId ?? "")}/items`,
        { signal },
      ),
    enabled:
      Number.isFinite(libraryId) &&
      libraryId > 0 &&
      collectionId !== null &&
      collectionId.length > 0,
  });
}
