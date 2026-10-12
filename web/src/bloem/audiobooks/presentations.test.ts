import { describe, expect, it } from "vitest";
import type { ItemDetail } from "@/api/types";
import { buildChapterList, buildPlayerChapters } from "@/lib/audiobooks/chapters";
import { F1_LOCAL_ID, F1_NATIVE_ID, F1_WIRE, F2_WIRE, F4_WIRE, fixtureItem } from "./fixtures";
import {
  audiobookPresentations,
  choosePresentation,
  clampBookPosition,
  isNativePresentationKey,
  itemAudiobookPresentations,
  itemForPresentation,
  locateBookPosition,
  ownsAudiobookItem,
  presentationFiles,
  presentationVariant,
  type AudiobookPresentation,
} from "./presentations";

function byId(presentations: AudiobookPresentation[], id: string): AudiobookPresentation {
  const presentation = presentations.find((entry) => entry.id === id);
  if (!presentation) throw new Error(`missing presentation ${id}`);
  return presentation;
}

function chapterStarts(presentation: AudiobookPresentation, title: string) {
  return buildChapterList(presentationFiles(presentation, title)).map((entry) => [
    entry.label,
    entry.absoluteStart,
  ]);
}

describe("F1: local multipart and native multipart", () => {
  const item = fixtureItem(F1_WIRE);
  const presentations = itemAudiobookPresentations(item);
  const local = byId(presentations, F1_LOCAL_ID);
  const native = byId(presentations, F1_NATIVE_ID);

  it("groups the versions into two complete presentations", () => {
    expect(presentations.map((entry) => entry.id)).toEqual([F1_LOCAL_ID, F1_NATIVE_ID]);
    expect(local).toMatchObject({ native: false, multipart: true, complete: true });
    expect(native).toMatchObject({ native: true, multipart: true, complete: true });
    expect(local.tracks.map((track) => track.file_id)).toEqual([501, 502]);
    expect(native.tracks.map((track) => track.file_id)).toEqual([601, 602]);
    expect(ownsAudiobookItem(presentations)).toBe(true);
  });

  it("builds each timeline from the integer track durations", () => {
    expect(local.starts).toEqual([0, 90]);
    expect(local.totalSeconds).toBe(210);
    expect(native.starts).toEqual([0, 70]);
    expect(native.totalSeconds).toBe(150);
  });

  it("places track-local chapters on the book timeline", () => {
    expect(chapterStarts(local, item.title)).toEqual([
      ["01 - Opening", 0],
      ["02 - Middle", 90],
    ]);
    expect(chapterStarts(native, item.title)).toEqual([
      ["Chapter 01", 0],
      ["Chapter 02", 40.25],
      ["Chapter 03", 70],
      ["Chapter 04", 75],
    ]);
  });

  it("clamps a native chapter that ends past its track's integer duration", () => {
    const chapters = buildPlayerChapters(presentationFiles(native, item.title));
    expect(chapters.map((chapter) => [chapter.start_seconds, chapter.end_seconds])).toEqual([
      [0, 40.25],
      [40.25, 70],
      [70, 75],
      [75, 150],
    ]);
  });

  it("chooses the presentation holding last_file_id before the first local one", () => {
    expect(choosePresentation(presentations, { lastFileId: 602, nativeAvailable: true })?.id).toBe(
      F1_NATIVE_ID,
    );
    expect(choosePresentation(presentations, { nativeAvailable: true })?.id).toBe(F1_LOCAL_ID);
  });

  it("prefers the remembered choice and never offers native without the token", () => {
    expect(
      choosePresentation(presentations, {
        remembered: F1_LOCAL_ID,
        lastFileId: 602,
        nativeAvailable: true,
      })?.id,
    ).toBe(F1_LOCAL_ID);
    expect(
      choosePresentation(presentations, {
        remembered: F1_NATIVE_ID,
        lastFileId: 602,
        nativeAvailable: false,
      })?.id,
    ).toBe(F1_LOCAL_ID);
  });

  it("resumes book position 100 in the right track of each presentation", () => {
    expect(locateBookPosition(native, 100)).toEqual({
      trackIndex: 1,
      fileId: 602,
      localSeconds: 30,
    });
    expect(locateBookPosition(local, 100)).toEqual({
      trackIndex: 1,
      fileId: 502,
      localSeconds: 10,
    });
    expect(locateBookPosition(native, 500)).toEqual({
      trackIndex: 1,
      fileId: 602,
      localSeconds: 80,
    });
    expect(locateBookPosition(native, -5)).toEqual({ trackIndex: 0, fileId: 601, localSeconds: 0 });
  });

  it("agrees with the server-built playback variants", () => {
    expect(presentationVariant(item.playback_variants, local)).toMatchObject({
      variant_id: "|multipart|7301928374650001",
      total_duration: 210,
    });
    expect(presentationVariant(item.playback_variants, native)).toMatchObject({
      variant_id: `|multipart|${native.key}`,
      total_duration: 150,
    });
  });

  it("restricts a copy of the item to the chosen presentation, never 360 seconds", () => {
    const view = itemForPresentation(item, native);
    expect(view.versions.map((version) => version.file_id)).toEqual([601, 602]);
    expect(view.audiobook?.total_duration_seconds).toBe(150);
    expect(view.playback_variants?.map((variant) => variant.variant_id)).toEqual([
      `|multipart|${native.key}`,
    ]);
    expect(itemForPresentation(item, local).audiobook?.total_duration_seconds).toBe(210);
    // The frozen cached payload is untouched.
    expect(item.versions).toHaveLength(4);
    expect(item.audiobook?.total_duration_seconds).toBe(360);
  });

  it("never exposes a native file name", () => {
    const view = itemForPresentation(item, native);
    expect(view.versions.every((version) => version.file_name === undefined)).toBe(true);
    expect(presentationFiles(native).every((file) => file.path === "")).toBe(true);
    expect(presentationFiles(local).map((file) => file.path)).toEqual([
      "01 - Opening.mp3",
      "02 - Middle.mp3",
    ]);
  });
});

