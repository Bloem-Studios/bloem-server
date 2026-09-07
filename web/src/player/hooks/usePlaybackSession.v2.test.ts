import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { createElement, createRef, type ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { PlayerConfigProvider, type PlayerConfig } from "../context/PlayerConfigContext";
import { fixturePlanV3 } from "../protocol-v3.fixtures";
import { usePlaybackSession } from "./usePlaybackSession";
import { useWatchProgress } from "./useWatchProgress";
import { resetCodecDetectionForTests } from "./useCodecDetection";

const config: PlayerConfig = {
  apiBaseUrl: "/api/v1",
  getAccessToken: () => "token",
  getProfileId: () => "profile",
  getDeviceId: () => "device",
  capturePlaybackMutationContext: () => ({
    accountId: "account",
    profileId: "profile",
    origin: "http://localhost:3000",
    isCurrent: () => true,
  }),
};
const wrapper = ({ children }: { children: ReactNode }) =>
  createElement(PlayerConfigProvider, { config, children });
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const cap = {
  installation_id: "installation",
  state: "available",
  allowed: true,
  protocol_versions: [3],
  features: ["sequenced_progress_v1"],
};
beforeEach(() => {
  localStorage.clear();
  const tails = new Map<string, Promise<unknown>>();
  Object.defineProperty(navigator, "locks", {
    configurable: true,
    value: {
      request: (key: string, _options: unknown, action: () => Promise<unknown>) => {
        const next = (tails.get(key) ?? Promise.resolve()).catch(() => {}).then(action);
        tails.set(key, next);
        return next;
      },
    },
  });
});
afterEach(() => {
  cleanup();
  resetCodecDetectionForTests();
  vi.unstubAllGlobals();
});
it.each(["not_configured", "missing"])("surfaces %s without any legacy start", async (state) => {
  const fetcher = vi.fn(async (_input: RequestInfo | URL) =>
    json({ state, protocol_versions: [], features: [] }, state === "missing" ? 404 : 200),
  );
  vi.stubGlobal("fetch", fetcher);
  const { result } = renderHook(() => usePlaybackSession("request", [], [], 42, 0, false, "auto"), {
    wrapper,
  });
  await waitFor(() => expect(result.current.loading).toBe(false));
  expect(result.current.sessionId).toBeNull();
  expect(result.current.error).toContain("API v2 playback");
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher.mock.calls[0]![0]).toBe("/api/v2/playback/capabilities");
});
it("uses real durable helpers for video start, replan, telemetry, progress and unmount stop", async () => {
  const plan = fixturePlanV3({ session_id: "integration-video" });
  const wire = {
    ...plan,
    requested_media_file_id: "42",
    effective_media_file_id: "42",
    source: { ...plan.source, media_file_id: "42" },
  };
  const fetcher = vi.fn(async (input: RequestInfo | URL, options?: RequestInit) => {
    const url = String(input);
    if (url.endsWith("/capabilities")) return json(cap);
    if (url.endsWith("/start") || url.endsWith("/replan"))
      return json({
        protocol_version: 3,
        server_features: ["sequenced_progress_v1"],
        session_id: "integration-video",
        playback_plan: wire,
      });
    if (options?.method === "DELETE")
      return json({ outcome: "stopped", stop_id: JSON.parse(String(options.body)).stop_id });
    if (url.endsWith("/progress")) return json({ outcome: "applied" });
    if (url.endsWith("/route-events")) return new Response(null, { status: 202 });
    throw new Error(`Unexpected request ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
  const videoRef = createRef<HTMLVideoElement>();
  videoRef.current = document.createElement("video");
  videoRef.current.currentTime = 37;
  const { result, unmount } = renderHook(
    () => {
      const session = usePlaybackSession("request", [], [], 42, 0, false, "auto");
      const flush = useWatchProgress(session.sessionId, videoRef);
      return { session, flush };
    },
    { wrapper },
  );
  await waitFor(() => expect(result.current.session.sessionId).toBe("integration-video"));
  await act(async () => {
    await result.current.flush();
  });
  await act(async () => {
    await result.current.session.changeQuality("720p", 37);
  });
  unmount();
  await waitFor(() =>
    expect(fetcher.mock.calls.some(([, options]) => options?.method === "DELETE")).toBe(true),
  );
  const paths = fetcher.mock.calls.map(([url]) => String(url));
  expect(paths.every((url) => url.includes("/api/v2/"))).toBe(true);
  for (const suffix of ["/start", "/replan", "/route-events", "/progress"])
    expect(paths.some((url) => url.endsWith(suffix))).toBe(true);
  const stops = fetcher.mock.calls.filter(([, options]) => options?.method === "DELETE");
  expect(stops[0]![1]?.keepalive).toBe(true);
  expect(JSON.parse(String(stops[0]![1]?.body))).toMatchObject({
    installation_id: "installation",
    position: 37,
    sequence: 2,
  });
});
it("does not fabricate telemetry authority for a terminal start", async () => {
  const fetcher = vi.fn(async (input: RequestInfo | URL) =>
    String(input).endsWith("/capabilities")
      ? json(cap)
      : json({
          protocol_version: 3,
          server_features: [],
          terminal: { reason: "unsupported_codec" },
        }),
  );
  vi.stubGlobal("fetch", fetcher);
  const { result } = renderHook(
    () => usePlaybackSession("terminal", [], [], 42, 0, false, "auto"),
    { wrapper },
  );
  await waitFor(() => expect(result.current.loading).toBe(false));
  expect(result.current.sessionId).toBeNull();
  expect(fetcher).toHaveBeenCalledTimes(2);
});
