import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { PreviewCtaContext, PreviewModeContext } from "./usePreviewMode";
import ActionBar from "./adapters/ActionBar";
import CardPlayOverlay from "./adapters/CardPlayOverlay";
import MediaItemMenu, { PosterCardFavoriteButton } from "./adapters/MediaItemMenu";
vi.mock("@/playback/watchPlaybackContext", () => ({
  useWatchPlaybackController: () => ({ startPlayback: () => {} }),
}));
vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ user: null }),
  useOptionalAuth: () => null,
}));
function Harness({
  children,
  kind = "guest",
  opened = () => {},
  itemCtaLabel = "",
}: {
  children: ReactNode;
  kind?: "guest" | "personal";
  opened?: (item?: string) => void;
  itemCtaLabel?: string;
}) {
  return (
    <MemoryRouter>
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
      >
        <PreviewModeContext.Provider
          value={{
            kind,
            ends_at: null,
            cta_url: "https://store.test/trial",
            cta_label: "Subscribe",
            item_cta_label: itemCtaLabel,
            cta_message: "",
            storefront_url: "",
            launch_url: "",
          }}
        >
          <PreviewCtaContext.Provider value={opened}>{children}</PreviewCtaContext.Provider>
        </PreviewModeContext.Provider>
      </QueryClientProvider>
    </MemoryRouter>
  );
}
it.each(["guest", "personal"] as const)(
  "shows subscription and metadata links without personal actions for %s",
  (kind) => {
    const opened: string[] = [];
    render(
      <Harness kind={kind} opened={(id) => opened.push(id ?? "")}>
        <ActionBar
          contentId="movie"
          itemTitle="Movie"
          playHref="/watch/movie"
          onToggleWatched={() => {
            throw new Error("mutation");
          }}
          onToggleFavorite={() => {
            throw new Error("mutation");
          }}
          onDownload={() => {
            throw new Error("mutation");
          }}
          onShuffle={() => {
            throw new Error("mutation");
          }}
        />
      </Harness>,
    );
    expect(
      screen.queryByRole("button", { name: /favorite|watched|download|shuffle|rating/i }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Subscribe to watch" }));
    expect(opened).toEqual(["movie"]);
  },
);
it("puts the operator's wording where play would be", () => {
  const opened: string[] = [];
  render(
    <Harness itemCtaLabel="Start your €1 trial" opened={(id) => opened.push(id ?? "")}>
      <ActionBar contentId="movie" itemTitle="Movie" playHref="/watch/movie" />
    </Harness>,
  );
  expect(screen.queryByRole("button", { name: "Subscribe to watch" })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Start your €1 trial" }));
  expect(opened).toEqual(["movie"]);
});
it("removes play and favorite poster overlays", () => {
  render(
    <Harness>
      <CardPlayOverlay contentId="episode" title="Episode" />
      <PosterCardFavoriteButton
        isFavorite={false}
        isPending={false}
        onToggle={() => {
          throw new Error("mutation");
        }}
      />
    </Harness>,
  );
  expect(screen.queryByLabelText(/play episode|favorite/i)).not.toBeInTheDocument();
});
it("limits the card menu to its details link", () => {
  render(
    <Harness>
      <MediaItemMenu
        contentId="movie"
        mediaType="movie"
        quickActionMode="favorites"
        showFavoriteShortcut
        showWatchedShortcut
      />
    </Harness>,
  );
  fireEvent.pointerDown(screen.getByRole("button", { name: "More actions" }), {
    button: 0,
    ctrlKey: false,
  });
  expect(screen.getByRole("menuitem", { name: "Details" })).toHaveAttribute("href", "/item/movie");
  expect(
    screen.queryByRole("menuitem", { name: /favorite|watched|watchlist|collection/ }),
  ).not.toBeInTheDocument();
});
