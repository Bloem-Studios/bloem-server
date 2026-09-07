import { describe, expect, it } from "vitest";
import { buildPlayerStreamUrl, proxySubtitleRequest, subtitleFetchOptions } from "./stream-url";

describe("buildPlayerStreamUrl", () => {
  it("joins the access token with `&` when the stream path already has `?st=`", () => {
    const url = buildPlayerStreamUrl(
      "https://api.example.com",
      "/api/v1/playback/stream/abc.m3u8?st=streamtoken123",
      "jwt-access-token",
    );

    const parsed = new URL(url);
    // Both params must survive as separate query keys.
    expect(parsed.searchParams.get("st")).toBe("streamtoken123");
    expect(parsed.searchParams.get("token")).toBe("jwt-access-token");
  });

  it("uses `?` when the stream path has no existing query string", () => {
    const url = buildPlayerStreamUrl(
      "https://api.example.com",
      "/api/v1/playback/stream/abc.m3u8",
      "jwt-access-token",
    );

    expect(url).toBe(
      "https://api.example.com/api/v1/playback/stream/abc.m3u8?token=jwt-access-token",
    );
    const parsed = new URL(url);
    expect(parsed.searchParams.get("token")).toBe("jwt-access-token");
  });

  it("preserves a server-anchored seek param instead of synthesizing one", () => {
    // v3 plans arrive fully anchored: the seek offset is the server's decision
    // and rides in the plan's stream URL. The helper must pass it through
    // untouched and never add one of its own.
    const url = buildPlayerStreamUrl(
      "https://api.example.com",
      "/api/v1/playback/stream/abc.m3u8?st=streamtoken123&seek=12.500",
      "jwt-access-token",
    );

    const parsed = new URL(url);
    expect(parsed.searchParams.get("st")).toBe("streamtoken123");
    expect(parsed.searchParams.get("token")).toBe("jwt-access-token");
    expect(parsed.searchParams.get("seek")).toBe("12.500");
  });

  it("returns the path unchanged when there is no token", () => {
    const url = buildPlayerStreamUrl(
      "https://api.example.com",
      "/api/v1/playback/proxy/sometoken/abc.m3u8",
      null,
    );

    expect(url).toBe("https://api.example.com/api/v1/playback/proxy/sometoken/abc.m3u8");
  });
});

it.each([
  [
    "/api/v1",
    "/api/v2/stream/session?st=opaque%2Bsignature",
    "/api/v2/stream/session?st=opaque%2Bsignature&token=access",
  ],
  ["/api/v1", "/api/v1/stream/session?st=opaque", "/api/v1/stream/session?st=opaque&token=access"],
  [
    "https://silo.example.test/base/api/v1",
    "/api/v2/playback/transcode/session/master.m3u8?st=opaque",
    "https://silo.example.test/base/api/v2/playback/transcode/session/master.m3u8?st=opaque&token=access",
  ],
  ["/api/v1", "/stream/session", "/api/v1/stream/session?token=access"],
])("resolves server media paths from configured API base %s", (base, path, expected) => {
  expect(buildPlayerStreamUrl(base, path, "access")).toBe(expected);
});

describe("captured proxy subtitle requests", () => {
  const sid = "11111111-1111-4111-8111-111111111111";
  const stream = { url: `https://proxy.example.test/stream/v3/${sid}` };
  const headers = { Authorization: "Bearer captured", "X-Profile-Id": "captured-profile" };
  const path = `${stream.url}/subtitles/4.ass`;
  const query = "?file_id=5&embedded_stream_index=2";
  const resolve = (url: string, supplied = headers, fonts = false) =>
    proxySubtitleRequest(
      url,
      stream,
      supplied,
      sid,
      4,
      [5, 6],
      "https://api.example.test/api/v1",
      fonts,
    );

  it("preserves immutable bytes and snapshots only captured credentials", () => {
    const supplied = { ...headers, Cookie: "private", Extra: "ignored" };
    const request = resolve(path + query, supplied)!;
    supplied.Authorization = "Bearer replacement";
    expect(request.url).toBe(path + query);
    expect(request.headers).toEqual({
      authorization: "Bearer captured",
      "x-profile-id": "captured-profile",
    });
    expect(subtitleFetchOptions(request.headers)).toMatchObject({
      redirect: "error",
      credentials: "omit",
    });
  });

  it.each([
    "downloaded_subtitle_id=7",
    `external_subtitle_key=${"a".repeat(64)}`,
    "embedded_stream_index=0",
  ])("retains supported font identity %s", (pin) => {
    const url = `${path}/fonts?${pin}&file_id=5`;
    expect(resolve(url, headers, true)?.url).toBe(url);
  });

  it.each([
    `${path}${query}&embedded_stream_index=3`,
    `${path}${query}&downloaded_subtitle_id=8`,
    `${path}${query}&token=credential`,
    `${path}${query}&st=opaque`,
    `${path}?file_id=99&embedded_stream_index=2`,
    `${path}?file_id=5`,
    `${path}${query}#fragment`,
    `${path.replace("proxy.example.test", "foreign.example.test")}${query}`,
    `${path.replace(sid, "22222222-2222-4222-8222-222222222222")}${query}`,
    `${path.replace("4.ass", "5.ass")}${query}`,
    `${path.replace("/subtitles/", "/segment/../subtitles/")}${query}`,
    `${path.replace("/subtitles/", "/%73ubtitles/")}${query}`,
    `${path.replace("https://", "https://user:secret@")}${query}`,
  ])("refuses credential release for %s", (url) => expect(resolve(url)).toBeNull());

  it("refuses missing captured authority and stale requests", () => {
    expect(resolve(path + query, { Authorization: "", "X-Profile-Id": "" })).toBeNull();
    expect(() => subtitleFetchOptions(headers, undefined, () => false)).toThrow(
      "Playback identity changed",
    );
  });
});

it.each([
  "/stream/direct/opaque.signed.token",
  "/stream/transcode/opaque.signed.token/master.m3u8",
])("scopes auxiliary headers to existing signed main route %s", (mainPath) => {
  const sid = "44444444-4444-4444-8444-444444444444";
  const raw = `https://proxy.example.test/stream/v3/${sid}/subtitles/0.vtt?file_id=1&downloaded_subtitle_id=9`;
  const request = proxySubtitleRequest(
    raw,
    { url: `https://proxy.example.test${mainPath}` },
    { authorization: "Bearer captured", "x-profile-id": "profile" },
    sid,
    0,
    [1],
    "https://api.example.test",
  );
  expect(request?.url).toBe(raw);
  expect(request?.headers?.authorization).toBe("Bearer captured");
});
