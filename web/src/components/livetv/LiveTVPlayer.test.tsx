// @vitest-environment jsdom

import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LiveTVPlayer } from "./LiveTVPlayer";
import Hls from "hls.js";

const { mockHls, hlsListeners } = vi.hoisted(() => {
  const hlsListeners = new Map<string, Array<(event: string, data: unknown) => void>>();
  const mockHls = {
    loadSource: vi.fn(),
    attachMedia: vi.fn(),
    destroy: vi.fn(),
    startLoad: vi.fn(),
    recoverMediaError: vi.fn(),
    liveSyncPosition: 12.5 as number | null,
    on: vi.fn((event: string, cb: (event: string, data: unknown) => void) => {
      const list = hlsListeners.get(event) ?? [];
      list.push(cb);
      hlsListeners.set(event, list);
    }),
    emit(event: string, data: unknown) {
      for (const cb of hlsListeners.get(event) ?? []) {
        cb(event, data);
      }
    },
    reset() {
      hlsListeners.clear();
      mockHls.loadSource.mockReset();
      mockHls.attachMedia.mockReset();
      mockHls.destroy.mockReset();
      mockHls.startLoad.mockReset();
      mockHls.recoverMediaError.mockReset();
      mockHls.liveSyncPosition = 12.5;
      mockHls.on.mockClear();
    },
  };
  return { mockHls, hlsListeners };
});

vi.mock("hls.js", () => {
  function Hls() {
    return mockHls;
  }
  Hls.isSupported = () => true;
  Hls.Events = {
    MANIFEST_PARSED: "hlsManifestParsed",
    ERROR: "hlsError",
  };
  Hls.ErrorTypes = {
    NETWORK_ERROR: "networkError",
    MEDIA_ERROR: "mediaError",
  };
  Hls.ErrorDetails = {
    BUFFER_STALLED_ERROR: "bufferStalledError",
    BUFFER_APPEND_ERROR: "bufferAppendError",
    MANIFEST_LOAD_ERROR: "manifestLoadError",
  };
  return { default: Hls };
});

vi.mock("mpegts.js", () => ({
  default: {
    isSupported: () => false,
    getFeatureList: () => ({ mseLivePlayback: false }),
    createPlayer: vi.fn(),
    Events: { ERROR: "error" },
  },
}));

vi.mock("@/api/client", () => ({
  getAccessToken: () => "access-token",
  getProfileId: () => "profile-1",
  getProfileToken: () => null,
}));

function emitFatalAppendError() {
  mockHls.emit("hlsError", {
    fatal: true,
    type: "mediaError",
    details: "bufferAppendError",
    sourceBufferName: "audio",
    error: { message: "SourceBuffer append failed" },
    frag: { sn: 0, url: "seg_00000.ts" },
  });
}

async function mountHlsPlayer(hideNativeControls = true) {
  render(
    <LiveTVPlayer
      streamUrl="/api/v1/livetv/live-hls/sess/index.m3u8"
      transport="hls"
      hideNativeControls={hideNativeControls}
    />,
  );
  await waitFor(() => {
    expect(mockHls.loadSource).toHaveBeenCalled();
    expect(hlsListeners.get("hlsError")?.length).toBeGreaterThan(0);
  });
}

describe("LiveTVPlayer HLS bufferAppendError recovery", () => {
  let now: number;

  beforeEach(() => {
    mockHls.reset();
    now = 1_000_000;
    vi.spyOn(Date, "now").mockImplementation(() => now);
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("seeks to the live edge and recovers instead of re-appending the same fragment", async () => {
    await mountHlsPlayer();
    const video = screen.getByLabelText("Live TV") as HTMLVideoElement;

    act(() => {
      emitFatalAppendError();
    });

    expect(video.currentTime).toBe(12.5);
    expect(mockHls.recoverMediaError).toHaveBeenCalledTimes(1);
    expect(mockHls.startLoad).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(/rejected by the browser/i)).toBeNull();
  });

  it("throttles recoveries that arrive within 1.5s", async () => {
    await mountHlsPlayer();

    act(() => {
      emitFatalAppendError();
      emitFatalAppendError();
    });
    expect(mockHls.recoverMediaError).toHaveBeenCalledTimes(1);

    act(() => {
      now += 1500;
      emitFatalAppendError();
    });
    expect(mockHls.recoverMediaError).toHaveBeenCalledTimes(2);
  });

  it("surfaces a friendly error after append recovery is exhausted", async () => {
    await mountHlsPlayer(false);

    for (let i = 0; i < 6; i++) {
      act(() => {
        if (i > 0) now += 1500;
        emitFatalAppendError();
      });
    }

    expect(mockHls.recoverMediaError).toHaveBeenCalledTimes(5);
    expect(screen.getByText(/Live stream media was rejected by the browser/i)).toBeInTheDocument();
  });

  it("keeps account credentials out of HLS URLs", async () => {
    await mountHlsPlayer();
    const url = new URL(mockHls.loadSource.mock.calls[0]![0]);
    expect(url.searchParams.has("token")).toBe(false);
    expect(url.searchParams.has("profile_id")).toBe(false);
    expect(url.href).not.toContain("access-token");
  });

  it.each([
    "https://foreign.example/stream.m3u8",
    `${window.location.origin.replace("//", "//user:secret@")}/stream.m3u8`,
  ])("rejects untrusted stream addresses: %s", (url) => {
    render(<LiveTVPlayer streamUrl={url} transport="hls" />);
    expect(screen.getByText(/unsupported Live TV stream address/)).toBeInTheDocument();
    expect(mockHls.loadSource).not.toHaveBeenCalled();
  });

  it("plays signed native HLS without MSE and detaches on teardown", async () => {
    vi.spyOn(Hls, "isSupported").mockReturnValue(false);
    vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("probably");
    const load = vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => undefined);
    const pause = vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => undefined);
    const playing = vi.fn();
    const view = render(
      <LiveTVPlayer
        streamUrl="/api/v1/livetv/live-hls/session/index.m3u8?st=stream-ticket"
        transport="hls"
        onPlayingChange={playing}
      />,
    );
    const video = screen.getByLabelText("Live TV") as HTMLVideoElement;
    expect(video.src).toBe(
      `${window.location.origin}/api/v1/livetv/live-hls/session/index.m3u8?st=stream-ticket`,
    );
    expect(mockHls.loadSource).not.toHaveBeenCalled();
    act(() => video.dispatchEvent(new Event("loadedmetadata")));
    await waitFor(() => expect(video.play).toHaveBeenCalled());
    act(() => video.dispatchEvent(new Event("playing")));
    expect(playing).toHaveBeenLastCalledWith(true);
    view.unmount();
    expect(video.getAttribute("src")).toBeNull();
    expect(pause).toHaveBeenCalled();
    expect(load).toHaveBeenCalledTimes(2);
    playing.mockClear();
    act(() => video.dispatchEvent(new Event("playing")));
    expect(playing).not.toHaveBeenCalled();
  });

  it("never substitutes account query credentials for a missing native stream ticket", () => {
    vi.spyOn(Hls, "isSupported").mockReturnValue(false);
    vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("probably");
    render(<LiveTVPlayer streamUrl="/api/v1/livetv/live-hls/session/index.m3u8" transport="hls" />);
    expect(screen.getByText(/server-signed native HLS stream/)).toBeInTheDocument();
    expect(screen.getByLabelText("Live TV")).not.toHaveAttribute("src");
  });
});
