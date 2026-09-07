import { beforeEach, afterEach, expect, it, vi } from "vitest";
import {
  openDurableSession,
  durableProgress,
  durableStop,
  pendingDurableSessions,
  recordDurableTermination,
  hasDurableTermination,
} from "./durable-session-mutations";
import type { PlayerConfig } from "./context/PlayerConfigContext";
let current = true;
const config: PlayerConfig = {
  apiBaseUrl: "/api/v1",
  getAccessToken: () => "token",
  getProfileId: () => "profile",
  getDeviceId: () => "device",
  capturePlaybackMutationContext: () => ({
    accountId: "account",
    profileId: "profile",
    origin: "https://server.example",
    isCurrent: () => current,
  }),
};
const sample = { position: 31, is_paused: false };
const reply = (status = 200) =>
  new Response(JSON.stringify({ outcome: status === 202 ? "draining" : "stopped" }), { status });
beforeEach(() => {
  localStorage.clear();
  current = true;
  let tail: Promise<unknown> = Promise.resolve();
  vi.stubGlobal("navigator", {
    locks: {
      request: (_name: string, _opts: unknown, action: () => Promise<unknown>) => {
        const next = tail.catch(() => {}).then(action);
        tail = next;
        return next;
      },
    },
  });
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it("persists an uncertain progress request and replays it before allocating after reload", async () => {
  vi.useFakeTimers();
  const first = await openDurableSession(config, "session", "installation");
  const fetcher = vi.fn().mockRejectedValue(new TypeError("lost reply"));
  vi.stubGlobal("fetch", fetcher);
  const failed = durableProgress(config, first, sample, false).catch((error) => error);
  await vi.advanceTimersByTimeAsync(500);
  expect(await failed).toBeInstanceOf(TypeError);
  const original = fetcher.mock.calls[0]![1].body;
  expect(JSON.parse(localStorage.getItem(first.key)!).pendingProgress).toBe(original);
  vi.resetModules();
  const reloaded = await import("./durable-session-mutations");
  const restored = await reloaded.openDurableSession(config, "session", "installation");
  fetcher.mockClear();
  fetcher.mockImplementation(async () => reply());
  await reloaded.durableProgress(config, restored, { ...sample, position: 12 }, false);
  expect(fetcher.mock.calls[0]![1].body).toBe(original);
  expect(JSON.parse(fetcher.mock.calls[1]![1].body)).toMatchObject({
    sequence: 2,
    position: 12,
    installation_id: "installation",
  });
});
it("retains the exact stop identity through draining timeout and a new page handle", async () => {
  vi.useFakeTimers();
  const binding = await openDurableSession(config, "stop", "installation");
  const fetcher = vi.fn().mockImplementation(async () => reply(202));
  vi.stubGlobal("fetch", fetcher);
  const stop = durableStop(config, binding, sample, false).catch((e) => e);
  await vi.advanceTimersByTimeAsync(30000);
  expect(await stop).toBeInstanceOf(Error);
  const original = fetcher.mock.calls[0]![1].body;
  const restored = await openDurableSession(config, "stop", "installation");
  fetcher.mockImplementation(async () => reply());
  await durableStop(config, restored, { position: 999, is_paused: true }, false);
  expect(fetcher.mock.lastCall![1].body).toBe(original);
  expect(pendingDurableSessions(config, "installation")).toEqual([]);
});
it("serializes separate page handles before allocating sequences", async () => {
  const one = await openDurableSession(config, "tabs", "installation");
  const two = await openDurableSession(config, "tabs", "installation");
  const fetcher = vi.fn().mockImplementation(async () => reply());
  vi.stubGlobal("fetch", fetcher);
  await Promise.all([
    durableProgress(config, one, sample, false),
    durableProgress(config, two, sample, false),
  ]);
  expect(fetcher.mock.calls.map((c) => JSON.parse(c[1].body).sequence)).toEqual([1, 2]);
});
it("refuses switched authority and restores only exact ownership", async () => {
  const binding = await openDurableSession(config, "identity", "installation");
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  current = false;
  await expect(durableStop(config, binding, sample, false)).rejects.toThrow("another account");
  expect(fetcher).not.toHaveBeenCalled();
  current = true;
  expect(pendingDurableSessions(config, "other-installation")).toEqual([]);
  const other = {
    ...config,
    capturePlaybackMutationContext: () => ({
      ...config.capturePlaybackMutationContext!()!,
      accountId: "other",
    }),
  };
  expect(pendingDurableSessions(other, "installation")).toEqual([]);
  expect(pendingDurableSessions(config, "installation")).toHaveLength(1);
});
it("fails before network effects when saved state is corrupt or missing", async () => {
  const binding = await openDurableSession(config, "corrupt", "installation");
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  localStorage.setItem(binding.key, "{}");
  await expect(durableProgress(config, binding, sample, false)).rejects.toThrow("invalid");
  expect(fetcher).not.toHaveBeenCalled();
  expect(localStorage.getItem(binding.key)).toBe("{}");
});
it("does not send an allocated request when persistent storage fails", async () => {
  const binding = await openDurableSession(config, "quota", "installation");
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const write = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new Error("quota exhausted");
  });
  try {
    await expect(durableProgress(config, binding, sample, false)).rejects.toThrow(
      "quota exhausted",
    );
    expect(fetcher).not.toHaveBeenCalled();
    expect(JSON.parse(localStorage.getItem(binding.key)!).sequence).toBe(0);
  } finally {
    write.mockRestore();
  }
});
it("persists both sequence and stop identity before their first request", async () => {
  const binding = await openDurableSession(config, "before-send", "installation");
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation(async (_url, init) => {
      const saved = JSON.parse(localStorage.getItem(binding.key)!);
      expect(init.body).toBe(init.method === "DELETE" ? saved.stopBody : saved.pendingProgress);
      expect(JSON.parse(init.body).sequence).toBe(saved.sequence);
      return reply();
    }),
  );
  await durableProgress(config, binding, sample, false);
  await durableStop(config, binding, sample, false);
});

