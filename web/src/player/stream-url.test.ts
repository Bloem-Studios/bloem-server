import { describe, expect, it } from "vitest";
import { buildPlayerStreamUrl } from "./stream-url";

describe("buildPlayerStreamUrl", () => {
  it("preserves only the scoped stream token", () => {
    const url = buildPlayerStreamUrl(
      "https://api.example.com",
      "/api/v1/playback/stream/abc.m3u8?st=streamtoken123",
    );

    const parsed = new URL(url);
    // Both params must survive as separate query keys.
    expect(parsed.searchParams.get("st")).toBe("streamtoken123");
    expect(parsed.searchParams.has("token")).toBe(false);
  });

  it("does not add a general credential when no scoped token is present", () => {
    const url = buildPlayerStreamUrl("https://api.example.com", "/api/v1/playback/stream/abc.m3u8");

    expect(url).toBe("https://api.example.com/api/v1/playback/stream/abc.m3u8");
    const parsed = new URL(url);
    expect(parsed.searchParams.has("token")).toBe(false);
  });

  it("preserves a server-anchored seek param instead of synthesizing one", () => {
    // v3 plans arrive fully anchored: the seek offset is the server's decision
    // and rides in the plan's stream URL. The helper must pass it through
    // untouched and never add one of its own.
    const url = buildPlayerStreamUrl(
      "https://api.example.com",
      "/api/v1/playback/stream/abc.m3u8?st=streamtoken123&seek=12.500",
    );

    const parsed = new URL(url);
    expect(parsed.searchParams.get("st")).toBe("streamtoken123");
    expect(parsed.searchParams.has("token")).toBe(false);
    expect(parsed.searchParams.get("seek")).toBe("12.500");
  });

  it("returns the path unchanged when there is no token", () => {
    const url = buildPlayerStreamUrl(
      "https://api.example.com",
      "/api/v1/playback/proxy/sometoken/abc.m3u8",
    );

    expect(url).toBe("https://api.example.com/api/v1/playback/proxy/sometoken/abc.m3u8");
  });
});

it.each([
  [
    "/api/v1",
    "/api/v2/stream/session?st=opaque%2Bsignature",
    "/api/v2/stream/session?st=opaque%2Bsignature",
  ],
  ["/api/v1", "/api/v1/stream/session?st=opaque", "/api/v2/stream/session?st=opaque"],
  [
    "https://silo.example.test/base/api/v1",
    "/api/v2/playback/transcode/session/master.m3u8?st=opaque",
    "https://silo.example.test/base/api/v2/playback/transcode/session/master.m3u8?st=opaque",
  ],
  ["/api/v1", "/stream/session", "/api/v2/stream/session"],
])("resolves server media paths from configured API base %s", (base, path, expected) => {
  expect(buildPlayerStreamUrl(base, path)).toBe(expected);
});

it.each(["/api/v1", "/api/v2", "https://silo.example.test/base/api/v1"])(
  "projects realtime subtitle paths without changing signed query bytes from %s",
  (base) => {
    const root = base.replace(/\/api\/v[12]$/, "");
    expect(
      buildPlayerStreamUrl(base, "/stream/session/subtitles/4.vtt?st=a%2Bb&file_id=7"),
    ).toBe(`${root}/api/v2/stream/session/subtitles/4.vtt?st=a%2Bb&file_id=7`);
    expect(buildPlayerStreamUrl(base, "/stream/session/subtitles/4/fonts?st=a%2Bb")).toBe(
      `${root}/api/v2/stream/session/subtitles/4/fonts?st=a%2Bb`,
    );
    expect(
      buildPlayerStreamUrl(base, "https://proxy.example.test/stream/opaque?st=a%2Bb"),
    ).toBe("https://proxy.example.test/stream/opaque?st=a%2Bb");
  },
);
