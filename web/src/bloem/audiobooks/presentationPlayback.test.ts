// The chosen presentation through Silo's unchanged audiobook player: the F1
// native resume at book position 100 starts track 602 at local 30 as an
// original, client-persisted session (brief fixture F5), and progress is
// reported book-global against the presentation's own total (150, never 360).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { createElement, type MutableRefObject, type ReactNode } from "react";
import { PlayerConfigProvider, type PlayerConfig } from "@/player";
import { resetCodecDetectionForTests } from "@/player/hooks/useCodecDetection";
import { resetSessionMutations } from "@/player/session-mutations";
import { useAudiobookPlayback } from "@/pages/audiobooks/player/useAudiobookPlayback";
import { F1_NATIVE_ID, F1_WIRE, fixtureItem } from "./fixtures";
import { itemAudiobookPresentations, presentationFiles } from "./presentations";

const reportBookProgress = vi.hoisted(() => vi.fn());
vi.mock("@/hooks/queries/progress", () => ({
  useReportMediaProgress: () => ({ mutate: reportBookProgress }),
}));
vi.mock("@/player/start-v2", () => ({
  startPlaybackV2: async (config: PlayerConfig, body: unknown) => {
    const { playerFetch } = await import("@/player/player-fetch");
    const decision = await playerFetch<{
      session_id?: string;
      playback_plan?: { session_id?: string };
    }>({ ...config, apiBaseUrl: "/api/v2" }, "/playback/start", {
      method: "POST",
      body: JSON.stringify(body),
    });
    const sessionId = decision?.playback_plan?.session_id ?? decision?.session_id;
    if (decision?.playback_plan && sessionId) {
      const { registerSessionMutations } = await import("@/player/session-mutations");
      registerSessionMutations(sessionId, "installation");
    }
    return decision;
  },
}));
vi.mock("@/player/hooks/usePlaybackRealtime", () => ({
  usePlaybackRealtime: vi.fn(() => ({ connectionState: "connected" })),
}));

const playerConfig: PlayerConfig = {
  apiBaseUrl: "/api/v1",
  getAccessToken: () => "token",
  getProfileId: () => "profile-1",
  getDeviceId: () => "test-device",
  getProfileToken: () => null,
};

/** Brief fixture F6, playable: the native track as an original file. */
function playableDecision(sessionId: string, startPosition: number, fileId: string) {
  return {
    protocol_version: 3,
    server_features: ["playback_plan_v3"],
    outcome: "playable",
    session_id: sessionId,
    playback_plan: {
      protocol_version: 3,
      plan_id: `plan:${sessionId}`,
      plan_attempt_key: "v3:0000000000000001",
      session_id: sessionId,
      delivery: "original_http",
      stream: {
        url: `/stream/${sessionId}?st=token`,
        protocol: "http_progressive",
        container: "mp4",
        mime_type: "application/octet-stream",
        headers: {},
        header_refresh: "none",
      },
      timeline: {
        source_start_seconds: startPosition,
        stream_origin_seconds: 0,
        player_start_seconds: startPosition,
        timeline_offset_seconds: 0,
        can_seek_anywhere: true,
        seek_restoration: "player_position",
      },
      selected_tracks: {},
      effective_recipe: {},
      claims: {},
      subtitle: { mode: "off", inventory: [] },
      transformations: [],
      applied_quirks: [],
      runtime_corrections: [],
      available_qualities: [{ label: "original", preserves_source: true }],
      degradation_warnings: [],
      decision_reason: "validated_original_playback",
      requested_media_file_id: fileId,
      effective_media_file_id: fileId,
      source: { media_file_id: fileId, duration_seconds: 80 },
      subtitle_fidelity_policy: "preserve",
    },
  };
}

function jsonResponse(body: unknown, init: ResponseInit = {}) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
    ...init,
  });
}

async function flushAsyncWork() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
}

function makeAudio() {
  const audio = document.createElement("audio");
  Object.defineProperty(audio, "duration", { value: 80, writable: true });
  Object.defineProperty(audio, "paused", { value: true, writable: true });
  audio.play = vi.fn().mockResolvedValue(undefined);
  audio.pause = vi.fn();
  audio.load = vi.fn();
  return audio;
}

function startBodies() {
  return vi
    .mocked(fetch)
    .mock.calls.filter(([url]) => String(url).endsWith("/playback/start"))
    .map(([, init]) => JSON.parse(String(init?.body)) as Record<string, unknown>);
}

describe("native presentation through Silo's audiobook player", () => {
  beforeEach(() => {
    reportBookProgress.mockClear();
    vi.useFakeTimers();
    vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockImplementation((mime) =>
      ["audio/mp4", "audio/mpeg", "audio/flac", "audio/ogg"].some((supported) =>
        mime.startsWith(supported),
      )
        ? "probably"
        : "",
    );
    let sessions = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith("/playback/start")) {
          sessions += 1;
          const body = JSON.parse(String(init?.body)) as {
            start_position: number;
            file_id: unknown;
          };
          return jsonResponse(
            playableDecision(`session-${sessions}`, body.start_position, String(body.file_id)),
            { status: 201 },
          );
        }
        if (url.endsWith("/playback/route-events")) return new Response(null, { status: 202 });
        if (url.includes("/progress")) return new Response(null, { status: 204 });
        if (init?.method === "DELETE") return jsonResponse({ outcome: "stopped" });
        return jsonResponse({});
      }),
    );
  });

  afterEach(() => {
    resetCodecDetectionForTests();
    resetSessionMutations();
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("starts 602 at local 30 and reports 112 of 150", async () => {
    const item = fixtureItem(F1_WIRE);
    const native = itemAudiobookPresentations(item).find((entry) => entry.id === F1_NATIVE_ID)!;
    const files = presentationFiles(native, item.title);
    const { result, unmount } = renderHook(
      () =>
        useAudiobookPlayback({ contentId: item.content_id, files, initialPositionSeconds: 100 }),
      {
        wrapper: ({ children }: { children: ReactNode }) =>
          createElement(PlayerConfigProvider, { config: playerConfig, children }),
      },
    );
    act(() => {
      (result.current.audioRef as MutableRefObject<HTMLAudioElement | null>).current = makeAudio();
    });
    await flushAsyncWork();

    expect(result.current.duration).toBe(150);
    expect(result.current.chapters.map((chapter) => chapter.start_seconds)).toEqual([
      0, 40.25, 70, 75,
    ]);
    const [start] = startBodies();
    expect(start).toMatchObject({
      protocol_version: 3,
      file_id: 602,
      start_position: 30,
      quality_preference: "original",
      progress_persistence: "client",
    });

    act(() => result.current.seekTo(112));
    await flushAsyncWork();
    reportBookProgress.mockClear();
    unmount();
    await flushAsyncWork();

    expect(reportBookProgress).toHaveBeenCalledExactlyOnceWith({
      contentId: item.content_id,
      positionSeconds: 112,
      durationSeconds: 150,
    });
    // Only the native pair was ever requested.
    expect(startBodies().every((body) => body.file_id === 601 || body.file_id === 602)).toBe(true);
  });
});
