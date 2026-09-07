import {
  createContext,
  lazy,
  Suspense,
  useCallback,
  useContext,
  useMemo,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import {
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  getAccessToken,
  getAuthContextVersion,
  getOrCreateDeviceId,
  getProfileToken,
  refreshAuthentication,
} from "@/api/client";
import { toast } from "sonner";
import { useAuth } from "@/hooks/useAuth";
import { useCurrentProfile } from "@/hooks/useCurrentProfile";
import { initialPlaybackCapabilities, offerPendingInitialStart } from "@/player/initial-v2";
import { offerPendingPlaybackStops } from "@/player/session-mutations";
import type { PlaybackMutationContext } from "@/player/context/PlayerConfigContext";
import type { AudiobookFile } from "@/lib/audiobooks/types";
import { PlayerConfigProvider, type PlayerConfig } from "@/player/context/PlayerConfigContext";
import { storage } from "@/utils/storage";
import type { AudiobookPlayerControls, AudiobookPlayerStatus } from "./AudiobookPlayer";

const AudiobookPlayer = lazy(() => import("./AudiobookPlayer"));

export interface AudiobookPlaybackStartInput {
  contentId: string;
  title: string;
  author?: string;
  narrator?: string;
  posterUrl?: string;
  files: AudiobookFile[];
  initialPositionSeconds?: number;
  autoPlay?: boolean;
}

interface ActiveAudiobookPlayback extends AudiobookPlaybackStartInput {
  requestKey: number;
  authority: PlaybackMutationContext;
}

export interface AudiobookPlaybackControllerValue {
  active: AudiobookPlayerStatus | null;
  activeRequest: ActiveAudiobookPlayback | null;
  isBackgroundBarVisible: boolean;
  startPlayback: (input: AudiobookPlaybackStartInput) => void;
  stopPlayback: () => void;
  toggleActivePlayback: () => void;
}

const AudiobookPlaybackControllerContext = createContext<AudiobookPlaybackControllerValue | null>(
  null,
);

export function useAudiobookPlaybackController() {
  return useContext(AudiobookPlaybackControllerContext);
}

export function AudiobookPlaybackProvider({ children }: { children: ReactNode }) {
  const { user } = useAuth();
  const { profile } = useCurrentProfile();
  const accountId = user?.id;
  const profileId = profile?.id;
  const [storedRequest, setActiveRequest] = useState<ActiveAudiobookPlayback | null>(null);
  const [active, setActive] = useState<AudiobookPlayerStatus | null>(null);
  const [controls, setControls] = useState<AudiobookPlayerControls | null>(null);
  const activeRequest = storedRequest?.authority.isCurrent() ? storedRequest : null;
  const playerConfig = useMemo<PlayerConfig>(
    () => ({
      apiBaseUrl: "/api/v1",
      capturePlaybackMutationContext: () => {
        const captured = captureProfileRequestContext();
        if (accountId == null || !captured || captured.profileId !== profileId) return null;
        return {
          accountId: String(accountId),
          profileId: captured.profileId,
          origin: captured.serverOrigin,
          isCurrent: () => isCapturedProfileAuthorityActive(captured),
        };
      },
      onPlaybackStartError: (error, retry) =>
        toast.error("Audiobook start unconfirmed", {
          id: "audiobook-start-pending",
          description: error.message,
          action: { label: "Retry", onClick: retry },
        }),
      onPlaybackStopError: (sessionId, error, retry) =>
        toast.error("Audiobook stop unconfirmed", {
          id: `audiobook-stop-${sessionId}`,
          description: error.message,
          action: { label: "Retry", onClick: retry },
        }),
      getAccessToken: () => getAccessToken(),
      getProfileId: () => storage.get(storage.KEYS.PROFILE_ID),
      getProfileToken: () => getProfileToken(),
      getDeviceId: () => getOrCreateDeviceId(),
      refreshToken: refreshAuthentication,
      getAuthContext: getAuthContextVersion,
    }),
    [accountId, profileId],
  );

  useEffect(() => {
    if (accountId == null || !profileId) return;
    let disposed = false;
    void initialPlaybackCapabilities(playerConfig)
      .then((cap) => {
        if (disposed || !cap.installation_id) return;
        offerPendingInitialStart(playerConfig, cap);
        offerPendingPlaybackStops(playerConfig, cap.installation_id);
      })
      .catch(() => {
        /* A requested start surfaces unavailable; never falls back. */
      });
    return () => {
      disposed = true;
    };
  }, [accountId, profileId, playerConfig]);

  const startPlayback = useCallback(
    (input: AudiobookPlaybackStartInput) => {
      const authority = playerConfig.capturePlaybackMutationContext?.();
      if (!authority?.isCurrent()) {
        toast.error("Audiobook playback identity unavailable");
        return;
      }
      setControls(null);
      setActive(null);
      setActiveRequest((previous) => ({
        ...input,
        authority,
        requestKey: (previous?.requestKey ?? 0) + 1,
      }));
    },
    [playerConfig],
  );

  const stopPlayback = useCallback(() => {
    setControls(null);
    setActive(null);
    setActiveRequest(null);
  }, []);

  const toggleActivePlayback = useCallback(() => {
    if (activeRequest) controls?.togglePlay();
  }, [activeRequest, controls]);

  const value = useMemo<AudiobookPlaybackControllerValue>(
    () => ({
      active: activeRequest ? active : null,
      activeRequest,
      isBackgroundBarVisible: Boolean(activeRequest),
      startPlayback,
      stopPlayback,
      toggleActivePlayback,
    }),
    [active, activeRequest, startPlayback, stopPlayback, toggleActivePlayback],
  );

  return (
    <AudiobookPlaybackControllerContext.Provider value={value}>
      {children}
      {activeRequest && (
        <PlayerConfigProvider config={playerConfig}>
          <Suspense fallback={null}>
            <AudiobookPlayer
              key={`${activeRequest.contentId}-${activeRequest.requestKey}`}
              contentId={activeRequest.contentId}
              title={activeRequest.title}
              author={activeRequest.author}
              narrator={activeRequest.narrator}
              posterUrl={activeRequest.posterUrl}
              files={activeRequest.files}
              initialPositionSeconds={activeRequest.initialPositionSeconds}
              autoPlay={activeRequest.autoPlay}
              onClose={stopPlayback}
              onPlaybackStateChange={setActive}
              onControlsChange={setControls}
            />
          </Suspense>
        </PlayerConfigProvider>
      )}
    </AudiobookPlaybackControllerContext.Provider>
  );
}
