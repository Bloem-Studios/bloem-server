import { afterEach, describe, expect, it, vi } from "vitest";
import {
  registerSessionMutations,
  observeSessionProgress,
  captureSessionProgress,
  sendSessionProgress,
  stopSequencedSession,
} from "./session-mutations";
import type { PlayerConfig } from "./context/PlayerConfigContext";

const config: PlayerConfig = {
  apiBaseUrl: "/api/v1",
  getAccessToken: () => "token",
  getProfileId: () => "profile",
  getDeviceId: () => "device",
};
const sample = { position: 30, is_paused: false };
const response = (status = 200) =>
  new Response(
    status === 204 ? null : JSON.stringify({ outcome: status === 202 ? "draining" : "stopped" }),
    { status },
  );
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("sequenced playback mutations", () => {
  it("allocates once per sample and preserves the body after a lost reply", async () => {
    vi.useFakeTimers();
    const fetcher = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("lost reply"))
      .mockImplementation(async () => response());
    vi.stubGlobal("fetch", fetcher);
    registerSessionMutations("progress-retry", ["sequenced_progress_v1"]);
    const first = sendSessionProgress(config, "progress-retry", sample);
    await vi.advanceTimersByTimeAsync(250);
    await first;
    await sendSessionProgress(config, "progress-retry", { ...sample, position: 10 });
    const bodies = fetcher.mock.calls.map((call) => call[1].body);
    expect(bodies[0]).toBe(bodies[1]);
    expect(JSON.parse(bodies[0]).sequence).toBe(1);
    expect(JSON.parse(bodies[2])).toEqual({ ...sample, position: 10, sequence: 2 });
  });

  it("does not treat 202 as completed and retries the exact stop identity", async () => {
    vi.useFakeTimers();
    const fetcher = vi.fn().mockResolvedValueOnce(response(202)).mockResolvedValueOnce(response());
    vi.stubGlobal("fetch", fetcher);
    registerSessionMutations("stop-pending", ["sequenced_progress_v1"]);
    let completed = false;
    const stop = stopSequencedSession(config, "stop-pending").then(() => {
      completed = true;
    });
    await vi.advanceTimersByTimeAsync(0);
    expect(completed).toBe(false);
    await vi.advanceTimersByTimeAsync(500);
    await stop;
    expect(completed).toBe(true);
    expect(fetcher.mock.calls[0]![1].body).toBe(fetcher.mock.calls[1]![1].body);
    expect(JSON.parse(fetcher.mock.calls[0]![1].body).stop_id).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("surfaces bounded pending failure and retains the stop ID for retry", async () => {
    vi.useFakeTimers();
    const fetcher = vi.fn().mockImplementation(async () => response(202));
    vi.stubGlobal("fetch", fetcher);
    registerSessionMutations("stop-timeout", ["sequenced_progress_v1"]);
    const stop = stopSequencedSession(config, "stop-timeout").catch((error) => error);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(await stop).toBeInstanceOf(Error);
    const original = fetcher.mock.calls[0]![1].body;
    fetcher.mockImplementation(async () => response());
    await stopSequencedSession(config, "stop-timeout");
    expect(fetcher.mock.calls[fetcher.mock.calls.length - 1]![1].body).toBe(original);
  });

  it("orders a stop after accepted progress and suppresses later progress", async () => {
    let accept!: (value: Response) => void;
    const fetcher = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            accept = resolve;
          }),
      )
      .mockImplementation(async () => response());
    vi.stubGlobal("fetch", fetcher);
    registerSessionMutations("stop-order", ["sequenced_progress_v1"]);
    const progress = sendSessionProgress(config, "stop-order", sample);
    const stop = stopSequencedSession(config, "stop-order");
    await Promise.resolve();
    await Promise.resolve();
    expect(fetcher).toHaveBeenCalledTimes(1);
    accept(response());
    await progress;
    await stop;
    await sendSessionProgress(config, "stop-order", { ...sample, position: 40 });
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(fetcher.mock.calls[1]![1].method).toBe("DELETE");
  });

  it("keeps legacy unsequenced payloads", async () => {
    const fetcher = vi.fn().mockImplementation(async () => response(204));
    vi.stubGlobal("fetch", fetcher);
    await sendSessionProgress(config, "legacy-mutations", sample);
    await stopSequencedSession(config, "legacy-mutations");
    expect(JSON.parse(fetcher.mock.calls[0]![1].body)).toEqual(sample);
    expect(fetcher.mock.calls[1]![1].body).toBeUndefined();
  });
});

it("reports an unconfirmed terminal response through the host callback", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValue(new Response(JSON.stringify({ outcome: "draining" }), { status: 200 }));
  vi.stubGlobal("fetch", fetcher);
  const onPlaybackStopError = vi.fn();
  registerSessionMutations("invalid-terminal", ["sequenced_progress_v1"]);
  await expect(
    stopSequencedSession({ ...config, onPlaybackStopError }, "invalid-terminal"),
  ).rejects.toThrow("Unexpected playback stop status");
  expect(onPlaybackStopError).toHaveBeenCalledWith(
    "invalid-terminal",
    expect.any(Error),
    expect.any(Function),
  );
});

it("captures the final snapshot once and keeps it stable during drain retries", async () => {
  vi.useFakeTimers();
  const fetcher = vi.fn().mockResolvedValueOnce(response(202)).mockResolvedValueOnce(response());
  vi.stubGlobal("fetch", fetcher);
  registerSessionMutations("final-snapshot", ["sequenced_progress_v1"]);
  captureSessionProgress("final-snapshot", { position: 20, is_paused: false });
  let current = { position: 47, is_paused: true };
  const forget = observeSessionProgress("final-snapshot", () => current);
  const stop = stopSequencedSession(config, "final-snapshot");
  await vi.advanceTimersByTimeAsync(0);
  current = { position: 99, is_paused: false };
  forget();
  await vi.advanceTimersByTimeAsync(500);
  await stop;
  const first = fetcher.mock.calls[0]![1].body;
  expect(JSON.parse(first)).toMatchObject({ position: 47, is_paused: true, sequence: 1 });
  expect(fetcher.mock.calls[1]![1].body).toBe(first);
});
