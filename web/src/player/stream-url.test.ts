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
