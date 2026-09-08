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

const timeline = {
  timeline_id: "a".repeat(64),
  media_item_id: "book",
  file_id: "42",
  part_offset_seconds: 600,
  part_duration_seconds: 300,
  duration_seconds: 900,
};
const boundBody = {
  ...body,
  progress_persistence: "client_bound" as const,
  timeline_id: timeline.timeline_id,
  start_position: 30,
  client_features: [...body.client_features, "bound_client_timeline"],
};
const timelineTerminal = {
  protocol_version: 3,
  server_features: ["bound_client_timeline"],
  outcome: "adaptation_unavailable",
  terminal: {
    reason: "client_timeline_changed",
    message: "The audiobook changed. Start a new playback request.",
    retryable: false,
  },
};
function boundTransport(response: () => Promise<Response>) {
  vi.stubGlobal("navigator", {
    locks: {
      request: (_key: string, _options: unknown, action: () => Promise<unknown>) => action(),
    },
  });
  const fetcher = vi.fn(async (url: string, _init?: RequestInit) =>
    url.endsWith("capabilities")
      ? reply({ ...cap, features: [...cap.features, "bound_client_timeline"] })
      : response(),
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
it("retires only the exact retained201 timeline terminal, then permits a new explicit attempt", async () => {
  let lost = true;
  const fetcher = boundTransport(async () => {
    if (lost) throw new TypeError("lost publication response");
    return reply(timelineTerminal, 201);
  });
  await expect(startInitialPlayback(config, boundBody, "installation", timeline)).rejects.toThrow(
    "lost publication",
  );
  const key = Object.keys(localStorage).find((key) => key.startsWith("silo-playback-start-v1:"))!;
  const original = localStorage.getItem(key);
  lost = false;
  vi.resetModules();
  const reloaded = await import("./initial-v2");
  expect(
    await reloaded.startInitialPlayback(config, boundBody, "installation", timeline),
  ).toMatchObject(timelineTerminal);
  const starts = () => fetcher.mock.calls.filter(([url]) => url.endsWith("/start"));
  expect(starts()).toHaveLength(2);
  expect(starts()[1]![1]?.body).toBe(original);
  expect(localStorage.length).toBe(0);
  const fresh = { ...timeline, timeline_id: "b".repeat(64) };
  await reloaded.startInitialPlayback(
    config,
    { ...boundBody, playback_attempt_id: "new-explicit-intent", timeline_id: fresh.timeline_id },
    "installation",
    fresh,
  );
  expect(starts()).toHaveLength(3);
  expect(JSON.parse(String(starts()[2]![1]?.body))).toMatchObject({
    playback_attempt_id: "new-explicit-intent",
    timeline_id: fresh.timeline_id,
  });
  expect(
    fetcher.mock.calls.every(([url]) => url.endsWith("/start") || url.endsWith("capabilities")),
  ).toBe(true);
});
it.each([
  [409, { code: "timeline_changed" }],
  [409, { detail: "conflict" }],
  [503, { detail: "publication unknown" }],
  [200, timelineTerminal],
  [201, { ...timelineTerminal, outcome: "playable" }],
  [201, { ...timelineTerminal, terminal: { ...timelineTerminal.terminal, retryable: true } }],
  [
    201,
    {
      ...timelineTerminal,
      terminal: { reason_code: "client_timeline_changed", retryable: false, message: "changed" },
    },
  ],
  [201, { ...timelineTerminal, session_id: "unexpected" }],
  [201, { ...timelineTerminal, playback_plan: null }],
])(
  "keeps nondefinitive or malformed bound refusal bytes unchanged: %j",
  async (status, response) => {
    const fetcher = boundTransport(async () => reply(response, status as number));
    await expect(
      startInitialPlayback(config, boundBody, "installation", timeline),
    ).rejects.toThrow();
    const key = Object.keys(localStorage).find((key) => key.startsWith("silo-playback-start-v1:"))!;
    const original = localStorage.getItem(key);
    expect(original).toBe(fetcher.mock.calls.find(([url]) => url.endsWith("/start"))![1]?.body);
    await expect(
      startInitialPlayback(
        config,
        { ...boundBody, playback_attempt_id: "replacement" },
        "installation",
        timeline,
      ),
    ).rejects.toThrow("earlier playback start");
    expect(localStorage.getItem(key)).toBe(original);
    expect(fetcher.mock.calls.filter(([url]) => url.endsWith("/start"))).toHaveLength(1);
    expect(
      Object.keys(localStorage).some((key) => key.startsWith("silo-playback-mutation-v1:")),
    ).toBe(false);
  },
);
it("does not retire a late201 refusal after the original authority changes", async () => {
  let current = true;
  const captured = {
    ...config,
    capturePlaybackMutationContext: () => ({
      ...config.capturePlaybackMutationContext!()!,
      isCurrent: () => current,
    }),
  };
  boundTransport(async () => {
    current = false;
    return reply(timelineTerminal, 201);
  });
  await expect(startInitialPlayback(captured, boundBody, "installation", timeline)).rejects.toThrow(
    "identity changed",
  );
  expect(Object.keys(localStorage).some((key) => key.startsWith("silo-playback-start-v1:"))).toBe(
    true,
  );
});

const ownerRecovery = {
  recovery_id: "recovery",
  playback_attempt_id: "attempt",
  session_id: "lost-session",
  state: "draining",
  reason: "owner_lost",
};
const ownerTerminal = {
  protocol_version: 3,
  server_features: ["sequenced_progress_v1"],
  outcome: "adaptation_unavailable",
  terminal: {
    reason: "playback_owner_lost",
    message: "Playback ended after its server owner was lost.",
    retryable: false,
  },
  recovery: { ...ownerRecovery, state: "aborted" },
};
it("retains exact lost START bytes through draining reload, then durably records abandonment", async () => {
  let phase = 0;
  const fetcher = boundTransport(async () => {
    if (phase++ === 0) throw new TypeError("lost response");
    return phase === 2
      ? reply({ outcome: "draining", recovery: ownerRecovery }, 202)
      : reply(ownerTerminal, 201);
  });
  await expect(startInitialPlayback(config, body)).rejects.toThrow("lost response");
  const key = Object.keys(localStorage).find((key) => key.startsWith("silo-playback-start-v1:"))!;
  const original = localStorage.getItem(key);
  await expect(startInitialPlayback(config, body)).rejects.toThrow("still draining");
  expect(localStorage.getItem(key)).toBe(original);
  vi.resetModules();
  const restored = await import("./initial-v2");
  expect(await restored.startInitialPlayback(config, body)).toEqual(ownerTerminal);
  const starts = fetcher.mock.calls.filter(([url]) => url.endsWith("/start"));
  expect(starts).toHaveLength(3);
  expect(starts.every((call) => call[1]?.body === original)).toBe(true);
  expect(localStorage.getItem(key)).toBeNull();
  const recoveryKey = Object.keys(localStorage).find((key) =>
    key.startsWith("silo-playback-start-recovery-v1:"),
  )!;
  expect(JSON.parse(localStorage.getItem(recoveryKey)!)).toMatchObject({
    originalStart: original,
    recovery: ownerTerminal.recovery,
  });
  expect(
    Object.keys(localStorage).some((key) => key.startsWith("silo-playback-mutation-v1:")),
  ).toBe(false);
});
it.each([
  [200, ownerTerminal],
  [
    202,
    {
      outcome: "draining",
      recovery: { ...ownerRecovery, accepted: { sequence: 1, position: 4, is_paused: false } },
    },
  ],
  [
    201,
    { ...ownerTerminal, recovery: { ...ownerTerminal.recovery, playback_attempt_id: "foreign" } },
  ],
  [201, { ...ownerTerminal, playback_plan: {} }],
  [201, { ...ownerTerminal, terminal: { ...ownerTerminal.terminal, retryable: true } }],
  [
    201,
    {
      ...ownerTerminal,
      recovery: {
        ...ownerTerminal.recovery,
        accepted: { sequence: 0, position: 4, is_paused: false },
      },
    },
  ],
])(
  "keeps the original START pending for invalid owner-loss receipt %j",
  async (status, receipt) => {
    boundTransport(async () => reply(receipt, status as number));
    await expect(startInitialPlayback(config, body)).rejects.toThrow();
    expect(Object.keys(localStorage).some((key) => key.startsWith("silo-playback-start-v1:"))).toBe(
      true,
    );
  },
);
it("refuses a changed recovery identity after observing the original pending START", async () => {
  let receipt: unknown = { outcome: "draining", recovery: ownerRecovery };
  let status = 202;
  boundTransport(async () => reply(receipt, status));
  await expect(startInitialPlayback(config, body)).rejects.toThrow("still draining");
  const snapshot = { ...localStorage };
  receipt = {
    ...ownerTerminal,
    recovery: { ...ownerTerminal.recovery, session_id: "foreign-session" },
  };
  status = 201;
  await expect(startInitialPlayback(config, body)).rejects.toThrow("identity changed");
  expect({ ...localStorage }).toEqual(snapshot);
});

it("retains the exact START when terminal abandonment storage fails", async () => {
  const fetcher = boundTransport(async () => reply(ownerTerminal, 201));
  const originalSet = Storage.prototype.setItem;
  const spy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (
    this: Storage,
    key,
    value,
  ) {
    if (key.startsWith("silo-playback-start-recovery-v1:")) throw new Error("storage full");
    return originalSet.call(this, key, value);
  });
  try {
    await expect(startInitialPlayback(config, body)).rejects.toThrow("storage full");
    const start = fetcher.mock.calls.find(([url]) => url.endsWith("/start"))!;
    const key = Object.keys(localStorage).find((key) => key.startsWith("silo-playback-start-v1:"))!;
    expect(localStorage.getItem(key)).toBe(start[1]?.body);
  } finally {
    spy.mockRestore();
  }
});

it("refuses a changed completed recovery after a crash before START journal cleanup", async () => {
  let receipt = ownerTerminal;
  boundTransport(async () => reply(receipt, 201));
  const originalRemove = Storage.prototype.removeItem;
  const spy = vi.spyOn(Storage.prototype, "removeItem").mockImplementation(function (
    this: Storage,
    key,
  ) {
    if (key.startsWith("silo-playback-start-v1:")) throw new Error("cleanup interrupted");
    return originalRemove.call(this, key);
  });
  try {
    await expect(startInitialPlayback(config, body)).rejects.toThrow("cleanup interrupted");
  } finally {
    spy.mockRestore();
  }
  const snapshot = { ...localStorage };
  receipt = { ...ownerTerminal, recovery: { ...ownerTerminal.recovery, recovery_id: "changed" } };
  await expect(startInitialPlayback(config, body)).rejects.toThrow("identity changed");
  expect({ ...localStorage }).toEqual(snapshot);
  receipt = ownerTerminal;
  await expect(startInitialPlayback(config, body)).resolves.toEqual(ownerTerminal);
});