it.each(["progress", "stop"])(
  "halts %s retry and queued cleanup after authoritative termination without rewriting uncertainty",
  async (operation) => {
    vi.useFakeTimers();
    const binding = await openDurableSession(config, `terminated-${operation}`, "installation");
    let fail!: (reason: Error) => void;
    const fetcher = vi.fn(
      () =>
        new Promise<Response>((_resolve, reject) => {
          fail = reject;
        }),
    );
    vi.stubGlobal("fetch", fetcher);
    const inFlight =
      operation === "progress"
        ? durableProgress(config, binding, sample, false)
        : durableStop(config, binding, sample, false);
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
    const exactJournal = localStorage.getItem(binding.key);
    recordDurableTermination(config, binding, "admin-command");
    fail(new TypeError("lost in-flight reply"));
    await inFlight;
    await durableProgress(config, binding, { position: 999, is_paused: false }, true);
    await durableStop(config, binding, sample, true);
    await vi.advanceTimersByTimeAsync(31000);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(localStorage.getItem(binding.key)).toBe(exactJournal);
    expect(JSON.parse(exactJournal!).stopped).toBe(false);
    expect(pendingDurableSessions(config, "installation")).toEqual([]);
    vi.resetModules();
    const restored = await import("./durable-session-mutations");
    const reopened = await restored.openDurableSession(
      config,
      binding.identity.sessionId,
      "installation",
    );
    expect(restored.hasDurableTermination(reopened)).toBe(true);
    await restored.durableStop(config, reopened, sample, true);
    expect(fetcher).toHaveBeenCalledTimes(1);
  },
);
it("does not infer terminal authority from an ambiguous404", async () => {
  const binding = await openDurableSession(config, "ambiguous-terminal", "installation");
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(null, { status: 404 })),
  );
  await expect(durableStop(config, binding, sample, false)).rejects.toThrow("404");
  expect(hasDurableTermination(binding)).toBe(false);
  expect(pendingDurableSessions(config, "installation")).toHaveLength(1);
});
it("rejects a terminal signal after the captured account changed", async () => {
  const binding = await openDurableSession(config, "stale-terminal", "installation");
  current = false;
  expect(() => recordDurableTermination(config, binding, "old-command")).toThrow("another account");
  expect(hasDurableTermination(binding)).toBe(false);
});
