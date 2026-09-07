import type { components } from "@/api/v2/schema";
import type { PlayerConfig } from "./context/PlayerConfigContext";
import { playerRequestHeaders, PlayerFetchError } from "./player-fetch";
import { durableSessionFor } from "./session-mutations";
import type { DecisionResponseV3, ReplanRequestV3 } from "./protocol-v3";

type ReplanBody = components["schemas"]["PlaybackReplanBody"];

/**
 * Whether a session was started through the durable v2 flow. Only such a
 * session carries the installed authority the v2 replan and route-event
 * routes require; a legacy session keeps its v1 lifecycle unchanged.
 */
export function hasDurableLifecycle(sessionId: string | null | undefined): boolean {
  return !!sessionId && durableSessionFor(sessionId) !== undefined;
}

function numericFileID(value: unknown): number {
  if (typeof value !== "string" || !/^[1-9]\d*$/.test(value))
    throw new Error("Invalid playback media file ID");
  const n = Number(value);
  if (!Number.isSafeInteger(n))
    throw new Error("Playback media file ID exceeds this player's supported range");
  return n;
}

/**
 * Replans a durable v2 session under its captured authority. The request is
 * the ordinary v3 replan body plus the installation the session was started
 * with; a decision is delivered only while that authority is still current.
 * Refusals surface as PlayerFetchError so the hook's existing handling applies.
 */
export async function replanDurableSession(
  config: PlayerConfig,
  sessionId: string,
  body: ReplanRequestV3,
): Promise<DecisionResponseV3> {
  const durable = durableSessionFor(sessionId);
  if (!durable) throw new Error("Playback session has no durable authority");
  if (!durable.context.isCurrent()) throw new Error("Playback identity changed");
  const payload: ReplanBody = {
    ...body,
    installation_id: durable.identity.installationId,
  } as ReplanBody;
  const response = await fetch(
    `${durable.identity.origin}/api/v2/playback/${encodeURIComponent(sessionId)}/replan`,
    {
      method: "POST",
      headers: playerRequestHeaders(config, undefined, true),
      body: JSON.stringify(payload),
      signal: AbortSignal.timeout(15000),
    },
  );
  if (!durable.context.isCurrent()) throw new Error("Playback identity changed while replanning");
  const text = await response.text().catch(() => "");
  if (!response.ok) {
    let message = "Playback replan was refused";
    let code: string | undefined;
    try {
      const problem = JSON.parse(text) as { detail?: string; type?: string };
      if (problem.detail) message = problem.detail;
      code = problem.type?.split("/").pop();
    } catch {
      // A non-problem body keeps the generic message.
    }
    throw new PlayerFetchError(response.status, message, code, text);
  }
  const wire = JSON.parse(text) as components["schemas"]["PlaybackDecision"];
  const plan = wire.playback_plan;
  const converted = plan
    ? {
        ...plan,
        requested_media_file_id: numericFileID(plan.requested_media_file_id),
        effective_media_file_id: numericFileID(plan.effective_media_file_id),
        source: { ...plan.source, media_file_id: numericFileID(plan.source.media_file_id) },
      }
    : undefined;
  return { ...wire, playback_plan: converted } as DecisionResponseV3;
}
