import { act, cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { PlayerConfigProvider, type PlayerConfig } from "@/player/context/PlayerConfigContext";
import { fixturePlanV3 } from "@/player/protocol-v3.fixtures";
import { useAudiobookPlayback, type AudiobookPlayback } from "./useAudiobookPlayback";

const mocks = vi.hoisted(() => ({ bookProgress: vi.fn(), toast: vi.fn() }));
vi.mock("@/hooks/queries/progress", () => ({
  useReportMediaProgress: () => ({ mutate: mocks.bookProgress }),
}));
vi.mock("sonner", () => ({ toast: { error: mocks.toast } }));
vi.mock("@/player/hooks/usePlaybackRealtime", () => ({
  usePlaybackRealtime: () => ({ connectionState: "disconnected" }),
}));
const files = [
  { id: 1, path: "part1.m4b", duration_seconds: 300, chapters: [] },
  { id: 2, path: "part2.m4b", duration_seconds: 300, chapters: [] },
];
let current = true;
let retryStop: (() => void) | undefined;
const context = {
  accountId: "original-account",
  profileId: "profile",
  origin: "http://localhost:3000",
  isCurrent: () => current,
};
const config: PlayerConfig = {
  apiBaseUrl: "/api/v1",
  getAccessToken: () => "synthetic",
  getProfileId: () => "profile",
  getDeviceId: () => "device",
  capturePlaybackMutationContext: () => context,
  onPlaybackStartError: (error) => mocks.toast(error.message),
  onPlaybackStopError: (_id, error, retry) => {
    retryStop = retry;
    mocks.toast(error.message);
  },
};
let latest: AudiobookPlayback;
function Player({ initial = 330 }: { initial?: number }) {
  const playback = useAudiobookPlayback({
    contentId: "book",
    files,
    initialPositionSeconds: initial,
    autoPlay: false,
  });
  const { audioRef, streamUrl } = playback;
  useEffect(() => {
    latest = playback;
  }, [playback]);
  return <audio ref={audioRef} src={streamUrl || undefined} />;
}
function mount(initial = 330) {
  return render(
    <PlayerConfigProvider config={config}>
      <Player initial={initial} />
    </PlayerConfigProvider>,
  );
}
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const cap = {
  installation_id: "install",
  state: "available",
  allowed: true,
  protocol_versions: [3],
  features: ["sequenced_progress_v1"],
};
let unique = 0;
function decision(file: string, position: number) {
  const id = `audio-${unique}-${file}`;
  const plan = fixturePlanV3({ session_id: id });
  return {
    protocol_version: 3,
    server_features: ["sequenced_progress_v1"],
    session_id: id,
    playback_plan: {
      ...plan,
      requested_media_file_id: file,
      effective_media_file_id: file,
      source: { ...plan.source, media_file_id: file },
      timeline: {
        ...plan.timeline,
        source_start_seconds: position,
        player_start_seconds: position,
        timeline_offset_seconds: 0,
      },
      stream: { ...plan.stream, url: `/api/v2/media/files/${file}/original` },
    },
  };
}
function server() {
  const fetcher = vi.fn(async (input: RequestInfo | URL, options?: RequestInit) => {
    const url = String(input);
    if (!url.includes("/api/v2/")) throw new Error(`Legacy request ${url}`);
    if (url.endsWith("/capabilities")) return json(cap);
    const body = JSON.parse(String(options?.body ?? "{}"));
    if (url.endsWith("/start")) return json(decision(body.file_id, body.start_position));
    if (url.endsWith("/replan")) return json(decision("2", body.position ?? 45));
    if (options?.method === "DELETE") return json({ outcome: "stopped", stop_id: body.stop_id });
    if (url.endsWith("/progress")) return json({ outcome: "applied" });
    if (url.endsWith("/route-events")) return new Response(null, { status: 202 });
    throw new Error(`Unexpected request ${url}`);
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
beforeEach(() => {
  unique++;
  current = true;
  retryStop = undefined;
  localStorage.clear();
  vi.clearAllMocks();
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue();
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
  const tails = new Map<string, Promise<unknown>>();
  Object.defineProperty(navigator, "locks", {
    configurable: true,
    value: {
      request: (key: string, _options: unknown, action: () => Promise<unknown>) => {
        const next = (tails.get(key) ?? Promise.resolve()).catch(() => {}).then(action);
        tails.set(key, next);
        return next;
      },
    },
  });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
it("keeps whole-book progress while v2 session clocks follow each part, including cleanup", async () => {
  const fetcher = server();
  const { container, unmount } = mount();
  await waitFor(() => expect(latest.streamUrl).toContain("/files/2/original"));
  const starts = () => fetcher.mock.calls.filter(([url]) => String(url).endsWith("/start"));
  expect(JSON.parse(String(starts()[0]![1]?.body))).toMatchObject({
    file_id: "2",
    start_position: 30,
    progress_persistence: "client",
    installation_id: "install",
  });
  const audio = container.querySelector("audio")!;
  audio.currentTime = 45;
  fireEvent.timeUpdate(audio);
  fireEvent.seeked(audio);
  await waitFor(() =>
    expect(fetcher.mock.calls.some(([url]) => String(url).endsWith("/progress"))).toBe(true),
  );
  expect(mocks.bookProgress).toHaveBeenCalledWith({
    contentId: "book",
    positionSeconds: 345,
    durationSeconds: 600,
  });
  await act(async () => {
    latest.seekTo(40);
  });
  await waitFor(() => expect(latest.streamUrl).toContain("/files/1/original"));
  expect(JSON.parse(String(starts()[1]![1]?.body))).toMatchObject({
    file_id: "1",
    start_position: 40,
    progress_persistence: "client",
  });
  const stops = () => fetcher.mock.calls.filter(([, options]) => options?.method === "DELETE");
  await waitFor(() => expect(stops()).toHaveLength(1));
  expect(JSON.parse(String(stops()[0]![1]?.body))).toMatchObject({
    installation_id: "install",
    position: 45,
  });
  unmount();
  await waitFor(() => expect(stops()).toHaveLength(2));
  expect(stops()[1]![1]?.keepalive).toBe(true);
  expect(fetcher.mock.calls.every(([url]) => String(url).includes("/api/v2/"))).toBe(true);
});
it("replans failures through v2 and preserves the original session binding", async () => {
  const fetcher = server();
  const { container } = mount();
  await waitFor(() => expect(latest.streamUrl).toContain("/files/2/original"));
  fireEvent.error(container.querySelector("audio")!);
  await waitFor(() =>
    expect(fetcher.mock.calls.some(([url]) => String(url).endsWith("/replan"))).toBe(true),
  );
  const call = fetcher.mock.calls.find(([url]) => String(url).endsWith("/replan"))!;
  expect(String(call[0])).toContain(`/api/v2/playback/audio-${unique}-2/replan`);
  expect(JSON.parse(String(call[1]?.body))).toMatchObject({
    installation_id: "install",
    operation: "failure_recovery",
  });
});
it("refuses not_configured without legacy start or unbound telemetry", async () => {
  const fetcher = vi.fn(async (_input: RequestInfo | URL) =>
    json({ ...cap, state: "not_configured", allowed: false }),
  );
  vi.stubGlobal("fetch", fetcher);
  mount();
  await waitFor(() => expect(mocks.toast).toHaveBeenCalled());
  expect(latest.streamUrl).toBe("");
  expect(fetcher).toHaveBeenCalledTimes(1);
});
it("retains an uncertain start across a part change without minting another request", async () => {
  const fetcher = server();
  fetcher.mockImplementation(async (input) => {
    if (String(input).endsWith("/capabilities")) return json(cap);
    throw new TypeError("lost start reply");
  });
  mount();
  await waitFor(() => expect(mocks.toast).toHaveBeenCalled());
  const saved = localStorage.getItem(localStorage.key(0)!);
  await act(async () => {
    latest.seekTo(10);
  });
  await waitFor(() =>
    expect(
      fetcher.mock.calls.filter(([url]) => String(url).endsWith("/capabilities")),
    ).toHaveLength(2),
  );
  expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/start"))).toHaveLength(1);
  expect(localStorage.getItem(localStorage.key(0)!)).toBe(saved);
});
it("quarantines a late start after the original account changes", async () => {
  let finish!: (response: Response) => void;
  const fetcher = server();
  fetcher.mockImplementation(async (input) =>
    String(input).endsWith("/capabilities")
      ? json(cap)
      : new Promise<Response>((resolve) => {
          finish = resolve;
        }),
  );
  const { unmount } = mount();
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  current = false;
  await act(async () => {
    finish(json(decision("2", 30)));
  });
  expect(latest.streamUrl).toBe("");
  unmount();
  expect(fetcher.mock.calls).toHaveLength(2);
  expect(localStorage.length).toBe(1);
});
it("stops a late start canceled on the same account using its durable receipt", async () => {
  let finish!: (response: Response) => void;
  const fetcher = server();
  const normal = fetcher.getMockImplementation()!;
  fetcher.mockImplementation(async (input, options) =>
    String(input).endsWith("/start")
      ? new Promise<Response>((resolve) => {
          finish = resolve;
        })
      : normal(input, options),
  );
  const { unmount } = mount();
  await waitFor(() => expect(finish).toBeTypeOf("function"));
  unmount();
  await act(async () => {
    finish(json(decision("2", 30)));
  });
  await waitFor(() =>
    expect(fetcher.mock.calls.some(([, options]) => options?.method === "DELETE")).toBe(true),
  );
  const stop = fetcher.mock.calls.find(([, options]) => options?.method === "DELETE")!;
  expect(JSON.parse(String(stop[1]?.body))).toMatchObject({ installation_id: "install" });
  expect(stop[1]?.keepalive).toBe(true);
});

it("retries the exact unconfirmed unmount stop and fences book writes after account change", async () => {
  const fetcher = server();
  const normal = fetcher.getMockImplementation()!;
  let refused = false;
  fetcher.mockImplementation(async (input, options) => {
    if (options?.method === "DELETE" && !refused) {
      refused = true;
      return json({ detail: "unconfirmed" }, 409);
    }
    return normal(input, options);
  });
  const { container, unmount } = mount();
  await waitFor(() => expect(latest.streamUrl).toContain("/files/2/original"));
  const audio = container.querySelector("audio")!;
  audio.currentTime = 44;
  fireEvent.timeUpdate(audio);
  current = false;
  fireEvent.pause(audio);
  expect(mocks.bookProgress).not.toHaveBeenCalled();
  current = true;
  unmount();
  await waitFor(() => expect(retryStop).toBeTypeOf("function"));
  await act(async () => {
    retryStop!();
  });
  await waitFor(() =>
    expect(fetcher.mock.calls.filter(([, options]) => options?.method === "DELETE")).toHaveLength(
      2,
    ),
  );
  const stops = fetcher.mock.calls.filter(([, options]) => options?.method === "DELETE");
  expect(stops[0]![1]?.body).toBe(stops[1]![1]?.body);
  expect(JSON.parse(String(stops[1]![1]?.body))).toMatchObject({
    position: 44,
    installation_id: "install",
  });
});
