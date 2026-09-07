import { describe, expect, it } from "vitest";
import { V2ProblemError } from "@/api/v2/request";
import {
  createPlaybackRealtimeUrlFactory,
  isPlaybackControlFallbackError,
} from "./usePlaybackRealtime";

describe("createPlaybackRealtimeUrlFactory", () => {
  it("reads the current token for each reconnect attempt", () => {
    let token: string | null = "stale-token";
    const getUrl = createPlaybackRealtimeUrlFactory("/api/v1", "session-123", () => token);

    expect(getUrl()).toBe("/api/v1/playback/sessions/session-123/control/ws?token=stale-token");

    token = "fresh-token";

    expect(getUrl()).toBe("/api/v1/playback/sessions/session-123/control/ws?token=fresh-token");
  });
});

describe("isPlaybackControlFallbackError", () => {
  const problem = (type: string, status: number) =>
    new V2ProblemError("createPlaybackControlSocketTicket", {
      type: `https://siloserver.org/docs/api/v2/problems/${type}`,
      title: type,
      status,
      detail: type,
      instance: "urn:silo:request:test",
    });
  it("falls back to the bridge only when the v2 handshake is not served", () => {
    expect(isPlaybackControlFallbackError(problem("not_found", 404))).toBe(true);
    expect(isPlaybackControlFallbackError(problem("dependency_unavailable", 503))).toBe(true);
  });
  it("never falls back after an owner or lease refusal, nor on transport errors", () => {
    expect(isPlaybackControlFallbackError(problem("permission_denied", 403))).toBe(false);
    expect(isPlaybackControlFallbackError(problem("conflict", 409))).toBe(false);
    expect(isPlaybackControlFallbackError(new TypeError("network"))).toBe(false);
  });
});
