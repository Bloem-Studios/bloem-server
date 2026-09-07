/** Named v2 fields from the bound-client timeline handoff, pending generation. */
import type { ProgressTimelineV3 } from "@/player/protocol-v3";
export type ProgressTimeline = ProgressTimelineV3;

export interface BoundAcceptedProgress {
  sequence: number;
  position: number;
  is_paused: boolean;
  timeline_id: string;
  item_position: number;
}

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Audiobook timeline is unavailable");
  return value as Record<string, unknown>;
}
function seconds(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0;
}

/** Copy the server mapping; UI metadata never supplies an offset or duration. */
export function readProgressTimeline(
  value: unknown,
  mediaItemId: string,
  fileId: string,
): Readonly<ProgressTimeline> {
  const wire = object(value);
  if (
    typeof wire.timeline_id !== "string" ||
    !/^[a-f0-9]{64}$/.test(wire.timeline_id) ||
    wire.media_item_id !== mediaItemId ||
    wire.file_id !== fileId ||
    !mediaItemId ||
    !/^[1-9]\d*$/.test(fileId) ||
    !seconds(wire.part_offset_seconds) ||
    !seconds(wire.part_duration_seconds) ||
    wire.part_duration_seconds === 0 ||
    !seconds(wire.duration_seconds) ||
    wire.duration_seconds === 0 ||
    wire.part_offset_seconds + wire.part_duration_seconds > wire.duration_seconds
  )
    throw new Error("Audiobook timeline does not match the selected part");
  return Object.freeze({
    timeline_id: wire.timeline_id,
    media_item_id: mediaItemId,
    file_id: fileId,
    part_offset_seconds: wire.part_offset_seconds,
    part_duration_seconds: wire.part_duration_seconds,
    duration_seconds: wire.duration_seconds,
  });
}

/** The receipt describes the accepted stored sequence, not the attempted sample. */
export function readBoundAcceptedProgress(
  value: unknown,
  timeline: Readonly<ProgressTimeline>,
): BoundAcceptedProgress {
  const wire = object(value);
  if (
    typeof wire.sequence !== "number" ||
    !Number.isSafeInteger(wire.sequence) ||
    wire.sequence < 0 ||
    wire.timeline_id !== timeline.timeline_id ||
    typeof wire.is_paused !== "boolean" ||
    !seconds(wire.position) ||
    wire.position > timeline.part_duration_seconds ||
    !seconds(wire.item_position) ||
    wire.item_position !== timeline.part_offset_seconds + wire.position
  )
    throw new Error("Audiobook progress receipt does not match its timeline");
  return {
    sequence: wire.sequence,
    position: wire.position,
    is_paused: wire.is_paused,
    timeline_id: timeline.timeline_id,
    item_position: wire.item_position,
  };
}

export interface PlaybackTimelineManifest {
  installation_id: string;
  timeline_id: string;
  media_item_id: string;
  edition_id: string;
  duration_seconds: number;
  parts: ReadonlyArray<
    Readonly<{ file_id: string; offset_seconds: number; duration_seconds: number }>
  >;
}

export function readTimelineManifest(
  value: unknown,
  installationId: string,
  mediaItemId: string,
  anchorFileId: string,
): Readonly<PlaybackTimelineManifest> {
  const wire = object(value);
  if (
    wire.installation_id !== installationId ||
    !installationId ||
    wire.media_item_id !== mediaItemId ||
    !mediaItemId ||
    typeof wire.edition_id !== "string" ||
    !wire.edition_id ||
    typeof wire.timeline_id !== "string" ||
    !/^[a-f0-9]{64}$/.test(wire.timeline_id) ||
    !seconds(wire.duration_seconds) ||
    wire.duration_seconds === 0 ||
    !Array.isArray(wire.parts) ||
    !wire.parts.length ||
    wire.parts.length > 4096
  )
    throw new Error("Audiobook manifest is unavailable or belongs to another selection");
  let offset = 0;
  const ids = new Set<string>();
  const parts = wire.parts.map((value: unknown) => {
    const part = object(value);
    if (
      typeof part.file_id !== "string" ||
      !/^[1-9]\d*$/.test(part.file_id) ||
      !Number.isSafeInteger(Number(part.file_id)) ||
      ids.has(part.file_id) ||
      part.offset_seconds !== offset ||
      !seconds(part.duration_seconds) ||
      part.duration_seconds === 0
    )
      throw new Error("Audiobook manifest has invalid part membership, order or duration");
    ids.add(part.file_id);
    offset += part.duration_seconds;
    return Object.freeze({
      file_id: part.file_id,
      offset_seconds: part.offset_seconds as number,
      duration_seconds: part.duration_seconds,
    });
  });
  if (!ids.has(anchorFileId) || offset !== wire.duration_seconds)
    throw new Error("Audiobook manifest does not match its anchor or total duration");
  return Object.freeze({
    installation_id: installationId,
    timeline_id: wire.timeline_id,
    media_item_id: mediaItemId,
    edition_id: wire.edition_id,
    duration_seconds: wire.duration_seconds,
    parts: Object.freeze(parts),
  });
}

export function selectTimelinePart(
  manifest: Readonly<PlaybackTimelineManifest>,
  itemPosition: number,
) {
  if (!seconds(itemPosition) || itemPosition > manifest.duration_seconds)
    throw new Error("Audiobook position is outside its timeline");
  const index = manifest.parts.findIndex(
    (part, index) =>
      itemPosition < part.offset_seconds + part.duration_seconds ||
      index === manifest.parts.length - 1,
  );
  const part = manifest.parts[index]!;
  return { index, part, localPosition: itemPosition - part.offset_seconds };
}

export function validateSelectedTimeline(
  value: unknown,
  manifest: Readonly<PlaybackTimelineManifest>,
  fileId: string,
) {
  const timeline = readProgressTimeline(value, manifest.media_item_id, fileId);
  const part = manifest.parts.find((part) => part.file_id === fileId);
  if (
    !part ||
    timeline.timeline_id !== manifest.timeline_id ||
    timeline.part_offset_seconds !== part.offset_seconds ||
    timeline.part_duration_seconds !== part.duration_seconds ||
    timeline.duration_seconds !== manifest.duration_seconds
  )
    throw new Error("Audiobook timeline changed; start a new explicit playback request");
  return timeline;
}

/** Internal chapter intent; global offsets are resolved only from discovery. */
export interface AudiobookChapterIntent {
  fileId: string;
  positionSeconds: number;
}
