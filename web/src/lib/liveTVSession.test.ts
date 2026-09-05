import { afterEach, describe, expect, it, vi } from "vitest";
import { startLiveTVPlayback } from "./liveTVSession";
import {
  setAccessToken,
  setProfileId,
  setProfileToken,
  captureProfileRequestContext,
} from "@/api/client";

function identity() {
  setAccessToken("viewer-token");
  setProfileId("viewer-a");
  setProfileToken("pin-a");
  return captureProfileRequestContext()!;
}
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
describe("Live TV tuner lifecycle", () => {
  it("releases a late tune response after the viewer leaves without publishing playback", async () => {
    const authority = identity();
    let resolveTune!: (response: Response) => void;
    const calls: Array<{ url: string; init?: RequestInit }> = [];
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>((input, init) => {
        calls.push({ url: String(input), init });
        if (init?.method === "POST")
          return new Promise((resolve) => {
            resolveTune = resolve;
          });
        return Promise.resolve(new Response(null, { status: 204 }));
      }),
    );
    const ready = vi.fn();
    const stop = startLiveTVPlayback(
      "ch/1",
      authority,
      { codecs_video: ["h264"], codecs_audio: ["aac"] },
      ready,
      vi.fn(),
    );
    stop();
    setProfileId("viewer-b");
    resolveTune(
      Response.json({ session_id: "session-1", hls_url: "/live.m3u8", transport: "hls" }),
    );
    await vi.waitFor(() => expect(calls.some((call) => call.init?.method === "DELETE")).toBe(true));
    expect(ready).not.toHaveBeenCalled();
    const release = calls.find((call) => call.init?.method === "DELETE")!;
    expect(release.url).toBe("/api/v1/livetv/sessions/session-1");
    expect((release.init?.headers as Record<string, string>)["X-Profile-Id"]).toBe("viewer-a");
  });
  it("does not retry a failed tune and risk claiming another tuner", async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => new Response(null, { status: 503 }));
    vi.stubGlobal("fetch", fetchMock);
    const failed = vi.fn();
    startLiveTVPlayback(
      "ch-1",
      identity(),
      { codecs_video: [], codecs_audio: [] },
      vi.fn(),
      failed,
    );
    await vi.waitFor(() => expect(failed).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
  it("stops and releases when heartbeat access is revoked", async () => {
    vi.useFakeTimers();
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>(async (input, init) => {
        const url = String(input);
        calls.push(`${init?.method} ${url}`);
        if (url.endsWith("/heartbeat"))
          return Response.json(
            { error: "live_tv_forbidden", message: "Access revoked" },
            { status: 403 },
          );
        if (init?.method === "DELETE") return new Response(null, { status: 204 });
        return Response.json({ session_id: "session-1", hls_url: "/live.m3u8", transport: "hls" });
      }),
    );
    const failed = vi.fn();
    const stop = startLiveTVPlayback(
      "ch-1",
      identity(),
      { codecs_video: [], codecs_audio: [] },
      vi.fn(),
      failed,
    );
    await vi.advanceTimersByTimeAsync(30000);
    expect(failed).toHaveBeenCalled();
    expect(calls).toContain("DELETE /api/v1/livetv/sessions/session-1");
    const count = calls.length;
    await vi.advanceTimersByTimeAsync(60000);
    expect(calls).toHaveLength(count);
    stop();
  });
});

it("releases on page teardown and leaves a reconnect state for browser back", async () => {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn<typeof fetch>(async (input, init) => {
      calls.push(`${init?.method} ${input}`);
      return init?.method === "DELETE"
        ? new Response(null, { status: 204 })
        : Response.json({ session_id: "lease-1", hls_url: "/live.m3u8" });
    }),
  );
  const ready = vi.fn();
  const failed = vi.fn();
  startLiveTVPlayback("ch-1", identity(), { codecs_video: [], codecs_audio: [] }, ready, failed);
  await vi.waitFor(() => expect(ready).toHaveBeenCalled());
  window.dispatchEvent(new Event("pagehide"));
  expect(calls).toContain("DELETE /api/v1/livetv/sessions/lease-1");
  expect(failed).toHaveBeenCalled();
});
