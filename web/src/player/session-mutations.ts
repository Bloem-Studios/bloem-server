import type { BoundAcceptedProgress, ProgressTimeline } from "./bound-client-timeline";
import {
  openDurableSession,
  durableProgress,
  durableStop,
  pendingDurableSessions,
  type DurableSession,
} from "./durable-session-mutations";
import type { PlayerConfig, PlaybackMutationContext } from "./context/PlayerConfigContext";
import { playerFetch, playerRequestHeaders, PlayerFetchError } from "./player-fetch";
import { randomUUID } from "@/lib/uuid";

type ProgressSample = { position: number; is_paused: boolean };

type Mutations = {
  durable?: DurableSession;
  readSample?: () => ProgressSample | null;
  latestSample?: ProgressSample;
  sequence: number;
  tail: Promise<unknown>;
  stopBody?: string;
  stopping?: Promise<BoundAcceptedProgress | void>;
  stopped?: boolean;
};
const sessions = new Map<string, Mutations>();

export function registerSessionMutations(sessionId: string, features: readonly string[]) {
  if (features.includes("sequenced_progress_v1") && !sessions.has(sessionId)) {
    sessions.set(sessionId, { sequence: 0, tail: Promise.resolve() });
  }
}
export async function registerDurableSessionMutations(
  config: PlayerConfig,
  sessionId: string,
  installationId: string,
  timeline?: Readonly<ProgressTimeline>,
  capturedContext?: PlaybackMutationContext,
) {
  const durable = await openDurableSession(
    config,
    sessionId,
    installationId,
    timeline,
    capturedContext,
  );
  registerSessionMutations(sessionId, ["sequenced_progress_v1"]);
  const existing = sessions.get(sessionId)!;
  if (existing.durable && existing.durable.key !== durable.key)
    throw new Error("Playback session identity conflicts with its saved retry state");
  existing.durable = durable;
}
export function offerPendingPlaybackStops(
  config: PlayerConfig,
  installationId: string,
  includeRegistered = false,
) {
  for (const durable of pendingDurableSessions(config, installationId)) {
    const id = durable.identity.sessionId;
    const existing = sessions.get(id);
    if (
      !includeRegistered &&
      existing &&
      (!existing.durable || existing.durable.context.isCurrent())
    )
      continue;
    if (existing?.durable && existing.durable.key !== durable.key) continue;
    registerSessionMutations(id, ["sequenced_progress_v1"]);
    sessions.get(id)!.durable = durable;
    config.onPlaybackStopError?.(
      id,
      new Error("A previous playback session still needs confirmation."),
      () => {
        void retryDurableStop(config, durable, undefined, false).catch(() => {});
      },
    );
  }
}

async function retryDurableStop(
  config: PlayerConfig,
  durable: DurableSession,
  sample: ProgressSample | undefined,
  keepalive: boolean,
): Promise<BoundAcceptedProgress | void> {
  try {
    return await durableStop(config, durable, sample, keepalive);
  } catch (error) {
    config.onPlaybackStopError?.(
      durable.identity.sessionId,
      error instanceof Error ? error : new Error("Playback stop not confirmed"),
      () => {
        void retryDurableStop(config, durable, sample, keepalive).catch(() => {});
      },
    );
    throw error;
  }
}

export function hasSequencedProgress(sessionId: string) {
  return sessions.has(sessionId);
}

/** The durable v2 binding of a session started through the initial flow, if any. */
export function durableSessionFor(sessionId: string): DurableSession | undefined {
  return sessions.get(sessionId)?.durable;
}

/** The installation a durable (v2 initial-flow) session was opened with, if any. */
export function sessionInstallation(sessionId: string): string | undefined {
  return sessions.get(sessionId)?.durable?.identity.installationId;
}

export function observeSessionProgress(sessionId: string, reader: () => ProgressSample | null) {
  const state = sessions.get(sessionId);
  if (!state) return () => {};
  state.readSample = reader;
  return () => {
    if (state.readSample === reader) state.readSample = undefined;
  };
}

export function captureSessionProgress(sessionId: string, sample: ProgressSample | null) {
  const state = sessions.get(sessionId);
  if (state && sample && !state.stopBody) state.latestSample = sample;
}

const pause = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

