import {
  api,
  apiWithProfileRequestContext,
  getAccessToken,
  isCapturedProfileAuthorityActive,
  isProfileRequestContextCurrent,
  type ProfileRequestContextSnapshot,
} from "@/api/client";
import type { LiveTVSessionStartResponse } from "@/api/types";
import type { LiveTVClientCapabilities } from "@/hooks/queries/useLiveTV";

/** One mounted viewer owns one tuner lease, including a tune still in flight. */
export function startLiveTVPlayback(
  channelId: string,
  authority: ProfileRequestContextSnapshot,
  capabilities: LiveTVClientCapabilities,
  onReady: (session: LiveTVSessionStartResponse) => void,
  onError: (error: Error) => void,
): () => void {
  let disposed = false;
  let sessionId: string | null = null;
  let heartbeat: ReturnType<typeof setInterval> | undefined;
  let heartbeatPending = false;
  const headers = () => ({
    Authorization: `Bearer ${isProfileRequestContextCurrent(authority) ? (getAccessToken() ?? authority.accessToken) : authority.accessToken}`,
    "X-Profile-Id": authority.profileId,
    "X-Profile-Token": authority.profileToken ?? "",
  });
  const release = () => {
    if (!sessionId) return;
    const id = sessionId;
    sessionId = null;
    // Retain the original viewer even if cleanup runs after logout/profile switch.
    void fetch(`/api/v1/livetv/sessions/${encodeURIComponent(id)}`, {
      method: "DELETE",
      headers: headers(),
      keepalive: true,
    }).catch(() => undefined);
  };
  const stop = () => {
    disposed = true;
    clearInterval(heartbeat);
    window.removeEventListener("pagehide", onPageHide);
    release();
  };
  const fail = (error: unknown) => {
    if (disposed) return;
    stop();
    onError(error instanceof Error ? error : new Error("Live TV playback failed"));
  };
  const onPageHide = () =>
    fail(new Error("Live TV stopped while you were away. Reconnect to continue."));
  window.addEventListener("pagehide", onPageHide);
  if (!isCapturedProfileAuthorityActive(authority)) {
    fail(new Error("The selected profile changed. Open Live TV again."));
    return stop;
  }
  // A tune allocates capacity. Never replay it after an ambiguous network error.
  // Read late responses so their session can still be released after navigation.
  void api<LiveTVSessionStartResponse>(
    `/livetv/channels/${encodeURIComponent(channelId)}/session`,
    {
      method: "POST",
      headers: { ...headers(), "Content-Type": "application/json" },
      body: JSON.stringify(capabilities),
    },
    "none",
  )
    .then((session) => {
      sessionId = session.session_id;
      if (disposed || !isCapturedProfileAuthorityActive(authority)) {
        stop();
        return;
      }
      if (!sessionId || !(session.hls_url || session.stream_url)) {
        fail(new Error("The server did not return a playable Live TV stream."));
        return;
      }
      onReady(session);
      heartbeat = setInterval(() => {
        if (heartbeatPending || disposed || !sessionId) return;
        if (!isCapturedProfileAuthorityActive(authority)) {
          stop();
          return;
        }
        heartbeatPending = true;
        void apiWithProfileRequestContext(
          `/livetv/sessions/${encodeURIComponent(sessionId)}/heartbeat`,
          authority,
          {
            method: "POST",
          },
        )
          .catch(fail)
          .finally(() => {
            heartbeatPending = false;
          });
      }, 30_000);
    })
    .catch(fail);
  return stop;
}
