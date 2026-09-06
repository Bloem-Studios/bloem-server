import type { components } from "@/api/v2/schema";
import type { PlaybackMutationContext, PlayerConfig } from "./context/PlayerConfigContext";
import { playerRequestHeaders, PlayerFetchError } from "./player-fetch";
import { playerV2Origin } from "./player-v2";
import type { DecisionResponseV3, StartRequestV3 } from "./protocol-v3";
import { offerPendingPlaybackStops, registerDurableSessionMutations } from "./session-mutations";

export type InitialPlaybackCapabilities = components["schemas"]["PlaybackCapabilities"];
export async function initialPlaybackCapabilities(
  config: PlayerConfig,
): Promise<InitialPlaybackCapabilities | null> {
  if (!config.capturePlaybackMutationContext) return null;
  const authority = config.capturePlaybackMutationContext();
  if (!authority?.isCurrent()) throw new Error("Playback account identity unavailable");
  const response = await fetch(`${playerV2Origin(config)}/api/v2/playback/capabilities`, {
    headers: playerRequestHeaders(config, undefined, false),
    signal: AbortSignal.timeout(5000),
  });
  if (!authority.isCurrent()) throw new Error("Playback identity changed");
  if (response.status === 404) return null;
  if (!response.ok)
    throw new PlayerFetchError(response.status, "Playback capabilities unavailable");
  const cap = (await response.json()) as InitialPlaybackCapabilities;
  if (
    (cap.state !== "not_configured" && !cap.installation_id) ||
    !Array.isArray(cap.features) ||
    !Array.isArray(cap.protocol_versions)
  )
    throw new Error("Invalid playback capabilities");
  return cap;
}
function numericFileID(value: unknown): number {
  if (typeof value !== "string" || !/^[1-9]\d*$/.test(value))
    throw new Error("Invalid playback media file ID");
  const n = Number(value);
  if (!Number.isSafeInteger(n))
    throw new Error("Playback media file ID exceeds this player's supported range");
  return n;
}
export async function startInitialPlayback(
  config: PlayerConfig,
  body: StartRequestV3,
): Promise<DecisionResponseV3 | null> {
  const cap = await initialPlaybackCapabilities(config);
  if (!cap || cap.state === "not_configured") {
    const pending = pendingStartInstallation(config);
    if (pending) {
      offerPendingInitialStart(config, {
        installation_id: pending,
        revision: "",
        state: "not_configured",
        allowed: false,
        features: [],
        protocol_versions: [],
        deliveries: [],
      });
      throw new Error(
        "An earlier playback start is still unconfirmed; legacy fallback is unavailable.",
      );
    }
    return null;
  }
  if (
    !cap.installation_id ||
    cap.state !== "available" ||
    !cap.allowed ||
    !cap.protocol_versions.includes(3) ||
    !cap.features.includes("sequenced_progress_v1")
  )
    throw new Error("Playback is not admitted for this account");
  if (!navigator.locks?.request)
    throw new Error("Durable playback requires browser storage locking");
  const authority = config.capturePlaybackMutationContext?.();
  if (!authority?.isCurrent()) throw new Error("Playback identity unavailable");
  const key = startKey(authority, cap.installation_id);
  const payload = JSON.stringify({
    ...body,
    file_id: String(body.file_id),
    installation_id: cap.installation_id,
  } satisfies components["schemas"]["PlaybackStartBody"]);
  return await navigator.locks.request(key, { signal: AbortSignal.timeout(30000) }, async () => {
    if (!authority.isCurrent()) throw new Error("Playback identity changed");
    const pending = localStorage.getItem(key);
    if (pending && pending !== payload) {
      offerPendingInitialStart(config, cap);
      throw new Error(
        "An earlier playback start is still unconfirmed. Resolve it before starting another session.",
      );
    }
    localStorage.setItem(key, payload);
    if (localStorage.getItem(key) !== payload)
      throw new Error("Playback retry storage unavailable");
    try {
      return await dispatchInitialStart(config, authority, cap.installation_id!, key, payload);
    } catch (error) {
      offerPendingInitialStart(config, cap);
      throw error;
    }
  });
}

function pendingStartInstallation(config: PlayerConfig): string | undefined {
  const context = config.capturePlaybackMutationContext?.();
  if (!context?.isCurrent()) return;
  for (let i = 0; i < localStorage.length; i++) {
    const key = localStorage.key(i);
    if (!key?.startsWith("silo-playback-start-v1:")) continue;
    const identity = JSON.parse(
      decodeURIComponent(key.slice("silo-playback-start-v1:".length)),
    ) as { installationId: string; accountId: string; profileId: string; origin: string };
    if (
      identity.accountId === context.accountId &&
      identity.profileId === context.profileId &&
      identity.origin === context.origin
    )
      return identity.installationId;
  }
}

