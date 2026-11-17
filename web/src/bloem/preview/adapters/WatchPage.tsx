import { PrePlaybackCampaign } from "@/components/engagement/CampaignPlacements";
import { WatchPage as SiloWatchPage } from "@/player/components/WatchPage";
import type { WatchPageProps } from "@/player/types";

// eslint-disable-next-line react-refresh/only-export-components -- the player module, with WatchPage replaced
export * from "@/player/components/WatchPage";

/**
 * The watch host's player behind Bloem's pre-playback campaign. The campaign
 * shows only in the foreground (not minimized, not post-roll), once per
 * playback request; cancelling it leaves playback as the player's exit does.
 */
export function WatchPage(props: WatchPageProps) {
  return (
    <PrePlaybackCampaign
      key={props.playbackRequestKey}
      contentId={props.contentId}
      enabled={props.displayMode === "foreground"}
      onCancel={() => void props.onExit()}
    >
      <SiloWatchPage {...props} />
    </PrePlaybackCampaign>
  );
}
