import { initialPlaybackCapabilities } from "@/player/initial-v2";
import { playerRequestHeaders, PlayerFetchError } from "@/player/player-fetch";
import type { PlayerConfig, PlaybackMutationContext } from "@/player/context/PlayerConfigContext";
import { readTimelineManifest } from "@/player/bound-client-timeline";

/** One explicit playback intent retains this no-store discovery snapshot. */
export async function discoverAudiobookTimeline(
  config: PlayerConfig,
  authority: PlaybackMutationContext,
  contentId: string,
  anchorFileId: string,
) {
  if (!authority.isCurrent()) throw new Error("Playback identity changed");
  const cap = await initialPlaybackCapabilities(config);
  if (!authority.isCurrent()) throw new Error("Playback identity changed");
  if (
    cap.state !== "available" ||
    !cap.allowed ||
    !cap.installation_id ||
    !cap.protocol_versions.includes(3) ||
    !cap.features.includes("sequenced_progress_v1") ||
    !cap.features.includes("bound_client_timeline")
  )
    throw new Error("Bound audiobook playback is unavailable on this server");
  const query = new URLSearchParams({ installation_id: cap.installation_id });
  const response = await fetch(
    `${authority.origin}/api/v2/playback/timelines/${encodeURIComponent(anchorFileId)}?${query}`,
    {
      headers: playerRequestHeaders(config, undefined, false),
      cache: "no-store",
      signal: AbortSignal.timeout(5000),
    },
  );
  if (!authority.isCurrent()) throw new Error("Playback identity changed");
  if (!response.ok)
    throw new PlayerFetchError(response.status, "Audiobook timeline discovery failed");
  const wire: unknown = await response.json();
  if (!authority.isCurrent()) throw new Error("Playback identity changed");
  return readTimelineManifest(wire, cap.installation_id, contentId, anchorFileId);
}