function startKey(context: PlaybackMutationContext, installationId: string): string {
  return (
    "silo-playback-start-v1:" +
    encodeURIComponent(
      JSON.stringify({
        installationId,
        accountId: context.accountId,
        profileId: context.profileId,
        origin: context.origin,
      }),
    )
  );
}
async function dispatchInitialStart(
  config: PlayerConfig,
  authority: PlaybackMutationContext,
  installationId: string,
  key: string,
  payload: string,
): Promise<DecisionResponseV3> {
  if (!authority.isCurrent()) throw new Error("Playback identity changed");
  const saved = JSON.parse(payload) as {
    installation_id?: string;
    playback_attempt_id?: string;
    profile_id?: string;
  };
  if (
    payload.length > 65536 ||
    saved.installation_id !== installationId ||
    saved.profile_id !== authority.profileId ||
    !saved.playback_attempt_id
  )
    throw new Error("Invalid saved playback start identity");
  const response = await fetch(`${authority.origin}/api/v2/playback/start`, {
    method: "POST",
    headers: playerRequestHeaders(config, undefined, true),
    body: payload,
    signal: AbortSignal.timeout(15000),
  });
  if (!authority.isCurrent()) throw new Error("Playback identity changed while starting");
  if (!response.ok) {
    if (response.status === 422) {
      const problem = (await response.json().catch(() => null)) as {
        type?: string;
        status?: number;
      } | null;
      // This contract rejection happens before reservation. Every uncertain
      // failure keeps its journal so a later retry cannot create a new attempt.
      if (
        problem?.type === "https://siloserver.org/docs/api/v2/problems/validation_failed" &&
        problem.status === 422 &&
        authority.isCurrent() &&
        localStorage.getItem(key) === payload
      ) {
        localStorage.removeItem(key);
      }
    }
    throw new PlayerFetchError(response.status, "Failed to start playback");
  }
  const wire = (await response.json()) as components["schemas"]["PlaybackDecision"];
  const plan = wire.playback_plan;
  const converted = plan
    ? {
        ...plan,
        requested_media_file_id: numericFileID(plan.requested_media_file_id),
        effective_media_file_id: numericFileID(plan.effective_media_file_id),
        source: { ...plan.source, media_file_id: numericFileID(plan.source.media_file_id) },
      }
    : undefined;
  if (wire.session_id)
    await registerDurableSessionMutations(config, wire.session_id, installationId);
  else if (!wire.terminal)
    throw new Error("Playback start returned no durable session or terminal decision");
  localStorage.removeItem(key);
  return { ...wire, playback_plan: converted } as DecisionResponseV3;
}

export function offerPendingInitialStart(
  config: PlayerConfig,
  cap: InitialPlaybackCapabilities,
): void {
  const authority = config.capturePlaybackMutationContext?.();
  if (!authority?.isCurrent() || !cap.installation_id) return;
  const installationId = cap.installation_id;
  const key = startKey(authority, installationId);
  const payload = localStorage.getItem(key);
  if (!payload) return;
  const retry = () => {
    void (async () => {
      if (!authority.isCurrent())
        throw new Error("Return to the original playback identity before retrying.");
      const current = await initialPlaybackCapabilities(config);
      if (
        !current?.allowed ||
        current.state !== "available" ||
        current.installation_id !== installationId
      )
        throw new Error("The original playback installation is not currently admitted.");
      if (!navigator.locks?.request)
        throw new Error("Durable playback requires browser storage locking");
      await navigator.locks.request(key, { signal: AbortSignal.timeout(30000) }, async () => {
        if (localStorage.getItem(key) !== payload)
          throw new Error("The pending playback start has already changed.");
        await dispatchInitialStart(config, authority, installationId, key, payload);
      });
      offerPendingPlaybackStops(config, installationId, true);
    })().catch((error) =>
      config.onPlaybackStartError?.(
        error instanceof Error ? error : new Error("Playback start is unconfirmed"),
        retry,
      ),
    );
  };
  config.onPlaybackStartError?.(
    new Error(
      "A previous playback start is unconfirmed. Retry to resolve it before starting another session.",
    ),
    retry,
  );
}
