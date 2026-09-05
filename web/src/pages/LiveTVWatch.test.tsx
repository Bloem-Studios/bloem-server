import type { ReactNode } from "react";
import { render, screen, waitFor, fireEvent, cleanup } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import { setAccessToken, setProfileId, setProfileToken } from "@/api/client";
import { buildLiveWatchHref } from "@/lib/liveTVWatch";
import LiveTVWatch from "./LiveTVWatch";
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({ profile: { id: "viewer-a" } }) }));
vi.mock("@/components/livetv/LiveTVAccessGate", () => ({
  LiveTVAccessGate: ({ children }: { children: ReactNode }) => children,
}));
vi.mock("@/player/hooks/useCodecDetection", () => ({
  useCodecDetection: () => ({
    settled: true,
    codecsVideo: ["h264"],
    codecsAudio: ["aac", "ac3", "eac3"],
    maxResolution: "1080p",
  }),
}));
vi.mock("@/components/livetv/LiveTVPlayer", () => ({
  LiveTVPlayer: ({
    streamUrl,
    onErrorChange,
  }: {
    streamUrl: string;
    onErrorChange: (message: string) => void;
  }) => (
    <div>
      <video src={streamUrl} aria-label="Live video" />
      <button onClick={() => onErrorChange("Stream disconnected")}>Fail player</button>
    </div>
  ),
}));
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("opens a guide watch link and releases the tuner after a fatal player error", async () => {
  setAccessToken("viewer-token");
  setProfileId("viewer-a");
  setProfileToken("pin-a");
  const requests: Array<{ url: string; method?: string; body?: BodyInit | null }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn<typeof fetch>(async (input, init) => {
      requests.push({ url: String(input), method: init?.method, body: init?.body });
      if (init?.method === "DELETE") return new Response(null, { status: 204 });
      return Response.json({ session_id: "lease-1", hls_url: "/live.m3u8", transport: "hls" });
    }),
  );
  render(
    <MemoryRouter initialEntries={[buildLiveWatchHref("channel-1")]}>
      <Routes>
        <Route path="/watch/live/:channelId" element={<LiveTVWatch />} />
      </Routes>
    </MemoryRouter>,
  );
  await screen.findByLabelText("Live video");
  expect(requests[0]?.url).toBe("/api/v1/livetv/channels/channel-1/session");
  expect(JSON.parse(String(requests[0]?.body))).toEqual({
    codecs_video: ["h264"],
    codecs_audio: ["aac"],
    max_resolution: "1080p",
  });
  fireEvent.click(screen.getByText("Fail player"));
  await screen.findByText("Stream disconnected");
  expect(screen.queryByLabelText("Live video")).toBeNull();
  await waitFor(() =>
    expect(
      requests.some(
        (request) =>
          request.url === "/api/v1/livetv/sessions/lease-1" && request.method === "DELETE",
      ),
    ).toBe(true),
  );
  expect(screen.getByRole("link", { name: "Back to Live TV" }).getAttribute("href")).toBe(
    "/livetv",
  );
});
