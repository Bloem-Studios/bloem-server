// Bloem: the watch host's player sits behind the pre-playback campaign.
import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";

import type { WatchPageProps } from "@/player/types";
import { WatchPage } from "./WatchPage";

const campaign = vi.hoisted(() => ({ props: [] as Array<Record<string, unknown>> }));

vi.mock("@/player/components/WatchPage", () => ({
  WatchPage: () => <div>Player</div>,
}));
vi.mock("@/components/engagement/CampaignPlacements", () => ({
  PrePlaybackCampaign: (props: {
    children: ReactNode;
    contentId: string;
    enabled: boolean;
    onCancel: () => void;
  }) => {
    campaign.props.push(props);
    return (
      <div>
        <button onClick={props.onCancel}>Skip campaign</button>
        {props.children}
      </div>
    );
  },
}));

function props(overrides: Partial<WatchPageProps>): WatchPageProps {
  return {
    contentId: "movie-1",
    playbackRequestKey: "request-1",
    displayMode: "foreground",
    onExit: vi.fn(),
    ...overrides,
  } as WatchPageProps;
}

it("puts the campaign in front of a foreground player and exits on cancel", () => {
  const onExit = vi.fn();
  render(<WatchPage {...props({ onExit })} />);
  expect(screen.getByText("Player")).toBeInTheDocument();
  expect(campaign.props.at(-1)).toMatchObject({ contentId: "movie-1", enabled: true });
  fireEvent.click(screen.getByRole("button", { name: "Skip campaign" }));
  expect(onExit).toHaveBeenCalledTimes(1);
});

it("keeps the campaign out of a detached or post-roll player", () => {
  render(<WatchPage {...props({ displayMode: "detached" })} />);
  expect(campaign.props.at(-1)).toMatchObject({ enabled: false });
  render(<WatchPage {...props({ displayMode: "postroll" })} />);
  expect(campaign.props.at(-1)).toMatchObject({ enabled: false });
});
