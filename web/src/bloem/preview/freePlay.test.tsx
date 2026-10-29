import { act, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import PreviewBanner from "./PreviewBanner";
import SubscribeCtaDialog from "./SubscribeCtaDialog";
import { PreviewCtaContext, PreviewModeContext } from "./usePreviewMode";
import { previewRouteRedirect } from "./routes";
import { storeGuestSession, type PreviewPresentation } from "./session";
import { WatchPlaybackProvider } from "@/playback/WatchPlaybackChrome";
import { useWatchPlaybackController } from "@/playback/watchPlaybackContext";
import {
  AudiobookPlaybackProvider,
  useAudiobookPlaybackController,
} from "@/pages/audiobooks/player/audiobookPlaybackContext";
const refreshAccount = vi.fn(async () => {});
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({ logout: () => {}, refreshAccount }) }));
vi.mock("@/api/client", () => ({ getAccessToken: () => "synthetic-preview" }));
vi.mock("@/hooks/useCurrentProfile", () => ({ useCurrentProfile: () => ({ profile: null }) }));
vi.mock("@/hooks/useBranding", () => ({ useBranding: () => ({ serverName: "Example" }) }));
afterEach(() => {
  vi.useRealTimers();
  refreshAccount.mockClear();
});
const guest = (extra: Partial<PreviewPresentation> = {}): PreviewPresentation => ({
  kind: "guest",
  ends_at: "2999-01-01T00:00:00Z",
  cta_url: "https://store.test/trial",
  cta_label: "Start a trial!",
  item_cta_label: "",
  cta_message: "",
  subscribe_label: "",
  subscribe_url: "",
  launch_url: "",
  storefront_url: "https://store.test",
  ...extra,
});
const playing = guest({
  free_play: { allowance_seconds: 1800, used_seconds: 120, ends_at: "2999-01-01T00:00:00Z" },
});
function withGuest(presentation: PreviewPresentation, children: React.ReactNode) {
  storeGuestSession({
    refresh_token: "synthetic",
    expires_at: "2999-01-01T00:00:00Z",
    item: "movie:7",
    presentation,
  });
  return (
    <MemoryRouter>
      <PreviewModeContext.Provider value={presentation}>{children}</PreviewModeContext.Provider>
    </MemoryRouter>
  );
}
it("offers a browsing guest the storefront's free play for the title", () => {
  render(
    withGuest(
      guest({ free_play_url: "https://store.test/preview/play", free_play_seconds: 1800 }),
      <SubscribeCtaDialog open onOpenChange={() => {}} item="movie:7" />,
    ),
  );
  const link = screen.getByRole("link", { name: "Play free for 30 minutes" });
  const href = new URL(link.getAttribute("href") ?? "");
  expect(href.origin + href.pathname).toBe("https://store.test/preview/play");
  expect(href.searchParams.get("item")).toBe("movie:7");
});
it("uses the operator's free-play wording and offers nothing without a link", () => {
  const { unmount } = render(
    withGuest(
      guest({ free_play_url: "https://store.test/preview/play", free_play_label: "Watch free" }),
      <SubscribeCtaDialog open onOpenChange={() => {}} item="movie:7" />,
    ),
  );
  expect(screen.getByRole("link", { name: "Watch free" })).toBeInTheDocument();
  unmount();
  render(withGuest(guest(), <SubscribeCtaDialog open onOpenChange={() => {}} item="movie:7" />));
  expect(screen.queryByRole("link", { name: /free/i })).not.toBeInTheDocument();
});
it("shows the free play left in place of the visit's countdown and keeps it current", () => {
  vi.useFakeTimers();
  render(withGuest(playing, <PreviewBanner />));
  expect(screen.getByText("Free play: 28 min left")).toBeInTheDocument();
  expect(screen.queryByText(/remaining/)).not.toBeInTheDocument();
  act(() => vi.advanceTimersByTime(30_000));
  expect(refreshAccount).toHaveBeenCalled();
});
it("tells the guest when free play has ended", () => {
  const opened: string[] = [];
  const tree = (presentation: PreviewPresentation) => (
    <MemoryRouter>
      <PreviewModeContext.Provider value={presentation}>
        <PreviewCtaContext.Provider value={(id) => opened.push(id ?? "")}>
          <PreviewBanner />
        </PreviewCtaContext.Provider>
      </PreviewModeContext.Provider>
    </MemoryRouter>
  );
  storeGuestSession({
    refresh_token: "synthetic",
    expires_at: "2999-01-01T00:00:00Z",
    item: "movie:7",
    presentation: playing,
  });
  const { rerender } = render(tree(playing));
  expect(opened).toEqual([]);
  rerender(tree(guest()));
  expect(opened).toEqual(["movie:7"]);
  render(withGuest(guest(), <SubscribeCtaDialog open onOpenChange={() => {}} freePlayEnded />));
  expect(screen.getByText("Your free play has ended")).toBeInTheDocument();
});
it("lets free play reach the player for video only", () => {
  expect(previewRouteRedirect("/watch/movie:7", "", true)).toBeNull();
  expect(previewRouteRedirect("/watch/movie:7", "", false)).toBe("/item/movie:7?cta=1");
  expect(previewRouteRedirect("/reader/ebook/book:1", "", true)).toBe("/item/book:1?cta=1");
  const opened: string[] = [];
  function Controls() {
    const video = useWatchPlaybackController();
    const audio = useAudiobookPlaybackController();
    return (
      <>
        <button onClick={() => video.startPlayback({ contentId: "movie" }, "viewer")}>Video</button>
        <button onClick={() => audio?.startPlayback({ contentId: "book", title: "B", files: [] })}>
          Audio
        </button>
      </>
    );
  }
  render(
    withGuest(
      playing,
      <PreviewCtaContext.Provider value={(id) => opened.push(id ?? "")}>
        <WatchPlaybackProvider>
          <AudiobookPlaybackProvider>
            <Controls />
          </AudiobookPlaybackProvider>
        </WatchPlaybackProvider>
      </PreviewCtaContext.Provider>,
    ),
  );
  fireEvent.click(screen.getByText("Video"));
  fireEvent.click(screen.getByText("Audio"));
  // Video went to the player; only the audiobook asked for the trial.
  expect(opened).toEqual(["book"]);
});