async function waitForProgress(tail: Promise<unknown>) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    await Promise.race([
      tail.catch(() => {}),
      new Promise<void>((resolve) => {
        timer = setTimeout(resolve, 30_000);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

async function mutationFetch(
  config: PlayerConfig,
  path: string,
  method: string,
  body: string,
  keepalive: boolean,
  timeout: number,
) {
  const response = await fetch(`${config.apiBaseUrl}${path}`, {
    method,
    body,
    keepalive,
    headers: playerRequestHeaders(config, undefined, true),
    signal: AbortSignal.timeout(timeout),
  });
  if (!response.ok) {
    throw new PlayerFetchError(response.status, `Playback update failed (${response.status})`);
  }
  return response;
}

export function sendSessionProgress(
  config: PlayerConfig,
  sessionId: string,
  sample: { position: number; is_paused: boolean },
  keepalive = false,
): Promise<BoundAcceptedProgress | void> {
  const state = sessions.get(sessionId);
  if (!state)
    return playerFetch(config, `/playback/${sessionId}/progress`, {
      method: "POST",
      body: JSON.stringify(sample),
      keepalive,
    });
  if (state.durable) {
    state.latestSample = sample;
    return durableProgress(config, state.durable, sample, keepalive);
  }
  if (state.stopBody) return Promise.resolve();
  if (!Number.isSafeInteger(state.sequence + 1))
    return Promise.reject(new Error("Playback progress sequence exhausted"));
  state.latestSample = sample;
  const body = JSON.stringify({ ...sample, sequence: ++state.sequence });
  const pending = state.tail
    .catch(() => {})
    .then(async () => {
      for (let attempt = 0; ; attempt++) {
        try {
          await mutationFetch(
            config,
            `/playback/${sessionId}/progress`,
            "POST",
            body,
            keepalive,
            5000,
          );
          return;
        } catch (error) {
          if (attempt >= 2 || (error instanceof PlayerFetchError && error.status < 500))
            throw error;
          await pause(250);
        }
      }
    });
  state.tail = pending;
  return pending;
}

// A stop captures one immutable identity and waits for prior progress requests.
// HTTP 202 means draining, never completion. A later caller retries the same ID.
export function stopSequencedSession(
  config: PlayerConfig,
  sessionId: string,
  keepalive = false,
): Promise<BoundAcceptedProgress | void> {
  const state = sessions.get(sessionId);
  if (!state) return playerFetch(config, `/playback/${sessionId}`, { method: "DELETE", keepalive });
  if (state.durable) {
    if (state.stopping) return state.stopping;
    const stopping = retryDurableStop(
      config,
      state.durable,
      state.readSample?.() ?? state.latestSample,
      keepalive,
    );
    state.stopping = stopping;

    void stopping
      .finally(() => {
        state.stopping = undefined;
      })
      .catch(() => {});
    return stopping;
  }
  if (state.stopped) return Promise.resolve();
  if (state.stopping) return state.stopping;
  if (!state.stopBody) {
    const sample = state.readSample?.() ?? state.latestSample;
    if (sample && !Number.isSafeInteger(state.sequence + 1))
      return Promise.reject(new Error("Playback progress sequence exhausted"));
    state.stopBody = JSON.stringify({
      stop_id: randomUUID(),
      ...(sample ? { ...sample, sequence: ++state.sequence } : {}),
    });
  }
  const body = state.stopBody;
  const deadline = Date.now() + 30_000;
  const stopping = waitForProgress(state.tail).then(async () => {
    while (Date.now() < deadline) {
      try {
        const response = await mutationFetch(
          config,
          `/playback/${sessionId}`,
          "DELETE",
          body,
          keepalive,
          Math.max(1, Math.min(5000, deadline - Date.now())),
        );
        const receipt = (await response.json()) as { outcome?: string; stop_id?: string };
        if (receipt.stop_id !== undefined && receipt.stop_id !== JSON.parse(body).stop_id)
          throw new Error("Playback stop returned another receipt");
        if (
          response.status === 200 &&
          (receipt.outcome === "stopped" || receipt.outcome === "replayed")
        ) {
          state.stopped = true;
          return;
        }
        if (response.status !== 202 || receipt.outcome !== "draining")
          throw new Error(`Unexpected playback stop status (${response.status})`);
      } catch (error) {
        if (error instanceof PlayerFetchError && error.status < 500) throw error;
        if (
          !(
            error instanceof PlayerFetchError ||
            error instanceof TypeError ||
            error instanceof DOMException
          )
        )
          throw error;
      }
      await pause(500);
    }
    throw new Error("Playback stop is still pending. Please retry.");
  });
  state.stopping = stopping;
  void stopping.catch((error) => {
    const failure = error instanceof Error ? error : new Error("Playback stop not confirmed");
    config.onPlaybackStopError?.(sessionId, failure, () => {
      void stopSequencedSession(config, sessionId, keepalive).catch(() => {});
    });
  });
  void stopping
    .finally(() => {
      state.stopping = undefined;
    })
    .catch(() => {});
  return stopping;
}