describe("F2: single-track local and single-track native", () => {
  const item = fixtureItem(F2_WIRE);
  const presentations = itemAudiobookPresentations(item);

  it("treats the keyed single native track as its own presentation", () => {
    expect(presentations.map((entry) => [entry.id, entry.native, entry.totalSeconds])).toEqual([
      ["file:701", false, 2],
      ["file:702", true, 2],
    ]);
    expect(isNativePresentationKey(presentations[1]!.key)).toBe(true);
    expect(choosePresentation(presentations, { nativeAvailable: true })?.id).toBe("file:701");
  });

  it("keeps a native chapter that ends past the track and displays it ending at 2", () => {
    const native = byId(presentations, "file:702");
    const [chapters] = presentationFiles(native, item.title).map((file) => file.chapters);
    expect(
      chapters?.map((chapter) => [chapter.title, chapter.start_seconds, chapter.end_seconds]),
    ).toEqual([
      ["Chapter 01", 0, 1.25],
      ["Chapter 02", 1.25, 2],
    ]);
    expect(itemForPresentation(item, native).audiobook?.total_duration_seconds).toBe(2);
  });

  it("agrees with the default and single-track native variants", () => {
    expect(presentationVariant(item.playback_variants, presentations[0]!)?.variant_id).toBe(
      "default",
    );
    expect(presentationVariant(item.playback_variants, presentations[1]!)?.variant_id).toBe(
      `||${presentations[1]!.key}`,
    );
  });
});

describe("F4: movie alternatives", () => {
  it("never become an audiobook presentation", () => {
    const item = fixtureItem(F4_WIRE);
    expect(itemAudiobookPresentations(item)).toEqual([]);
    expect(ownsAudiobookItem(itemAudiobookPresentations(item))).toBe(false);
  });
});

describe("presentation rules", () => {
  const base = fixtureItem(F1_WIRE).versions;

  it("accepts the legacy audiobook_part marker", () => {
    const legacy = base.map((version) =>
      version.file_id >= 600 ? { ...version, presentation_kind: "audiobook_part" } : version,
    );
    const native = audiobookPresentations(legacy).find((entry) => entry.native);
    expect(native).toMatchObject({ multipart: true, complete: true, totalSeconds: 150 });
  });

  it("sorts tracks by part index and marks a presentation with a missing part incomplete", () => {
    const reversed = audiobookPresentations([...base].reverse());
    expect(reversed.map((entry) => entry.tracks.map((track) => track.file_id))).toEqual([
      [601, 602],
      [501, 502],
    ]);
    const missing = audiobookPresentations(base.filter((version) => version.file_id !== 602));
    const native = missing.find((entry) => entry.native)!;
    expect(native.complete).toBe(false);
    expect(choosePresentation(missing, { lastFileId: 601, nativeAvailable: true })?.id).toBe(
      F1_LOCAL_ID,
    );
  });

  it("does not own an ordinary local audiobook", () => {
    expect(ownsAudiobookItem(audiobookPresentations(base.slice(0, 2)))).toBe(false);
  });

  it("gives a track without chapters one chapter", () => {
    const bare = audiobookPresentations(
      base.slice(2).map((version) => ({ ...version, chapters: [] })),
    )[0]!;
    expect(chapterStarts(bare, "Book")).toEqual([
      ["Part 1", 0],
      ["Part 2", 70],
    ]);
    const single = audiobookPresentations([
      { ...base[0]!, presentation_kind: "", chapters: [] },
    ])[0]!;
    expect(chapterStarts(single, "Book")).toEqual([["Book", 0]]);
  });

  it("clamps a stored position written under a longer presentation", () => {
    const native = audiobookPresentations(base).find((entry) => entry.native)!;
    expect(clampBookPosition(native, 200)).toBe(149);
    const item = { ...fixtureItem(F1_WIRE), user_data: { played: false, position_seconds: 200 } };
    expect(
      (itemForPresentation(item as ItemDetail, native).user_data as { position_seconds: number })
        .position_seconds,
    ).toBe(149);
  });
});
