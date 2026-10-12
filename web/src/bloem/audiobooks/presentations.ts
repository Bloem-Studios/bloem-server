// Audiobook presentations: the complete recordings an audiobook item carries.
//
// An item can hold a local Silo recording and a native Bloem storage recording
// of the same book. Both arrive in today's file-version wire; this module
// groups the versions into presentations so the existing player, chapter list
// and progress reporting see exactly one of them. It is the web source of
// truth for the mixed-locations Task 11 client contract.
import type { FileVersion, ItemDetail, PlaybackVariant, VersionChapter } from "@/api/types";
import type { AudiobookFile } from "@/lib/audiobooks/types";

/** Public capability token: native presentations can be played on this server. */
export const NATIVE_AUDIOBOOK_PRESENTATIONS_TOKEN = "native_audiobook_presentations_v1";

const NATIVE_KEY_PREFIX = "bloem-storage:";
const MULTIPART_KINDS = new Set(["multipart", "audiobook_part"]);

export interface AudiobookPresentation {
  /** `group:<presentation_group_key>` for a multipart recording, else `file:<file_id>`. */
  id: string;
  /** The wire presentation_group_key; empty for a local single-file book. */
  key: string;
  /** Served from Bloem storage: direct play only, never downloaded. */
  native: boolean;
  multipart: boolean;
  /** Tracks in play order. */
  tracks: FileVersion[];
  /** Every part is present, numbered 1..N. Only complete presentations are offered. */
  complete: boolean;
  /** Book-global start of each track, in Silo's integer seconds. */
  starts: number[];
  totalSeconds: number;
}

export function isNativePresentationKey(key: string | undefined): boolean {
  return (key ?? "").startsWith(NATIVE_KEY_PREFIX);
}

function trackSeconds(version: FileVersion): number {
  const value = version.duration;
  return Number.isFinite(value) && value > 0 ? value : 0;
}

function isMultipartVersion(version: FileVersion): boolean {
  return (
    MULTIPART_KINDS.has(version.presentation_kind ?? "") &&
    (version.presentation_group_key ?? "") !== ""
  );
}

function presentationIdOf(version: FileVersion): string {
  return isMultipartVersion(version)
    ? `group:${version.presentation_group_key}`
    : `file:${version.file_id}`;
}

function compareTracks(a: FileVersion, b: FileVersion): number {
  const byIndex = (a.presentation_part_index ?? 0) - (b.presentation_part_index ?? 0);
  return byIndex !== 0 ? byIndex : a.file_id - b.file_id;
}

function isCompleteMultipart(tracks: FileVersion[]): boolean {
  const total = tracks[0]?.presentation_part_total;
  return (
    total !== undefined &&
    tracks.length === total &&
    tracks.every(
      (track, index) =>
        track.presentation_part_total === total && track.presentation_part_index === index + 1,
    )
  );
}

/**
 * Groups audiobook versions into presentations, in the order their first
 * version appears. Callers must apply this only to audiobook items; see
 * itemAudiobookPresentations.
 */
export function audiobookPresentations(
  versions: readonly FileVersion[] | undefined,
): AudiobookPresentation[] {
  const groups = new Map<string, FileVersion[]>();
  for (const version of versions ?? []) {
    const id = presentationIdOf(version);
    const group = groups.get(id);
    if (group) group.push(version);
    else groups.set(id, [version]);
  }

  return Array.from(groups, ([id, members]) => {
    const multipart = id.startsWith("group:");
    const tracks = multipart ? [...members].sort(compareTracks) : members;
    const key = tracks[0]?.presentation_group_key ?? "";
    const starts: number[] = [];
    let totalSeconds = 0;
    for (const track of tracks) {
      starts.push(totalSeconds);
      totalSeconds += trackSeconds(track);
    }
    return {
      id,
      key,
      native: isNativePresentationKey(key),
      multipart,
      tracks,
      complete: multipart ? isCompleteMultipart(tracks) : tracks.length === 1,
      starts,
      totalSeconds,
    };
  });
}

/** Presentations of an audiobook item; every other item type has none. */
export function itemAudiobookPresentations(
  item: Pick<ItemDetail, "type" | "versions"> | undefined,
): AudiobookPresentation[] {
  return item?.type === "audiobook" ? audiobookPresentations(item.versions) : [];
}

