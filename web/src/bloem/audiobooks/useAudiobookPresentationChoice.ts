import { useCallback, useMemo } from "react";
import type { ItemDetail } from "@/api/types";
import { useBloemCapabilities } from "@/hooks/queries/useBloemCapabilities";
import { useAudiobookPlaybackController } from "@/pages/audiobooks/player/audiobookPlaybackContext";
import { useRememberedPresentation } from "./presentationChoice";
import {
  NATIVE_AUDIOBOOK_PRESENTATIONS_TOKEN,
  choosePresentation,
  itemAudiobookPresentations,
  presentationForFiles,
  type AudiobookPresentation,
} from "./presentations";

export interface AudiobookPresentationChoice {
  presentations: AudiobookPresentation[];
  /** The presentation to show and play; undefined while pending or when none is playable. */
  chosen: AudiobookPresentation | undefined;
  /** Remembers an explicit pick; stops this item's playback when it used another one. */
  choose: (presentationId: string) => void;
  nativeAvailable: boolean;
  /** Native presentations exist and the capability check has not answered yet. */
  pending: boolean;
}

/**
 * Resolves which presentation of an audiobook item the listener uses. A book
 * already playing keeps its presentation; otherwise the contract order applies
 * (remembered, last_file_id, local, native). The capability check runs only
 * for items with a native presentation, and nothing is chosen until it
 * answers, so a late answer can never swap the recording under a listener.
 */
export function useAudiobookPresentationChoice(
  item: ItemDetail | undefined,
): AudiobookPresentationChoice {
  const presentations = useMemo(() => itemAudiobookPresentations(item), [item]);
  const hasNative = presentations.some((presentation) => presentation.native);
  const capabilities = useBloemCapabilities(hasNative);
  const nativeAvailable =
    capabilities.data?.feature_tokens?.includes(NATIVE_AUDIOBOOK_PRESENTATIONS_TOKEN) ?? false;
  const pending = hasNative && capabilities.isPending;
  const [remembered, remember] = useRememberedPresentation(item?.content_id);
  const controller = useAudiobookPlaybackController();
  const activeRequest = controller?.activeRequest;
  const playing =
    item && activeRequest?.contentId === item.content_id
      ? presentationForFiles(
          presentations,
          activeRequest.files.map((file) => file.id),
        )
      : undefined;
  const userData = item?.user_data && "position_seconds" in item.user_data ? item.user_data : null;
  const chosen = pending
    ? undefined
    : (playing ??
      choosePresentation(presentations, {
        remembered,
        lastFileId: userData?.last_file_id,
        nativeAvailable,
      }));

  const playingId = playing?.id;
  const stopPlayback = controller?.stopPlayback;
  const choose = useCallback(
    (presentationId: string) => {
      remember(presentationId);
      // An explicit pick ends playback of the other recording rather than
      // leaving the controls driving a book the page no longer shows.
      if (playingId && playingId !== presentationId) stopPlayback?.();
    },
    [playingId, remember, stopPlayback],
  );

  return { presentations, chosen, choose, nativeAvailable, pending };
}
