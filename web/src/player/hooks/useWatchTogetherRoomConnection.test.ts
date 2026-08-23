import { describe, expect, it } from "vitest";
import { buildRoomWebSocketUrl } from "./useWatchTogetherRoomConnection";

describe("buildRoomWebSocketUrl", () => {
  it("carries only the single-use route ticket", () => {
    const url = new URL(buildRoomWebSocketUrl("https://example.com/api/v1", "room-1", "ticket-1"));

    expect(url.protocol).toBe("wss:");
    expect(url.searchParams.get("ticket")).toBe("ticket-1");
    expect(url.searchParams.has("token")).toBe(false);
    expect(url.searchParams.has("profile_token")).toBe(false);
    expect(url.searchParams.has("room_token")).toBe(false);
  });
});
