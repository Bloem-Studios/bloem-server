import { captureProfileRequestContext, isCapturedProfileAuthorityActive } from "@/api/client";
import type { PlaybackMutationContext } from "@/player/context/PlayerConfigContext";

/** Capture video request authority once; none of its credentials enter the journal. */
export function captureVideoPlaybackContext(
  accountId: string | number | undefined,
): PlaybackMutationContext | null {
  const captured = captureProfileRequestContext();
  if (accountId == null || !captured) return null;
  const headers = Object.freeze({
    Authorization: `Bearer ${captured.accessToken}`,
    "X-Profile-Id": captured.profileId,
  });
  const isCurrent = () => isCapturedProfileAuthorityActive(captured);
  return {
    accountId: String(accountId),
    profileId: captured.profileId,
    origin: captured.serverOrigin,
    isCurrent,
    mediaRequestHeaders: () => (isCurrent() ? headers : null),
  };
}
