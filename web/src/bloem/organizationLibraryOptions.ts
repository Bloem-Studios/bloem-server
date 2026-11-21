import type { Library } from "@/api/types";
import type { OrganizationLibraryProjection } from "@/hooks/queries/admin/organizationLibraries";

/**
 * An organization's libraries as the Library rows Silo's LibraryAccessSelector
 * takes. The selector reads a row's id, name, type and enabled flag; the other
 * Library fields are neutral values it never shows. A library is enabled for
 * the organization when it owns it or holds an active entitlement to it.
 */
export function organizationLibraryOptions(
  libraries: readonly OrganizationLibraryProjection[],
): Library[] {
  return libraries.map((library) => ({
    id: library.folder_id,
    name: library.name,
    type: library.type,
    enabled: library.access_kind === "owned" || library.entitlement?.status === "active",
    paths: [],
    metadata_language: "",
    auto_translate_metadata: false,
    chapter_thumbnails_enabled: false,
    chapter_thumbnails_supported: false,
    intro_detection_enabled: false,
    trailer_kinds: [],
    realtime_monitoring: false,
    sort_order: 0,
    last_scanned_at: null,
  }));
}
