import { afterEach, expect, it, vi } from "vitest";
import { initialPlaybackCapabilities, startInitialPlayback } from "./initial-v2";
import type { PlayerConfig } from "./context/PlayerConfigContext";
import { buildStartRequestV3 } from "./playback-session-wire-v3";
import {
  fixtureClientCapabilitiesV3,
  fixtureClientPlaybackContextV3,
  fixturePlanV3,
} from "./protocol-v3.fixtures";
const config: PlayerConfig = {
  apiBaseUrl: "/api/v1",
  getAccessToken: () => "token",
  getProfileId: () => "profile",
  getDeviceId: () => "device",
  capturePlaybackMutationContext: () => ({
    accountId: "account",
    profileId: "profile",
    origin: "http://localhost:3000",
    isCurrent: () => true,
  }),
};
const body = buildStartRequestV3({
  fileId: 42,
  profileId: "profile",
  playbackAttemptId: "attempt",
  qualityPreference: "auto",
  position: 0,
  forceStartPosition: false,
  metered: false,
  clientCapabilities: fixtureClientCapabilitiesV3(),
  clientPlaybackContext: fixtureClientPlaybackContextV3(),
});
const cap = {
  installation_id: "installation",
  revision: "1",
  state: "available",
  allowed: true,
  protocol_versions: [3],
  features: ["sequenced_progress_v1"],
  deliveries: ["direct"],
};
const reply = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});
it("refuses an unconfigured server without dispatching a start", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValue(
        reply({ state: "not_configured", allowed: false, protocol_versions: [], features: [] }),
      ),
  );
  await expect(startInitialPlayback(config, body)).rejects.toThrow("not configured");
  expect(fetch).toHaveBeenCalledTimes(1);
});
it("does not fall back on admission refusal or transient capability failure", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValue(reply({ ...cap, state: "not_admitted", allowed: false }));
  vi.stubGlobal("fetch", fetcher);
  await expect(startInitialPlayback(config, body)).rejects.toThrow("not admitted");
  expect(fetcher).toHaveBeenCalledTimes(1);
  fetcher.mockResolvedValue(reply({}, 503));
  await expect(initialPlaybackCapabilities(config)).rejects.toThrow("unavailable");
});
it("uses v2 installation binding and converts string media IDs for the player", async () => {
  vi.stubGlobal("navigator", {
    locks: {
      request: (_key: string, _options: unknown, action: () => Promise<unknown>) => action(),
    },
  });
  const plan = fixturePlanV3();
  const wire = {
    ...plan,
    requested_media_file_id: "42",
    effective_media_file_id: "42",
    source: { ...plan.source, media_file_id: "42" },
  };
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(reply(cap))
    .mockResolvedValueOnce(
      reply({
        protocol_version: 3,
        server_features: ["sequenced_progress_v1"],
        session_id: "v2-start",
        outcome: "play",
        playback_plan: wire,
      }),
    );
  vi.stubGlobal("fetch", fetcher);
  const result = await startInitialPlayback(config, body);
  expect(fetcher.mock.calls[0]![0]).toBe("/api/v2/playback/capabilities");
  expect(fetcher.mock.calls[1]![0]).toBe("http://localhost:3000/api/v2/playback/start");
  expect(JSON.parse(fetcher.mock.calls[1]![1].body)).toMatchObject({
    installation_id: "installation",
    file_id: "42",
  });
  expect(result!.playback_plan!.source.media_file_id).toBe(42);
  expect(localStorage.length).toBe(1);
});
it("refuses configured start before effects when browser locking is unavailable", async () => {
  vi.stubGlobal("navigator", {});
  const fetcher = vi.fn().mockResolvedValue(reply(cap));
  vi.stubGlobal("fetch", fetcher);
  await expect(startInitialPlayback(config, body)).rejects.toThrow("storage locking");
  expect(fetcher).toHaveBeenCalledTimes(1);
});
it("keeps an uncertain start body through reload and blocks a newly minted attempt", async () => {
  vi.stubGlobal("navigator", {
    locks: {
      request: (_key: string, _options: unknown, action: () => Promise<unknown>) => action(),
    },
  });
  const fetcher = vi.fn().mockImplementation(async (url: string) => {
    if (url.endsWith("capabilities")) return reply(cap);
    throw new TypeError("lost successful reply");
  });
  vi.stubGlobal("fetch", fetcher);
  await expect(startInitialPlayback(config, body)).rejects.toThrow("lost successful reply");
  const original = fetcher.mock.calls.find((c) => c[0].endsWith("/start"))![1].body;
  expect(localStorage.getItem(localStorage.key(0)!)).toBe(original);
  await expect(
    startInitialPlayback(config, { ...body, playback_attempt_id: "different-attempt" }),
  ).rejects.toThrow("earlier playback start");
  expect(fetcher.mock.calls.filter((c) => c[0].endsWith("/start"))).toHaveLength(1);
  vi.resetModules();
  const reloaded = await import("./initial-v2");
  fetcher.mockImplementation(async (url: string) =>
    url.endsWith("capabilities")
      ? reply(cap)
      : reply({
          protocol_version: 3,
          server_features: [],
          outcome: "terminal",
          terminal: { code: "unavailable" },
        }),
  );
  await reloaded.startInitialPlayback(config, body);
  expect(fetcher.mock.calls.filter((c) => c[0].endsWith("/start"))[1]![1].body).toBe(original);
  expect(localStorage.length).toBe(0);
});
it("quarantines an old start recovery action after identity changes", async () => {
  let current = true;
  let retry: (() => void) | undefined;
  const scoped = {
    ...config,
    capturePlaybackMutationContext: () => ({
      ...config.capturePlaybackMutationContext!()!,
      isCurrent: () => current,
    }),
    onPlaybackStartError: (_error: Error, action: () => void) => {
      retry = action;
    },
  };
  vi.stubGlobal("navigator", {
    locks: {
      request: (_key: string, _options: unknown, action: () => Promise<unknown>) => action(),
    },
  });
  const fetcher = vi.fn().mockImplementation(async (url: string) => {
    if (url.endsWith("capabilities")) return reply(cap);
    throw new TypeError("lost");
  });
  vi.stubGlobal("fetch", fetcher);
  await expect(startInitialPlayback(scoped, body)).rejects.toThrow("lost");
  expect(retry).toBeTypeOf("function");
  current = false;
  fetcher.mockClear();
  retry!();
  await Promise.resolve();
  await Promise.resolve();
  expect(fetcher).not.toHaveBeenCalled();
  expect(localStorage.length).toBe(1);
});
it("does not fall back to legacy while an earlier start remains uncertain", async () => {
  vi.stubGlobal("navigator", {
    locks: {
      request: (_key: string, _options: unknown, action: () => Promise<unknown>) => action(),
    },
  });
  const fetcher = vi.fn().mockImplementation(async (url: string) => {
    if (url.endsWith("capabilities")) return reply(cap);
    throw new TypeError("lost");
  });
  vi.stubGlobal("fetch", fetcher);
  await expect(startInitialPlayback(config, body)).rejects.toThrow("lost");
  fetcher.mockImplementation(async () =>
    reply({ state: "not_configured", allowed: false, protocol_versions: [], features: [] }),
  );
  await expect(startInitialPlayback(config, body)).rejects.toThrow(
    "API v2 playback is not configured",
  );
  expect(localStorage.length).toBe(1);
});
it("retains the same attempt after a lost start reply followed by validation_failed", async () => {
  vi.stubGlobal("navigator", {
    locks: {
      request: (_key: string, _options: unknown, action: () => Promise<unknown>) => action(),
    },
  });
  let starts = 0;
  const fetcher = vi.fn(async (url: string, _options?: RequestInit) => {
    if (url.endsWith("capabilities")) return reply(cap);
    starts++;
    if (starts === 1) throw new Error("lost successful reply");
    if (starts === 2) {
      return reply(
        { type: "https://siloserver.org/docs/api/v2/problems/validation_failed", status: 422 },
        422,
      );
    }
    return reply({
      protocol_version: 3,
      server_features: [],
      outcome: "adaptation_unavailable",
      terminal: { code: "unavailable" },
    });
  });
  vi.stubGlobal("fetch", fetcher);
  await expect(startInitialPlayback(config, body)).rejects.toThrow("lost successful reply");
  const stored = localStorage.getItem(localStorage.key(0)!);
  await expect(startInitialPlayback(config, body)).rejects.toThrow("Failed to start playback");
  expect(localStorage.getItem(localStorage.key(0)!)).toBe(stored);
  await expect(
    startInitialPlayback(config, { ...body, playback_attempt_id: "different-attempt" }),
  ).rejects.toThrow("earlier playback start");
  expect(starts).toBe(2);
  await startInitialPlayback(config, body);
  expect(localStorage.length).toBe(0);
  const requests = fetcher.mock.calls.filter((c) => c[0].endsWith("/start"));
  expect(requests).toHaveLength(3);
  for (const request of requests) expect((request[1] as RequestInit).body).toBe(stored);
});
it.each([
  {
    status: 422,
    problem: { type: "https://siloserver.org/docs/api/v2/problems/validation_failed", status: 422 },
  },
  {
    status: 422,
    problem: { type: "https://siloserver.org/docs/api/v2/problems/other", status: 422 },
  },
  {
    status: 503,
    problem: { type: "https://siloserver.org/docs/api/v2/problems/validation_failed", status: 503 },
  },
  { status: 422, problem: {} },
])(
  "retains the start journal for uncertain rejection $status $problem.type",
  async ({ status, problem }) => {
    vi.stubGlobal("navigator", {
      locks: {
        request: (_key: string, _options: unknown, action: () => Promise<unknown>) => action(),
      },
    });
    const fetcher = vi
      .fn()
      .mockImplementation(async (url: string) =>
        url.endsWith("capabilities") ? reply(cap) : reply(problem, status),
      );
    vi.stubGlobal("fetch", fetcher);
    await expect(startInitialPlayback(config, body)).rejects.toThrow("Failed to start playback");
    expect(localStorage.length).toBe(1);
    await expect(
      startInitialPlayback(config, { ...body, playback_attempt_id: "new-attempt" }),
    ).rejects.toThrow("earlier playback start");
    expect(fetcher.mock.calls.filter((c) => c[0].endsWith("/start"))).toHaveLength(1);
  },
);

it("requires v2 capabilities and captured identity before start", async () => {
  const fetcher = vi.fn().mockResolvedValue(reply({}, 404));
  vi.stubGlobal("fetch", fetcher);
  await expect(startInitialPlayback(config, body)).rejects.toThrow(
    "API v2 playback is unavailable",
  );
  expect(fetcher).toHaveBeenCalledTimes(1);
  fetcher.mockClear();
  await expect(
    startInitialPlayback({ ...config, capturePlaybackMutationContext: undefined }, body),
  ).rejects.toThrow("identity unavailable");
  expect(fetcher).not.toHaveBeenCalled();
});
