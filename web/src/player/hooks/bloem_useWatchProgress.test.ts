// Bloem: progress reporting stops once the server no longer has the session
// (useWatchProgress.ts). Drop with that seam once the fix lands upstream.
import { act, renderHook } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";

import { PlayerConfigProvider, type PlayerConfig } from "../context/PlayerConfigContext";
import { PlayerFetchError } from "../player-fetch";

const mocks = vi.hoisted(() => ({ send: vi.fn() }));

vi.mock("../session-mutations", () => ({
  sendSessionProgress: (...args: unknown[]) => mocks.send(...args),
  observeSessionProgress: () => () => {},
  captureSessionProgress: () => {},
}));

import { useWatchProgress } from "./useWatchProgress";

const playerConfig: PlayerConfig = {
  apiBaseUrl: "/api/v1",
  getAccessToken: () => "token",
  getProfileId: () => "profile-1",
  getDeviceId: () => "test-device",
  getProfileToken: () => null,
};

function wrapper({ children }: { children: ReactNode }) {
  return createElement(PlayerConfigProvider, { config: playerConfig, children });
}

const notFoundProblem = JSON.stringify({
  type: "https://siloserver.org/docs/api/v2/problems/not_found",
  title: "Not found",
  status: 404,
  detail: "Playback session not found",
});

afterEach(() => {
  vi.useRealTimers();
});

// A stopped session's row is purged once it expires, and the server then
// answers 404 rather than 410. A tab left open on it must stop posting
// progress for that session, without ending playback.
it("stops reporting once the server no longer has the session", async () => {
  mocks.send.mockReset();
  mocks.send.mockRejectedValue(
    new PlayerFetchError(404, "Playback session not found", "not_found", notFoundProblem),
  );
  const onSessionEnded = vi.fn();
  const video = document.createElement("video");
  Object.defineProperty(video, "currentTime", { value: 30 });
  Object.defineProperty(video, "readyState", { value: HTMLMediaElement.HAVE_METADATA });
  const videoRef = { current: video };
  vi.useFakeTimers();
  const rendered = renderHook(
    () => useWatchProgress("session-1", videoRef, undefined, onSessionEnded),
    { wrapper },
  );
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10_000);
  });
  expect(mocks.send).toHaveBeenCalledTimes(1);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60_000);
  });
  expect(mocks.send).toHaveBeenCalledTimes(1);
  expect(onSessionEnded).not.toHaveBeenCalled();
  mocks.send.mockResolvedValue(undefined);
  rendered.unmount();
});
