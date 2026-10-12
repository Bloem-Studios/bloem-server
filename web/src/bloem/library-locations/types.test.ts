import { expect, it } from "vitest";
import { supportsSource, type LocationCapabilities, type StorageSource } from "./types";
const source = { plugin_id: "bloem.storage.s3", enabled: true, configured: true } as StorageSource;
const caps = {
  supported_source_media: [{ source_kind: "s3", media_kinds: ["movies", "ebooks", "audiobooks"] }],
  native_audio_libraries: false,
  native_audio_playback: false,
} as LocationCapabilities;
it("offers advertised movie and ebook sources, including singular aliases", () => {
  expect(supportsSource(caps, source, "movies")).toBe(true);
  expect(supportsSource(caps, source, "ebook")).toBe(true);
  expect(supportsSource(caps, source, "series")).toBe(false);
});
it("offers audio only after both actual readiness flags are enabled", () => {
  expect(supportsSource(caps, source, "audiobooks")).toBe(false);
  expect(supportsSource({ ...caps, native_audio_libraries: true }, source, "audiobooks")).toBe(
    false,
  );
  expect(
    supportsSource(
      { ...caps, native_audio_libraries: true, native_audio_playback: true },
      source,
      "audiobooks",
    ),
  ).toBe(true);
});
it("does not assume an unknown plugin kind or offer disabled sources", () => {
  expect(supportsSource(caps, { ...source, plugin_id: "other" }, "movies")).toBe(false);
  expect(supportsSource(caps, { ...source, enabled: false }, "movies")).toBe(false);
});
