import type { CreateLibraryRequest, Library } from "@/api/types";
import type { V2Body } from "@/api/v2/request";

/**
 * The library half of a library-locations create: BloemLibraryCreateOptions in
 * contracts/api/bloem/v2/openapi.json. It refuses unknown members, and it has
 * no enabled, auto-translate or box set members; those follow as an update
 * ({@link afterLocationCreate}).
 */
export type LocationCreateOptions = Pick<
  CreateLibraryRequest,
  | "name"
  | "paths"
  | "type"
  | "metadata_language"
  | "chapter_thumbnails_enabled"
  | "intro_detection_enabled"
  | "trailer_kinds"
  | "realtime_monitoring"
  | "trickplay_enabled"
>;

/** The create's own members of the form's body; members left undefined are not sent. */
export function locationCreateOptions(body: CreateLibraryRequest): LocationCreateOptions {
  return {
    name: body.name,
    paths: body.paths,
    type: body.type,
    metadata_language: body.metadata_language,
    chapter_thumbnails_enabled: body.chapter_thumbnails_enabled,
    intro_detection_enabled: body.intro_detection_enabled,
    trailer_kinds: body.trailer_kinds,
    realtime_monitoring: body.realtime_monitoring,
    trickplay_enabled: body.trickplay_enabled,
  };
}

/**
 * What a library-locations create could not carry, as an update of the
 * library it made: enabled and auto-translate where they differ from what the
 * create made, and the box set settings, which the form sends only when they
 * change and the create's answer does not include. Null when nothing is left.
 */
export function afterLocationCreate(
  body: CreateLibraryRequest,
  created: Library,
): V2Body<"PATCH /api/v2/libraries/{id}"> | null {
  const update: V2Body<"PATCH /api/v2/libraries/{id}"> = {};
  if (body.enabled !== undefined && body.enabled !== created.enabled) update.enabled = body.enabled;
  if (
    body.auto_translate_metadata !== undefined &&
    body.auto_translate_metadata !== created.auto_translate_metadata
  )
    update.auto_translate_metadata = body.auto_translate_metadata;
  if (body.box_sets_enabled !== undefined) update.box_sets_enabled = body.box_sets_enabled;
  if (body.box_set_min_movies !== undefined) update.box_set_min_movies = body.box_set_min_movies;
  if (body.box_sets_in_search !== undefined) update.box_sets_in_search = body.box_sets_in_search;
  return Object.keys(update).length ? update : null;
}
