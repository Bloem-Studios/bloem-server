// Bloem: the playback control ticket retry backs off to 30 s (usePlaybackRealtime.ts).
// Drop with that seam once Silo-Server/silo-server#4392 is merged upstream.
import { act, cleanup, renderHook } from "@testing-library/react";
import { createElement } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { PlayerConfigProvider, type PlayerConfig } from "../context/PlayerConfigContext";
import { usePlaybackRealtime } from "./usePlaybackRealtime";

const mocks = vi.hoisted(() => ({
  capture: vi.fn(),
  current: vi.fn(),
  installation: vi.fn(),
  mint: vi.fn(),
}));
vi.mock("@/api/client", () => ({
  captureProfileRequestContext: mocks.capture,
  isCapturedProfileAuthorityActive: mocks.current,
  StaleApiRequestContextError: class extends Error {},
}));
vi.mock("../session-mutations", () => ({ sessionInstallation: mocks.installation }));
vi.mock("@/api/v2/playbackControlSocket", async (original) => ({
  ...(await original<object>()),
  mintPlaybackControlSocketTicket: mocks.mint,
}));
const config: PlayerConfig = {
  apiBaseUrl: "https://bloem.example.test/api/v1",
  getAccessToken: () => "private-token",
  getProfileId: () => "profile",
  getDeviceId: () => "device",
};
const wrapper = ({ children }: { children: React.ReactNode }) =>
  createElement(PlayerConfigProvider, { config, children });
beforeEach(() => {
  vi.clearAllMocks();
  mocks.capture.mockReturnValue({ profileId: "profile" });
  mocks.current.mockReturnValue(true);
  mocks.installation.mockReturnValue("install");
  vi.stubGlobal("WebSocket", class extends EventTarget {});
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it("backs off to one mint every 30 seconds while minting keeps failing", async () => {
  vi.useFakeTimers();
  mocks.mint.mockRejectedValue(new Error("404"));
  renderHook(() => usePlaybackRealtime({ sessionId: "session", onCommand: vi.fn() }), {
    wrapper,
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(48_500);
  });
  expect(mocks.mint).toHaveBeenCalledTimes(7);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(29_000);
  });
  expect(mocks.mint).toHaveBeenCalledTimes(7);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1_000);
  });
  expect(mocks.mint).toHaveBeenCalledTimes(8);
});
