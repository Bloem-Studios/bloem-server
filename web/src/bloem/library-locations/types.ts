import type { StorageSource } from "@/hooks/queries/admin/storageSources";
export type { StorageSource };
export interface StorageDirectory {
  selection_ref: string;
  name: string;
  logical_path: string;
}
export interface StoragePage {
  current: StorageDirectory;
  breadcrumbs: StorageDirectory[];
  directories: StorageDirectory[];
  next_cursor: string;
}
export interface RootSelection extends StorageDirectory {
  source_key: string;
  configuration_revision: number;
}
export interface MixedLibraryLocations {
  revision: number;
  edit_token: string;
  paths: string[];
  selections: RootSelection[];
  locations: Array<{
    source_key: string;
    enabled: boolean;
    roots: Array<{
      id: string;
      directory_id: string;
      name: string;
      logical_path: string;
      enabled: boolean;
    }>;
  }>;
}
export interface LocationCapabilities {
  library_location_browsing: boolean;
  library_location_atomic_editing: boolean;
  library_location_mixing: boolean;
  supported_source_media: Array<{ source_kind: string; media_kinds: string[] }>;
  native_audio_libraries: boolean;
  native_audio_playback: boolean;
}
export function sourceKind(source: StorageSource): string | null {
  return source.plugin_id === "bloem.storage.s3"
    ? "s3"
    : source.plugin_id === "bloem.storage.bookwarehouse"
      ? "bookwarehouse"
      : null;
}
export function supportsSource(
  caps: LocationCapabilities,
  source: StorageSource,
  type: string,
): boolean {
  const kind =
    type === "ebook"
      ? "ebooks"
      : type === "audiobook"
        ? "audiobooks"
        : type === "movie"
          ? "movies"
          : type;
  if (kind === "audiobooks" && !(caps.native_audio_libraries && caps.native_audio_playback))
    return false;
  // A Bookwarehouse source serves one feed, chosen at installation: its
  // audiobook feed is rooted at audio-root, its ebook feed at root.
  if (
    sourceKind(source) === "bookwarehouse" &&
    (source.root_entry_id === "audio-root") !== (kind === "audiobooks")
  )
    return false;
  return (
    source.enabled &&
    source.configured &&
    caps.supported_source_media.some(
      (s) => s.source_kind === sourceKind(source) && s.media_kinds.includes(kind),
    )
  );
}
