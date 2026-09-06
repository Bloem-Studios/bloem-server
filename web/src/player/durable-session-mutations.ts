import type { components } from "@/api/v2/schema";
import type { PlayerConfig, PlaybackMutationContext } from "./context/PlayerConfigContext";
import { playerRequestHeaders, PlayerFetchError } from "./player-fetch";
import { randomUUID } from "@/lib/uuid";

const PREFIX = "silo-playback-mutation-v1:";
type Sample = { position: number; is_paused: boolean };
type Identity = {
  installationId: string;
  accountId: string;
  profileId: string;
  origin: string;
  sessionId: string;
};
type RecordV1 = {
  version: 1;
  identity: Identity;
  sequence: number;
  pendingProgress?: string;
  stopBody?: string;
  stopped: boolean;
};
export type DurableSession = { key: string; identity: Identity; context: PlaybackMutationContext };

function assertCurrent(config: PlayerConfig, binding: DurableSession) {
  const current = config.capturePlaybackMutationContext?.();
  if (
    !binding.context.isCurrent() ||
    !current ||
    current.accountId !== binding.identity.accountId ||
    current.profileId !== binding.identity.profileId ||
    current.origin !== binding.identity.origin ||
    !current.isCurrent()
  )
    throw new Error(
      "Playback retry belongs to another account, profile, or server. Return to its original identity to retry.",
    );
}
function read(binding: DurableSession): RecordV1 {
  const raw = localStorage.getItem(binding.key);
  if (!raw || raw.length > 65536) throw new Error("Playback retry state is missing or invalid");
  const record = JSON.parse(raw) as RecordV1;
  if (
    record.version !== 1 ||
    JSON.stringify(record.identity) !== JSON.stringify(binding.identity) ||
    !Number.isSafeInteger(record.sequence) ||
    record.sequence < 0 ||
    typeof record.stopped !== "boolean"
  )
    throw new Error("Playback retry state is invalid");
  for (const body of [record.pendingProgress, record.stopBody])
    if (body !== undefined) {
      if (typeof body !== "string") throw new Error("Playback retry payload is invalid");
      const payload = JSON.parse(body) as {
        installation_id?: string;
        sequence?: number;
        stop_id?: string;
      };
      if (
        payload.installation_id !== binding.identity.installationId ||
        (payload.sequence !== undefined &&
          (!Number.isSafeInteger(payload.sequence) ||
            payload.sequence < 1 ||
            payload.sequence > record.sequence))
      )
        throw new Error("Playback retry payload identity is invalid");
    }
  if (record.stopBody && typeof JSON.parse(record.stopBody).stop_id !== "string")
    throw new Error("Playback stop identity is missing");
  return record;
}
function save(binding: DurableSession, record: RecordV1) {
  const value = JSON.stringify(record);
  localStorage.setItem(binding.key, value);
  if (localStorage.getItem(binding.key) !== value)
    throw new Error("Unable to durably save playback retry state");
}
async function locked<T>(binding: DurableSession, action: () => Promise<T>): Promise<T> {
  if (!navigator.locks?.request)
    return Promise.reject(new Error("Durable playback requires browser storage locking"));
  return await navigator.locks.request(binding.key, { signal: AbortSignal.timeout(30000) }, action);
}
export async function openDurableSession(
  config: PlayerConfig,
  sessionId: string,
  installationId: string,
): Promise<DurableSession> {
  const context = config.capturePlaybackMutationContext?.();
  if (!context || !context.isCurrent() || !installationId)
    throw new Error("Playback installation and account identity are required");
  const identity: Identity = {
    installationId,
    accountId: context.accountId,
    profileId: context.profileId,
    origin: context.origin,
    sessionId,
  };
  const binding = { identity, context, key: PREFIX + encodeURIComponent(JSON.stringify(identity)) };
  await locked(binding, async () => {
    assertCurrent(config, binding);
    if (localStorage.getItem(binding.key) === null)
      save(binding, { version: 1, identity, sequence: 0, stopped: false });
    read(binding);
  });
  return binding;
}
async function request(
  config: PlayerConfig,
  binding: DurableSession,
  body: string,
  stop: boolean,
  keepalive: boolean,
  deadline: number,
) {
  assertCurrent(config, binding);
  const response = await fetch(
    `${binding.identity.origin}/api/v2/playback/${encodeURIComponent(binding.identity.sessionId)}${stop ? "" : "/progress"}`,
    {
      method: stop ? "DELETE" : "POST",
      body,
      keepalive,
      headers: playerRequestHeaders(config, undefined, true),
      signal: AbortSignal.timeout(Math.max(1, Math.min(5000, deadline - Date.now()))),
    },
  );
  if (!response.ok)
    throw new PlayerFetchError(response.status, `Playback update failed (${response.status})`);
  return response;
}
const pause = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
async function progressRequest(
  config: PlayerConfig,
  binding: DurableSession,
  body: string,
  keepalive: boolean,
) {
  for (let attempt = 0; ; attempt++)
    try {
      const response = await request(config, binding, body, false, keepalive, Date.now() + 5000);
      if (response.status !== 200) throw new Error("Unexpected playback progress status");
      return;
    } catch (error) {
      if (
        attempt >= 2 ||
        !(
          error instanceof TypeError ||
          error instanceof DOMException ||
          (error instanceof PlayerFetchError && error.status >= 500)
        )
      )
        throw error;
      await pause(250);
    }
}
export function durableProgress(
  config: PlayerConfig,
  binding: DurableSession,
  sample: Sample,
  keepalive: boolean,
): Promise<void> {
  return locked(binding, async () => {
    assertCurrent(config, binding);
    const record = read(binding);
    if (record.stopBody || record.stopped) return;
    if (record.pendingProgress) {
      await progressRequest(config, binding, record.pendingProgress, keepalive);
      delete record.pendingProgress;
      save(binding, record);
    }
    if (!Number.isSafeInteger(record.sequence + 1)) throw new Error("Playback sequence exhausted");
    record.pendingProgress = JSON.stringify({
      ...sample,
      sequence: ++record.sequence,
      installation_id: binding.identity.installationId,
    } satisfies components["schemas"]["PlaybackProgressBody"]);
    save(binding, record);
    await progressRequest(config, binding, record.pendingProgress, keepalive);
    delete record.pendingProgress;
    save(binding, record);
  });
}
export function durableStop(
  config: PlayerConfig,
  binding: DurableSession,
  sample: Sample | undefined,
  keepalive: boolean,
): Promise<void> {
  return locked(binding, async () => {
    assertCurrent(config, binding);
    const record = read(binding);
    if (record.stopped) return;
    if (!record.stopBody) {
      const final =
        sample ??
        (record.pendingProgress ? (JSON.parse(record.pendingProgress) as Sample) : undefined);
      if (final && !Number.isSafeInteger(record.sequence + 1))
        throw new Error("Playback sequence exhausted");
      record.stopBody = JSON.stringify({
        installation_id: binding.identity.installationId,
        stop_id: randomUUID(),
        ...(final
          ? { position: final.position, is_paused: final.is_paused, sequence: ++record.sequence }
          : {}),
      } satisfies components["schemas"]["PlaybackStopBody"]);
      save(binding, record);
    }
    const body = record.stopBody;
    const deadline = Date.now() + 30000;
    while (Date.now() < deadline) {
      try {
        const response = await request(config, binding, body, true, keepalive, deadline);
        const receipt = (await response.json()) as components["schemas"]["PlaybackMutation"];
        if (receipt.stop_id !== undefined && receipt.stop_id !== JSON.parse(body).stop_id)
          throw new Error("Playback returned another stop receipt");
        if (
          response.status === 200 &&
          (receipt.outcome === "stopped" || receipt.outcome === "replayed")
        ) {
          record.stopped = true;
          delete record.pendingProgress;
          save(binding, record);
          return;
        }
        if (response.status !== 202 || receipt.outcome !== "draining")
          throw new Error("Playback stop not confirmed");
      } catch (error) {
        if (
          !(
            error instanceof TypeError ||
            error instanceof DOMException ||
            (error instanceof PlayerFetchError && error.status >= 500)
          )
        )
          throw error;
      }
      await pause(500);
    }
    throw new Error("Playback stop is still pending. Please retry.");
  });
}

// Restore only exact current ownership. Other identities remain untouched.
export function pendingDurableSessions(
  config: PlayerConfig,
  installationId: string,
): DurableSession[] {
  const context = config.capturePlaybackMutationContext?.();
  if (!context || !context.isCurrent()) return [];
  const pending: DurableSession[] = [];
  for (let i = 0; i < localStorage.length; i++) {
    const key = localStorage.key(i);
    if (!key?.startsWith(PREFIX)) continue;
    const identity = JSON.parse(decodeURIComponent(key.slice(PREFIX.length))) as Identity;
    if (
      identity.installationId !== installationId ||
      identity.accountId !== context.accountId ||
      identity.profileId !== context.profileId ||
      identity.origin !== context.origin
    )
      continue;
    const binding = { key, identity, context };
    const record = read(binding);
    if (!record.stopped) pending.push(binding);
  }
  return pending;
}
