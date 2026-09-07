import type { components } from "@/api/v2/schema";
import type { PlayerConfig } from "./context/PlayerConfigContext";
import { playerRequestHeaders } from "./player-fetch";
import { durableSessionFor } from "./session-mutations";
import type { RouteEventV3 } from "./protocol-v3";
import { randomUUID } from "@/lib/uuid";

type RouteEventBody = components["schemas"]["PlaybackRouteEventBody"];

/**
 * Reports a route event for a durable v2 attempt. Diagnostics never control
 * playback: the event carries a fresh id, is sent once, and any failure is
 * swallowed. A lost 202 is not retried by this helper; a caller that does
 * retry must reuse the same event_id.
 */
export async function reportDurableRouteEvent(
  config: PlayerConfig,
  sessionId: string,
  event: RouteEventV3,
): Promise<void> {
  const durable = durableSessionFor(sessionId);
  if (!durable || !durable.context.isCurrent()) return;
  const payload: RouteEventBody = {
    ...event,
    installation_id: durable.identity.installationId,
    event_id: randomUUID(),
  } as RouteEventBody;
  try {
    await fetch(`${durable.identity.origin}/api/v2/playback/route-events`, {
      method: "POST",
      headers: playerRequestHeaders(config, undefined, true),
      body: JSON.stringify(payload),
      signal: AbortSignal.timeout(5000),
    });
  } catch {
    // Dropped, never retried.
  }
}
