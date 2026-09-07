import type { AudiobookChapterIntent } from "@/player/bound-client-timeline";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
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
let retryStart: (() => void) | undefined;
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
  onPlaybackStartError: (error, retry) => {
    retryStart = retry;
    mocks.toast(error.message);
  },
  onPlaybackStopError: (_id, error, retry) => {
    retryStop = retry;
    mocks.toast(error.message);
  },
};
let latest: AudiobookPlayback;
let queryClient: QueryClient;
function Player({
  initial = 330,
  chapter,
}: {
  initial?: number;
  chapter?: AudiobookChapterIntent;
}) {
  const playback = useAudiobookPlayback({
    contentId: "book",
    files,
    initialPositionSeconds: initial,
    initialChapter: chapter,
    autoPlay: false,
  });
  const { audioRef, streamUrl } = playback;
  useEffect(() => {
    latest = playback;
  }, [playback]);
  return <audio ref={audioRef} src={streamUrl || undefined} />;
}
function mount(initial = 330, chapter?: AudiobookChapterIntent) {
  queryClient = new QueryClient();
  return render(
    <QueryClientProvider client={queryClient}>
      <PlayerConfigProvider config={config}>
        <Player initial={initial} chapter={chapter} />
      </PlayerConfigProvider>
    </QueryClientProvider>,
  );
}
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const cap = {
  installation_id: "install",
  state: "available",
  allowed: true,
  protocol_versions: [3],
  features: ["sequenced_progress_v1", "bound_client_timeline"],
};
const manifest = {
  installation_id: "install",
  timeline_id: "a".repeat(64),
  media_item_id: "book",
  edition_id: "edition",
  duration_seconds: 600,
  parts: [
    { file_id: "1", offset_seconds: 0, duration_seconds: 300 },
    { file_id: "2", offset_seconds: 300, duration_seconds: 300 },
  ],
};
function accepted(
  body: { sequence: number; position: number; is_paused: boolean },
  second: boolean,
) {
  return {
    ...body,
    timeline_id: manifest.timeline_id,
    item_position: body.position + (second ? 300 : 0),
  };
}
let unique = 0;
function decision(file: string, position: number) {
  const id = `audio-${unique}-${file}`;
  const plan = fixturePlanV3({ session_id: id });
  return {
    protocol_version: 3,
    server_features: ["sequenced_progress_v1", "bound_client_timeline"],
    session_id: id,
    progress_timeline: {
      timeline_id: manifest.timeline_id,
      media_item_id: "book",
      file_id: file,
      part_offset_seconds: file === "2" ? 300 : 0,
      part_duration_seconds: 300,
      duration_seconds: 600,
    },
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
    if (url.includes("/timelines/")) return json(manifest);
    const body = JSON.parse(String(options?.body ?? "{}"));
    if (url.endsWith("/start")) return json(decision(body.file_id, body.start_position));
    if (url.endsWith("/replan")) return json(decision("2", body.position ?? 45));
    if (options?.method === "DELETE")
      return json({
        outcome: "stopped",
        stop_id: body.stop_id,
        ...(body.sequence ? { accepted: accepted(body, url.endsWith("-2")) } : {}),
      });
    if (url.endsWith("/progress"))
      return json({ outcome: "applied", accepted: accepted(body, url.includes("-2/")) });
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
  retryStart = undefined;
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
afterEach(async () => {
  await act(async () => cleanup());
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
    progress_persistence: "client_bound",
    timeline_id: manifest.timeline_id,
    installation_id: "install",
  });
  const audio = container.querySelector("audio")!;
  audio.currentTime = 45;
  fireEvent.timeUpdate(audio);
  fireEvent.seeked(audio);
  await waitFor(() =>
    expect(fetcher.mock.calls.some(([url]) => String(url).endsWith("/progress"))).toBe(true),
  );
  expect(mocks.bookProgress).not.toHaveBeenCalled();
  await act(async () => {
    latest.seekTo(40);
  });
  await waitFor(() => expect(latest.streamUrl).toContain("/files/1/original"));
  expect(JSON.parse(String(starts()[1]![1]?.body))).toMatchObject({
    file_id: "1",
    start_position: 40,
    progress_persistence: "client_bound",
    timeline_id: manifest.timeline_id,
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
    if (String(input).includes("/timelines/")) return json(manifest);
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
      : String(input).includes("/timelines/")
        ? json(manifest)
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
  expect(fetcher.mock.calls).toHaveLength(4);
  expect(
    Object.keys(localStorage).filter((key) => key.startsWith("silo-playback-start-v1:")),
  ).toHaveLength(1);
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

it("holds an immutable cross-part target through an unknown stop, then retries the same stop", async () => {
  const fetcher = server();
  const normal = fetcher.getMockImplementation()!;
  let firstStop = true;
  fetcher.mockImplementation(async (input, options) => {
    if (options?.method === "DELETE" && firstStop) {
      firstStop = false;
      return json({ detail: "stop outcome unknown" }, 409);
    }
    return normal(input, options);
  });
  mount();
  await waitFor(() => expect(latest.streamUrl).toContain("/files/2/original"));
  await act(async () => {
    latest.seekTo(40);
  });
  await waitFor(() =>
    expect(mocks.toast).toHaveBeenCalledWith("Audiobook part change pending", expect.any(Object)),
  );
  const starts = () => fetcher.mock.calls.filter(([url]) => String(url).endsWith("/start"));
  expect(starts()).toHaveLength(1);
  expect(latest.streamUrl).toContain("/files/2/original");
  expect(latest.currentTime).toBe(330);
  await act(async () => {
    latest.seekTo(70);
  });
  expect(starts()).toHaveLength(1);
  const options = mocks.toast.mock.calls.find(
    ([message]) => message === "Audiobook part change pending",
  )![1];
  await act(async () => {
    options.action.onClick();
  });
  await waitFor(() => expect(starts()).toHaveLength(2));
  expect(JSON.parse(String(starts()[1]![1]?.body))).toMatchObject({
    file_id: "1",
    start_position: 40,
  });
  const stops = fetcher.mock.calls.filter(([, options]) => options?.method === "DELETE");
  expect(stops).toHaveLength(2);
  expect(stops[0]![1]?.body).toBe(stops[1]![1]?.body);
});
it("does not advance at natural part end until the durable terminal receipt arrives", async () => {
  const fetcher = server();
  const normal = fetcher.getMockImplementation()!;
  let finishStop!: () => void;
  fetcher.mockImplementation(async (input, options) => {
    if (options?.method === "DELETE" && !finishStop) {
      return new Promise<Response>((resolve) => {
        finishStop = () =>
          resolve(
            json({
              outcome: "stopped",
              stop_id: JSON.parse(String(options.body)).stop_id,
              accepted: accepted(JSON.parse(String(options.body)), false),
            }),
          );
      });
    }
    return normal(input, options);
  });
  const { container } = mount(0);
  await waitFor(() => expect(latest.streamUrl).toContain("/files/1/original"));
  fireEvent.ended(container.querySelector("audio")!);
  await waitFor(() => expect(finishStop).toBeTypeOf("function"));
  expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/start"))).toHaveLength(1);
  const stop = fetcher.mock.calls.find(([, options]) => options?.method === "DELETE")!;
  expect(JSON.parse(String(stop[1]?.body)).position).toBe(300);
  await act(async () => {
    finishStop();
  });
  await waitFor(() => expect(latest.streamUrl).toContain("/files/2/original"));
});

it.each([undefined, { fileId: "1", positionSeconds: 30 }])(
  "resolves global resume or exact chapter intent against server order and durations: %j",
  async (chapter) => {
    const fetcher = server();
    const normal = fetcher.getMockImplementation()!;
    const snapshot = {
      ...manifest,
      parts: [
        { file_id: "2", offset_seconds: 0, duration_seconds: 200 },
        { file_id: "1", offset_seconds: 200, duration_seconds: 400 },
      ],
    };
    fetcher.mockImplementation(async (input, options) => {
      if (String(input).includes("/timelines/")) return json(snapshot);
      if (String(input).endsWith("/start")) {
        const body = JSON.parse(String(options?.body));
        const result = decision(body.file_id, body.start_position);
        const part = snapshot.parts.find((part) => part.file_id === body.file_id)!;
        result.progress_timeline = {
          ...result.progress_timeline,
          part_offset_seconds: part.offset_seconds,
          part_duration_seconds: part.duration_seconds,
        };
        return json(result);
      }
      return normal(input, options);
    });
    mount(330, chapter);
    await waitFor(() => expect(latest.streamUrl).toContain("/files/1/original"));
    const start = fetcher.mock.calls.find(([url]) => String(url).endsWith("/start"))!;
    expect(JSON.parse(String(start[1]?.body))).toMatchObject({
      file_id: "1",
      start_position: chapter ? 30 : 130,
      timeline_id: snapshot.timeline_id,
    });
    expect(latest.duration).toBe(600);
    const discovery = fetcher.mock.calls.find(([url]) => String(url).includes("/timelines/"))!;
    expect(String(discovery[0])).toContain("/timelines/1?installation_id=install");
    expect(discovery[1]?.cache).toBe("no-store");
    expect(fetcher.mock.calls.filter(([url]) => String(url).includes("/timelines/"))).toHaveLength(
      1,
    );
  },
);
it("holds a metadata 409 on the pinned next-part intent without rediscovery or fresh start", async () => {
  const fetcher = server();
  const normal = fetcher.getMockImplementation()!;
  let starts = 0;
  fetcher.mockImplementation(async (input, options) => {
    if (String(input).endsWith("/start") && ++starts > 1)
      return json({ detail: "timeline changed" }, 409);
    return normal(input, options);
  });
  mount();
  await waitFor(() => expect(latest.streamUrl).toContain("/files/2/original"));
  act(() => latest.seekTo(40));
  await waitFor(() => expect(starts).toBe(2));
  await waitFor(() => expect(mocks.toast).toHaveBeenCalled());
  const key = Object.keys(localStorage).find((key) => key.startsWith("silo-playback-start-v1:"))!;
  const original = localStorage.getItem(key);
  expect(JSON.parse(original!)).toMatchObject({
    timeline_id: manifest.timeline_id,
    file_id: "1",
    start_position: 40,
  });
  act(() => latest.seekTo(500));
  expect(localStorage.getItem(key)).toBe(original);
  expect(starts).toBe(2);
  expect(fetcher.mock.calls.filter(([url]) => String(url).includes("/timelines/"))).toHaveLength(1);
});
it("refreshes book progress, every detail scope and section items only after an accepted bound receipt", async () => {
  server();
  const { container } = mount();
  await waitFor(() => expect(latest.streamUrl).toContain("/files/2/original"));
  const keys = [
    ["progress"],
    ["catalog", "items", "book", "detail", "default"],
    ["catalog", "items", "book", "detail", 2],
    ["sections", "home", "items", "continue-listening"],
  ];
  for (const key of keys) queryClient.setQueryData(key, { old: true });
  const unrelated = ["catalog", "items", "other", "detail", "default"];
  queryClient.setQueryData(unrelated, { old: true });
  const audio = container.querySelector("audio")!;
  audio.currentTime = 0;
  fireEvent.seeked(audio);
  await waitFor(() =>
    expect(keys.every((key) => queryClient.getQueryState(key)?.isInvalidated)).toBe(true),
  );
  expect(queryClient.getQueryState(unrelated)?.isInvalidated).toBe(false);
  expect(latest.currentTime).toBe(300);
  expect(mocks.bookProgress).not.toHaveBeenCalled();
});
it("terminalizes the last part with the full book position and does not replay its ended stream", async () => {
  const fetcher = server();
  const { container } = mount();
  await waitFor(() => expect(latest.streamUrl).toContain("/files/2/original"));
  fireEvent.ended(container.querySelector("audio")!);
  await waitFor(() =>
    expect(fetcher.mock.calls.some(([, options]) => options?.method === "DELETE")).toBe(true),
  );
  const stop = fetcher.mock.calls.find(([, options]) => options?.method === "DELETE")!;
  expect(JSON.parse(String(stop[1]?.body))).toMatchObject({
    timeline_id: manifest.timeline_id,
    position: 300,
  });
  expect(latest.currentTime).toBe(600);
  vi.mocked(HTMLMediaElement.prototype.play).mockClear();
  act(() => latest.togglePlay());
  expect(HTMLMediaElement.prototype.play).not.toHaveBeenCalled();
});

it("refuses an installation change between discovery and START without refreshing the manifest", async () => {
  const fetcher = server();
  const normal = fetcher.getMockImplementation()!;
  let probes = 0;
  fetcher.mockImplementation(async (input, options) => {
    if (String(input).endsWith("/capabilities") && ++probes > 1)
      return json({ ...cap, installation_id: "replacement" });
    return normal(input, options);
  });
  mount();
  await waitFor(() => expect(mocks.toast).toHaveBeenCalled());
  expect(latest.streamUrl).toBe("");
  expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/start"))).toHaveLength(0);
  expect(fetcher.mock.calls.filter(([url]) => String(url).includes("/timelines/"))).toHaveLength(1);
});
it("keeps an inconsistent playable START response unresolved without adopting or relabeling it", async () => {
  const fetcher = server();
  const normal = fetcher.getMockImplementation()!;
  let consistent = false;
  fetcher.mockImplementation(async (input, options) => {
    if (String(input).endsWith("/start")) {
      const result = decision("2", 30);
      if (!consistent) result.progress_timeline.part_offset_seconds = 200;
      return json(result);
    }
    return normal(input, options);
  });
  const { unmount } = mount();
  await waitFor(() => expect(mocks.toast).toHaveBeenCalled());
  expect(latest.streamUrl).toBe("");
  const key = Object.keys(localStorage).find((key) => key.startsWith("silo-playback-start-v1:"))!;
  expect(JSON.parse(localStorage.getItem(key)!)).toMatchObject({
    timeline_id: manifest.timeline_id,
  });
  expect(
    Object.keys(localStorage).some((key) => key.startsWith("silo-playback-mutation-v1:")),
  ).toBe(false);
  const original = localStorage.getItem(key);
  unmount();
  consistent = true;
  await act(async () => retryStart!());
  await waitFor(() => expect(localStorage.getItem(key)).toBeNull());
  const starts = fetcher.mock.calls.filter(([url]) => String(url).endsWith("/start"));
  expect(starts).toHaveLength(2);
  expect(starts[1]![1]?.body).toBe(original);
});
it("holds a fresh player intent behind the prior bound session's unknown terminal stop", async () => {
  const fetcher = server();
  const normal = fetcher.getMockImplementation()!;
  fetcher.mockImplementation(async (input, options) =>
    options?.method === "DELETE" ? json({ detail: "unknown stop" }, 409) : normal(input, options),
  );
  const previous = mount();
  await waitFor(() => expect(latest.streamUrl).toContain("/files/2/original"));
  previous.unmount();
  await waitFor(() => expect(retryStop).toBeTypeOf("function"));
  const old = Object.keys(localStorage).find((key) =>
    key.startsWith("silo-playback-mutation-v1:"),
  )!;
  const original = localStorage.getItem(old);
  mocks.toast.mockClear();
  mount(0);
  await waitFor(() => expect(mocks.toast).toHaveBeenCalled());
  expect(latest.streamUrl).toBe("");
  expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/start"))).toHaveLength(1);
  expect(localStorage.getItem(old)).toBe(original);
});

it("refuses an out-of-bounds chapter without clamping or starting another part", async () => {
  const fetcher = server();
  mount(0, { fileId: "2", positionSeconds: 300 });
  await waitFor(() => expect(mocks.toast).toHaveBeenCalled());
  expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/start"))).toHaveLength(0);
  expect(latest.streamUrl).toBe("");
});

it("resolves a retained timeline terminal without automatic discovery and refreshes only for a new explicit player intent", async () => {
  const fetcher = server();
  const normal = fetcher.getMockImplementation()!;
  let digest = manifest.timeline_id;
  fetcher.mockImplementation(async (input, options) => {
    if (String(input).includes("/timelines/")) return json({ ...manifest, timeline_id: digest });
    if (String(input).endsWith("/start"))
      return json(
        {
          protocol_version: 3,
          server_features: ["bound_client_timeline"],
          outcome: "adaptation_unavailable",
          terminal: {
            reason: "client_timeline_changed",
            retryable: false,
            message: "The audiobook changed. Start a new playback request.",
          },
        },
        201,
      );
    return normal(input, options);
  });
  const first = mount();
  await waitFor(() => expect(mocks.toast).toHaveBeenCalled());
  const starts = () => fetcher.mock.calls.filter(([url]) => String(url).endsWith("/start"));
  const discoveries = () =>
    fetcher.mock.calls.filter(([url]) => String(url).includes("/timelines/"));
  expect(starts()).toHaveLength(1);
  expect(discoveries()).toHaveLength(1);
  expect(localStorage.length).toBe(0);
  expect(latest.streamUrl).toBe("");
  expect(fetcher.mock.calls.some(([url]) => String(url).endsWith("/route-events"))).toBe(false);
  first.unmount();
  digest = "b".repeat(64);
  mount();
  await waitFor(() => expect(starts()).toHaveLength(2));
  expect(discoveries()).toHaveLength(2);
  const original = JSON.parse(String(starts()[0]![1]?.body));
  const fresh = JSON.parse(String(starts()[1]![1]?.body));
  expect(fresh.timeline_id).toBe(digest);
  expect(fresh.playback_attempt_id).not.toBe(original.playback_attempt_id);
});
