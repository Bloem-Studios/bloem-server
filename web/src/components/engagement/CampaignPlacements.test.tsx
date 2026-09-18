// @vitest-environment jsdom
import { useEffect } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { PrePlaybackCampaign } from "./CampaignPlacements";
import type { CampaignCard } from "./campaigns";
const delivery = vi.hoisted(() => ({ pending: true, eligible: true, cards: [] as CampaignCard[] }));
vi.mock("./useCampaigns", () => ({
  useCampaigns: () => ({
    query: { isPending: delivery.pending },
    eligible: delivery.eligible,
    cards: delivery.cards,
    dismiss: vi.fn(),
  }),
}));
beforeEach(() => {
  delivery.pending = true;
  delivery.eligible = true;
  delivery.cards = [];
});
afterEach(cleanup);
it("keeps continue available immediately while delivery is pending", () => {
  render(
    <PrePlaybackCampaign contentId="movie" enabled onCancel={vi.fn()}>
      <div>Media engine</div>
    </PrePlaybackCampaign>,
  );
  expect(screen.queryByText("Media engine")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Continue to content" }));
  expect(screen.getByText("Media engine")).toBeInTheDocument();
});
it("never unmounts an admitted player for a late or refetched campaign", () => {
  const mounted = vi.fn(),
    unmounted = vi.fn();
  function Player() {
    useEffect(() => {
      mounted();
      return unmounted;
    }, []);
    return <div>Media engine</div>;
  }
  const tree = () => (
    <PrePlaybackCampaign contentId="movie" enabled onCancel={vi.fn()}>
      <Player />
    </PrePlaybackCampaign>
  );
  delivery.pending = false;
  const view = render(tree());
  expect(mounted).toHaveBeenCalledTimes(1);
  delivery.cards = [
    {
      id: "late",
      headline: "Late campaign",
      image_url: "https://cdn.example/image.webp",
      expires_at: "2099-01-01T00:00:00Z",
      dismissible: true,
    },
  ];
  view.rerender(tree());
  expect(screen.queryByRole("region", { name: "Before playback" })).toBeNull();
  expect(mounted).toHaveBeenCalledTimes(1);
  expect(unmounted).not.toHaveBeenCalled();
});
it("bypasses the interstitial for ineligible viewers", () => {
  delivery.eligible = false;
  render(
    <PrePlaybackCampaign contentId="movie" enabled onCancel={vi.fn()}>
      <div>Media engine</div>
    </PrePlaybackCampaign>,
  );
  expect(screen.getByText("Media engine")).toBeInTheDocument();
  expect(screen.queryByText("Before you watch")).toBeNull();
});
