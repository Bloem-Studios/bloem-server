import { describe, expect, it } from "vitest";
import { roomSocketURL } from "@/api/v2/watchTogetherSocket";

describe("roomSocketURL", () => {
  it("keeps socket credentials out of the URL", () => {
    const url = new URL(roomSocketURL("room-1"));

    expect(["ws:", "wss:"]).toContain(url.protocol);
    expect(url.pathname).toBe("/api/v2/watch-together/rooms/room-1/ws");
    expect(url.search).toBe("");
    expect(url.searchParams.has("token")).toBe(false);
    expect(url.searchParams.has("profile_token")).toBe(false);
    expect(url.searchParams.has("room_token")).toBe(false);
  });
});
