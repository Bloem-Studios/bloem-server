import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { createElement, createRef, type ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { PlayerConfigProvider, type PlayerConfig } from "../context/PlayerConfigContext";
import { fixturePlanV3, fixtureSubtitleInventoryItemV3 } from "../protocol-v3.fixtures";
import { usePlaybackSession } from "./usePlaybackSession";
import { useWatchProgress } from "./useWatchProgress";
import { resetCodecDetectionForTests } from "./useCodecDetection";

let authorityCurrent = true;
const config: PlayerConfig = {
  apiBaseUrl: "/api/v1",
  getAccessToken: () => "token",
  getProfileId: () => "profile",
  getDeviceId: () => "device",
  capturePlaybackMutationContext: () => ({
    accountId: "account",
    profileId: "profile",
    origin: "http://localhost:3000",
    isCurrent: () => authorityCurrent,
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
  authorityCurrent = true;
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

it.each(["same-account", "changed-account", "lost-reply"])(
  "preserves start authority after unmount: %s",
  async (outcome) => {
    let settle!: (reply: Response) => void;
    let reject!: (error: Error) => void;
    const delayed = new Promise<Response>((resolve, fail) => {
      settle = resolve;
      reject = fail;
    });
    const sessionId = `late-${outcome}`;
    const plan = fixturePlanV3({ session_id: sessionId });
    const fetcher = vi.fn(async (input: RequestInfo | URL, options?: RequestInit) => {
      if (String(input).endsWith("/capabilities")) return json(cap);
      if (String(input).endsWith("/start")) return delayed;
      if (options?.method === "DELETE")
        return json({ outcome: "stopped", stop_id: JSON.parse(String(options.body)).stop_id });
      throw new Error("Unexpected post-unmount request");
    });
    vi.stubGlobal("fetch", fetcher);
    const { unmount } = renderHook(
      () => usePlaybackSession(sessionId, [], [], 42, 0, false, "auto"),
      { wrapper },
    );
    await waitFor(() =>
      expect(fetcher.mock.calls.some(([url]) => String(url).endsWith("/start"))).toBe(true),
    );
    const originalBody = fetcher.mock.calls.find(([url]) => String(url).endsWith("/start"))![1]!
      .body;
    const startKey = Object.keys(localStorage).find((key) =>
      key.startsWith("silo-playback-start-v1:"),
    )!;
    expect(localStorage.getItem(startKey)).toBe(originalBody);
    unmount();
    if (outcome === "changed-account") authorityCurrent = false;
    await act(async () => {
      if (outcome === "lost-reply") reject(new TypeError("lost reply"));
      else
        settle(
          json({
            protocol_version: 3,
            session_id: sessionId,
            playback_plan: {
              ...plan,
              requested_media_file_id: "42",
              effective_media_file_id: "42",
              source: { ...plan.source, media_file_id: "42" },
            },
          }),
        );
    });
    if (outcome === "same-account") {
      await waitFor(() =>
        expect(fetcher.mock.calls.filter(([, init]) => init?.method === "DELETE")).toHaveLength(1),
      );
      const [url, init] = fetcher.mock.calls.find(([, init]) => init?.method === "DELETE")!;
      expect(url).toBe(`http://localhost:3000/api/v2/playback/${sessionId}`);
      expect(JSON.parse(String(init?.body))).toMatchObject({
        installation_id: "installation",
        stop_id: expect.any(String),
      });
      expect(localStorage.getItem(startKey)).toBeNull();
    } else {
      expect(fetcher.mock.calls.filter(([, init]) => init?.method === "DELETE")).toHaveLength(0);
      expect(localStorage.getItem(startKey)).toBe(originalBody);
    }
    expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/start"))).toHaveLength(1);
    expect(fetcher.mock.calls.every(([url]) => !String(url).endsWith("/route-events"))).toBe(true);
  },
);

it("joins proxy auxiliaries to captured durable authority without persisting credentials", async () => {
  const sid = "33333333-3333-4333-8333-333333333333";
  const url = `https://proxy.example.test/stream/v3/${sid}/subtitles/0.ass?file_id=42&embedded_stream_index=0`;
  const fontUrl = `https://proxy.example.test/stream/v3/${sid}/subtitles/0/fonts?file_id=42&embedded_stream_index=0`;
  let ambientToken = "captured-only";
  const scopedConfig = {
    ...config,
    capturePlaybackMutationContext: () => {
      const capturedHeaders = Object.freeze({
        Authorization: `Bearer ${ambientToken}`,
        "X-Profile-Id": "profile",
      });
      return {
        ...config.capturePlaybackMutationContext!()!,
        mediaRequestHeaders: () => (authorityCurrent ? capturedHeaders : null),
      };
    },
  };
  const plan = fixturePlanV3({ session_id: sid });
  const wire = {
    ...plan,
    requested_media_file_id: "42",
    effective_media_file_id: "42",
    source: { ...plan.source, media_file_id: "42" },
    stream: {
      ...plan.stream,
      url: "https://proxy.example.test/stream/direct/opaque.signed.token",
      headers: {},
    },
    subtitle: {
      ...plan.subtitle,
      inventory: [
        fixtureSubtitleInventoryItemV3({ combined_index: 0, url, font_bundle_url: fontUrl }),
      ],
    },
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith("/capabilities")) return json(cap);
      if (path.endsWith("/start")) {
        ambientToken = "rotated-during-response";
        return json(
          {
            protocol_version: 3,
            server_features: ["sequenced_progress_v1"],
            outcome: "playable",
            session_id: sid,
            playback_plan: wire,
          },
          201,
        );
      }
      if (path.endsWith("/route-events")) return new Response(null, { status: 202 });
      return json({ outcome: "stopped", last_sequence: 1 });
    }),
  );
  const { result } = renderHook(() => usePlaybackSession("proxy", [], [], 42, 0, false, "auto"), {
    wrapper: ({ children }) =>
      createElement(PlayerConfigProvider, { config: scopedConfig, children }),
  });
  await waitFor(() => expect(result.current.sessionId).toBe(sid));
  expect(result.current.subtitleUrls[0]).toMatchObject({
    url,
    font_bundle_url: fontUrl,
    request_headers: { authorization: "Bearer captured-only", "x-profile-id": "profile" },
  });
  expect(result.current.plan?.stream.headers).toEqual({});
  expect(JSON.stringify(localStorage)).not.toContain("captured-only");
  authorityCurrent = false;
  expect(result.current.subtitleUrls[0]?.request_is_current?.()).toBe(false);
  act(() =>
    result.current.applySubtitleTrack(
      fixtureSubtitleInventoryItemV3({ combined_index: 0, url, font_bundle_url: fontUrl }),
    ),
  );
  await waitFor(() => expect(result.current.subtitleUrls[0]?.url).toBe(""));
});

it("does not start a subtitle fallback after a validated owner-loss terminal decision", async () => {
  const fetcher = vi.fn(async (input: RequestInfo | URL, options?: RequestInit) => {
    if (String(input).endsWith("/capabilities")) return json(cap);
    const original = JSON.parse(String(options?.body));
    return json(
      {
        protocol_version: 3,
        server_features: ["sequenced_progress_v1"],
        outcome: "adaptation_unavailable",
        terminal: { reason: "playback_owner_lost", retryable: false, message: "Playback ended." },
        recovery: {
          recovery_id: "recovery",
          playback_attempt_id: original.playback_attempt_id,
          session_id: "lost-session",
          reason: "owner_lost",
          state: "aborted",
        },
      },
      201,
    );
  });
  vi.stubGlobal("fetch", fetcher);
  const { result } = renderHook(
    () =>
      usePlaybackSession(
        "owner-lost",
        [],
        [],
        42,
        0,
        false,
        "auto",
        null,
        undefined,
        null,
        { 42: 0 },
        { 42: 0 },
      ),
    { wrapper },
  );
  await waitFor(() => expect(result.current.loading).toBe(false));
  expect(result.current.sessionId).toBeNull();
  expect(result.current.error).toBe("Playback ended.");
  expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/start"))).toHaveLength(1);
  expect(fetcher.mock.calls.some(([url]) => String(url).endsWith("/route-events"))).toBe(false);
  expect(Object.keys(localStorage).some((key) => key.startsWith("silo-playback-start-v1:"))).toBe(
    false,
  );
});
