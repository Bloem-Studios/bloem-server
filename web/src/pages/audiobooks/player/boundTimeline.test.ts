import { expect, it } from "vitest";
import {
  readBoundAcceptedProgress,
  readProgressTimeline,
  readTimelineManifest,
  selectTimelinePart,
  validateSelectedTimeline,
} from "@/player/bound-client-timeline";
const wire = {
  timeline_id: "a".repeat(64),
  media_item_id: "book",
  file_id: "2",
  part_offset_seconds: 600,
  part_duration_seconds: 300,
  duration_seconds: 900,
};
it("captures only the server mapping and validates the selected part", () => {
  const timeline = readProgressTimeline(wire, "book", "2");
  expect(Object.isFrozen(timeline)).toBe(true);
  expect(timeline).not.toBe(wire);
  expect(() => readProgressTimeline(wire, "other-book", "2")).toThrow();
  expect(() => readProgressTimeline(wire, "book", "1")).toThrow();
});
it.each([
  null,
  { ...wire, timeline_id: "A".repeat(64) },
  { ...wire, part_offset_seconds: -1 },
  { ...wire, part_duration_seconds: 0 },
  { ...wire, part_duration_seconds: NaN },
  { ...wire, duration_seconds: Infinity },
  { ...wire, duration_seconds: 899 },
])("refuses an invalid or unavailable mapping %#", (value) => {
  expect(() => readProgressTimeline(value, "book", "2")).toThrow();
});
it("keeps local and global clocks distinct, including accepted zero", () => {
  const timeline = readProgressTimeline(wire, "book", "2");
  expect(
    readBoundAcceptedProgress(
      {
        sequence: 2,
        position: 30,
        item_position: 630,
        is_paused: false,
        timeline_id: wire.timeline_id,
      },
      timeline,
    ),
  ).toMatchObject({ position: 30, item_position: 630 });
  const first = readProgressTimeline({ ...wire, part_offset_seconds: 0 }, "book", "2");
  expect(
    readBoundAcceptedProgress(
      {
        sequence: 0,
        position: 0,
        item_position: 0,
        is_paused: true,
        timeline_id: wire.timeline_id,
      },
      first,
    ).item_position,
  ).toBe(0);
});
it.each([
  { timeline_id: "b".repeat(64) },
  { position: 301, item_position: 901 },
  { position: 30, item_position: 30 },
  { item_position: undefined },
  { sequence: Number.MAX_SAFE_INTEGER + 1 },
  { position: NaN },
])("refuses an unrelated or inconsistent accepted receipt %#", (change) => {
  const timeline = readProgressTimeline(wire, "book", "2");
  expect(() =>
    readBoundAcceptedProgress(
      {
        sequence: 3,
        position: 30,
        item_position: 630,
        is_paused: false,
        timeline_id: wire.timeline_id,
        ...change,
      },
      timeline,
    ),
  ).toThrow();
});

const manifestWire = {
  installation_id: "installation",
  timeline_id: "a".repeat(64),
  media_item_id: "book",
  edition_id: "edition",
  duration_seconds: 900,
  parts: [
    { file_id: "2", offset_seconds: 0, duration_seconds: 600 },
    { file_id: "1", offset_seconds: 600, duration_seconds: 300 },
  ],
};
it("retains the complete server order and selects local resume at zero, boundaries and book end", () => {
  const manifest = readTimelineManifest(manifestWire, "installation", "book", "1");
  expect(selectTimelinePart(manifest, 0)).toMatchObject({
    index: 0,
    part: { file_id: "2" },
    localPosition: 0,
  });
  expect(selectTimelinePart(manifest, 630)).toMatchObject({
    index: 1,
    part: { file_id: "1" },
    localPosition: 30,
  });
  expect(selectTimelinePart(manifest, 600).localPosition).toBe(0);
  expect(selectTimelinePart(manifest, 900).localPosition).toBe(300);
  expect(Object.isFrozen(manifest.parts[0])).toBe(true);
  expect(Object.isFrozen(manifest.parts)).toBe(true);
  expect(() => selectTimelinePart(manifest, 901)).toThrow();
  expect(() => selectTimelinePart(manifest, NaN)).toThrow();
});
it.each([
  { installation_id: "other" },
  { media_item_id: "other" },
  { edition_id: "" },
  { duration_seconds: 901 },
  { parts: [] },
  { parts: [manifestWire.parts[1], manifestWire.parts[0]] },
  { parts: [manifestWire.parts[0], { ...manifestWire.parts[1], file_id: "2" }] },
  { parts: [manifestWire.parts[0], { ...manifestWire.parts[1], duration_seconds: Infinity }] },
  { parts: [manifestWire.parts[0], { ...manifestWire.parts[1], offset_seconds: 601 }] },
])("refuses malformed or unrelated complete manifests %#", (change) => {
  expect(() =>
    readTimelineManifest({ ...manifestWire, ...change }, "installation", "book", "1"),
  ).toThrow();
});
it("refuses missing anchors and changed decision pins without changing the retained selection", () => {
  expect(() => readTimelineManifest(manifestWire, "installation", "book", "3")).toThrow();
  const manifest = readTimelineManifest(manifestWire, "installation", "book", "1");
  const selected = { ...wire, file_id: "1" };
  expect(validateSelectedTimeline(selected, manifest, "1").timeline_id).toBe(manifest.timeline_id);
  expect(() =>
    validateSelectedTimeline({ ...selected, timeline_id: "b".repeat(64) }, manifest, "1"),
  ).toThrow();
  expect(() =>
    validateSelectedTimeline({ ...selected, part_offset_seconds: 500 }, manifest, "1"),
  ).toThrow();
  expect(selectTimelinePart(manifest, 630).localPosition).toBe(30);
});
