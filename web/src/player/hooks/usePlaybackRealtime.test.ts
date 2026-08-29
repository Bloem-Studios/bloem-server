import { describe, expect, it } from "vitest";
import { buildPlaybackRealtimeUrl } from "./usePlaybackRealtime";

describe("buildPlaybackRealtimeUrl", () => {
  it("uses only a scoped websocket ticket", () => {
    const url = buildPlaybackRealtimeUrl("/api/v1", "session-123", "short-lived ticket");

    expect(url).toBe("/api/v1/playback/sessions/session-123/control/ws?ticket=short-lived+ticket");
    expect(url).not.toContain("token=");
  });
});