/**
 * Whether the Bloem adapters own this item: more than one complete
 * presentation, or any native one. Everything else keeps Silo's behaviour.
 */
export function ownsAudiobookItem(presentations: readonly AudiobookPresentation[]): boolean {
  return (
    presentations.some((presentation) => presentation.native) ||
    presentations.filter((presentation) => presentation.complete).length > 1
  );
}

export function isPlayablePresentation(
  presentation: AudiobookPresentation,
  nativeAvailable: boolean,
): boolean {
  return (
    presentation.complete &&
    presentation.tracks.length > 0 &&
    (!presentation.native || nativeAvailable)
  );
}

export interface PresentationChoiceInput {
  /** The presentation this listener last chose for the item on this device. */
  remembered?: string | null;
  /** user_data.last_file_id: a weak hint from older single-file playback. */
  lastFileId?: number;
  /** The server advertises native_audiobook_presentations_v1. */
  nativeAvailable: boolean;
}

/**
 * The presentation to use, in contract order: the remembered choice, the one
 * holding last_file_id, the first local one, then the first native one. Only
 * playable presentations qualify; there is never an automatic switch away
 * from a presentation that fails later.
 */
export function choosePresentation(
  presentations: readonly AudiobookPresentation[],
  { remembered, lastFileId, nativeAvailable }: PresentationChoiceInput,
): AudiobookPresentation | undefined {
  const playable = presentations.filter((presentation) =>
    isPlayablePresentation(presentation, nativeAvailable),
  );
  return (
    (remembered ? playable.find((presentation) => presentation.id === remembered) : undefined) ??
    (lastFileId !== undefined
      ? playable.find((presentation) =>
          presentation.tracks.some((track) => track.file_id === lastFileId),
        )
      : undefined) ??
    playable.find((presentation) => !presentation.native) ??
    playable.find((presentation) => presentation.native)
  );
}

/** The presentation whose tracks are `fileIds`, in order; used to find what is playing. */
export function presentationForFiles(
  presentations: readonly AudiobookPresentation[],
  fileIds: readonly number[],
): AudiobookPresentation | undefined {
  return presentations.find(
    (presentation) =>
      presentation.tracks.length === fileIds.length &&
      presentation.tracks.every((track, index) => track.file_id === fileIds[index]),
  );
}

/**
 * Track-local chapters ready for display: a chapter starting at or past the
 * track's integer duration is dropped, ends are clamped to the next chapter's
 * start and to the track duration (a native chapter may end up to a second
 * past Silo's truncated duration), and a track without chapters gets one.
 */
function displayChapters(
  track: FileVersion,
  trackIndex: number,
  presentation: AudiobookPresentation,
  bookTitle: string,
): VersionChapter[] {
  const duration = trackSeconds(track);
  const sorted = [...(track.chapters ?? [])]
    .filter((chapter) => duration <= 0 || chapter.start_seconds < duration)
    .sort((a, b) => a.start_seconds - b.start_seconds);
  if (sorted.length === 0) {
    return [
      {
        index: 0,
        title: presentation.tracks.length > 1 ? `Part ${trackIndex + 1}` : bookTitle || "Chapter 1",
        start_seconds: 0,
        end_seconds: duration,
        source: "",
      },
    ];
  }
  return sorted.map((chapter, index) => {
    const nextStart = sorted[index + 1]?.start_seconds ?? Number.POSITIVE_INFINITY;
    const limit = duration > 0 ? Math.min(nextStart, duration) : nextStart;
    const end = Math.min(chapter.end_seconds, limit);
    return end === chapter.end_seconds ? chapter : { ...chapter, end_seconds: end };
  });
}

/** The existing player's input for one presentation. Native file names never leave here. */
export function presentationFiles(
  presentation: AudiobookPresentation,
  bookTitle = "",
): AudiobookFile[] {
  return presentation.tracks.map((track, index) => ({
    id: track.file_id,
    path: presentation.native ? "" : (track.file_path ?? track.file_name ?? ""),
    duration_seconds: trackSeconds(track),
    chapters: displayChapters(track, index, presentation, bookTitle),
  }));
}

