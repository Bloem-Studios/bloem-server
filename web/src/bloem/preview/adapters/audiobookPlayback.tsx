/* eslint-disable react-refresh/only-export-components -- Preserve the original module exports at this adapter boundary. */
import { createContext, useContext, useMemo, type ReactNode } from "react";
import {
  AudiobookPlaybackProvider as OriginalProvider,
  useAudiobookPlaybackController as useOriginalController,
  type AudiobookPlaybackControllerValue,
} from "../../../pages/audiobooks/player/audiobookPlaybackContext";
import { usePreviewMode } from "../usePreviewMode";
export * from "../../../pages/audiobooks/player/audiobookPlaybackContext";
const PreviewAudioContext = createContext<AudiobookPlaybackControllerValue | null>(null);
export function useAudiobookPlaybackController() {
  const original = useOriginalController();
  const preview = useContext(PreviewAudioContext);
  return preview ?? original;
}
export function AudiobookPlaybackProvider({ children }: { children: ReactNode }) {
  const { active, requestSubscription } = usePreviewMode();
  const value = useMemo<AudiobookPlaybackControllerValue>(
    () => ({
      active: null,
      activeRequest: null,
      isBackgroundBarVisible: false,
      startPlayback: (input) => requestSubscription(input.contentId),
      stopPlayback: () => {},
      toggleActivePlayback: () => requestSubscription(),
    }),
    [requestSubscription],
  );
  return active ? (
    <PreviewAudioContext.Provider value={value}>{children}</PreviewAudioContext.Provider>
  ) : (
    <OriginalProvider>{children}</OriginalProvider>
  );
}
