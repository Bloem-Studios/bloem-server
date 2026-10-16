import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { expect, it, vi } from "vitest";
import { PreviewCtaContext, PreviewModeContext } from "./usePreviewMode";
import { WatchPlaybackProvider } from "@/playback/WatchPlaybackChrome";
import { useWatchPlaybackController } from "@/playback/watchPlaybackContext";
import {
  AudiobookPlaybackProvider,
  useAudiobookPlaybackController,
} from "@/pages/audiobooks/player/audiobookPlaybackContext";
import { createWatchRouteRequest } from "@/pages/watchRouteHelpers";
vi.mock("@/hooks/useCurrentProfile", () => ({ useCurrentProfile: () => ({ profile: null }) }));
function Controls() {
  const video = useWatchPlaybackController();
  const audio = useAudiobookPlaybackController();
  return (
    <>
      <button onClick={() => video.startPlayback({ contentId: "movie" }, "viewer")}>Video</button>
      <button
        onClick={() => video.syncRouteRequest(createWatchRouteRequest({ contentId: "episode" }))}
      >
        Route
      </button>
      <button onClick={() => audio?.startPlayback({ contentId: "book", title: "Book", files: [] })}>
        Audio
      </button>
      <p>Video state: {video.state.request?.contentId ?? "idle"}</p>
      <p>Audio state: {audio?.activeRequest?.contentId ?? "idle"}</p>
    </>
  );
}
it.each(["guest", "personal"] as const)(
  "intercepts video, route sync and audio for %s previews",
  (kind) => {
    const opened: string[] = [];
    render(
      <MemoryRouter>
        <PreviewModeContext.Provider
          value={{
            kind,
            ends_at: null,
            cta_url: "https://store.test/trial",
            cta_label: "Subscribe",
            item_cta_label: "",
            cta_message: "",
            launch_url: "",
            storefront_url: "",
          }}
        >
          <PreviewCtaContext.Provider value={(id) => opened.push(id ?? "")}>
            <WatchPlaybackProvider>
              <AudiobookPlaybackProvider>
                <Controls />
              </AudiobookPlaybackProvider>
            </WatchPlaybackProvider>
          </PreviewCtaContext.Provider>
        </PreviewModeContext.Provider>
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByText("Video"));
    fireEvent.click(screen.getByText("Route"));
    fireEvent.click(screen.getByText("Audio"));
    expect(opened).toEqual(["movie", "episode", "book"]);
    expect(screen.getByText("Video state: idle")).toBeInTheDocument();
    expect(screen.getByText("Audio state: idle")).toBeInTheDocument();
  },
);
