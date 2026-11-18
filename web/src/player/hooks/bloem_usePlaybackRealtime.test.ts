// Bloem: the playback control ticket mint stops once the server answers 404 for
// a purged attempt (usePlaybackRealtime.ts). Drop with that seam once the fix
// lands upstream.
import { act, cleanup, renderHook } from "@testing-library/react";
import { createElement } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { V2ProblemError } from "@/api/v2/request";
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

// A session the server no longer has (404) is never coming back, so the hook
// stops minting.
it("stops minting once the server no longer has the session", async () => {
  vi.useFakeTimers();
  mocks.mint.mockRejectedValue(
    new V2ProblemError("createPlaybackControlSocketTicket", {
      type: "https://siloserver.org/docs/api/v2/problems/not_found",
      title: "Not found",
      status: 404,
      detail: "Playback session not found",
      instance: "/api/v2/playback/sessions/session/control-socket-tickets",
    }),
  );
  const { result } = renderHook(
    () => usePlaybackRealtime({ sessionId: "session", onCommand: vi.fn() }),
    { wrapper },
  );
  await act(async () => {
    await vi.advanceTimersByTimeAsync(180_000);
  });
  expect(mocks.mint).toHaveBeenCalledTimes(1);
  expect(result.current.connectionState).toBe("disconnected");
});