export interface BookPosition {
  trackIndex: number;
  fileId: number;
  /** Seconds inside the track, the `start_position` of its playback session. */
  localSeconds: number;
}

/** Maps a book-global position to a track and a local position inside it. */
export function locateBookPosition(
  presentation: AudiobookPresentation,
  seconds: number,
): BookPosition | undefined {
  const { tracks, starts } = presentation;
  if (tracks.length === 0) return undefined;
  const position = Number.isFinite(seconds) && seconds > 0 ? seconds : 0;
  let trackIndex = tracks.length - 1;
  for (let index = 0; index < tracks.length; index += 1) {
    const end = starts[index]! + trackSeconds(tracks[index]!);
    if (position < end) {
      trackIndex = index;
      break;
    }
  }
  const track = tracks[trackIndex]!;
  const duration = trackSeconds(track);
  return {
    trackIndex,
    fileId: track.file_id,
    localSeconds: Math.min(Math.max(position - starts[trackIndex]!, 0), duration),
  };
}

/**
 * A stored book position (written under whichever presentation last played)
 * clamped onto this presentation's timeline.
 */
export function clampBookPosition(presentation: AudiobookPresentation, seconds: number): number {
  const position = Number.isFinite(seconds) && seconds > 0 ? seconds : 0;
  return presentation.totalSeconds > 0
    ? Math.min(position, Math.max(0, presentation.totalSeconds - 1))
    : position;
}

/** The server-built playback variant that describes exactly this presentation. */
export function presentationVariant(
  variants: readonly PlaybackVariant[] | undefined,
  presentation: AudiobookPresentation,
): PlaybackVariant | undefined {
  const fileIds = presentation.tracks.map((track) => track.file_id);
  return variants?.find((variant) => {
    const variantIds = variant.parts.flatMap((part) =>
      part.versions.map((version) => version.file_id),
    );
    return (
      variantIds.length === fileIds.length &&
      variantIds.every((fileId, index) => fileId === fileIds[index])
    );
  });
}

/**
 * A shallow, display-ready copy of the item restricted to one presentation:
 * its tracks as `versions`, its own total, and the stored position clamped to
 * it. The cached detail payload is never touched.
 */
export function itemForPresentation(
  item: ItemDetail,
  presentation: AudiobookPresentation | undefined,
): ItemDetail {
  if (!presentation) {
    // Nothing playable (or not decided yet): no tracks and no summed total.
    return {
      ...item,
      versions: [],
      playback_variants: [],
      audiobook: item.audiobook ? { ...item.audiobook, total_duration_seconds: 0 } : item.audiobook,
    };
  }
  const files = presentationFiles(presentation, item.title);
  const versions = presentation.tracks.map((track, index): FileVersion => {
    const chapters = files[index]!.chapters;
    return presentation.native
      ? { ...track, file_name: undefined, file_path: undefined, chapters }
      : { ...track, chapters };
  });
  const variant = presentationVariant(item.playback_variants, presentation);
  const userData =
    item.user_data && "position_seconds" in item.user_data
      ? {
          ...item.user_data,
          position_seconds: clampBookPosition(presentation, item.user_data.position_seconds ?? 0),
          duration_seconds: presentation.totalSeconds,
        }
      : item.user_data;
  return {
    ...item,
    versions,
    playback_variants: variant ? [variant] : [],
    audiobook: item.audiobook
      ? { ...item.audiobook, total_duration_seconds: presentation.totalSeconds }
      : item.audiobook,
    user_data: userData,
  };
}

/**
 * The stored position was written under a presentation of another length, so
 * resuming it in `presentation` is only approximate (user_data.duration_seconds
 * is the writer's presentation total).
 */
export function isApproximateResume(
  item: Pick<ItemDetail, "user_data">,
  presentation: AudiobookPresentation | undefined,
): boolean {
  const userData = item.user_data && "position_seconds" in item.user_data ? item.user_data : null;
  return Boolean(
    presentation &&
    userData &&
    (userData.position_seconds ?? 0) > 0 &&
    (userData.duration_seconds ?? 0) > 0 &&
    userData.duration_seconds !== presentation.totalSeconds,
  );
}
