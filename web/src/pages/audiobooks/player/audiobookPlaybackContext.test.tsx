import { fireEvent, render, screen, act, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import type { AudiobookPlayerProps } from "./AudiobookPlayer";
import {
  AudiobookPlaybackProvider,
  useAudiobookPlaybackController,
} from "./audiobookPlaybackContext";

const identity = vi.hoisted(() => ({ current: true, account: 1 }));
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({ user: { id: identity.account } }) }));
vi.mock("@/hooks/useCurrentProfile", () => ({
  useCurrentProfile: () => ({ profile: { id: "profile" } }),
}));
vi.mock("@/api/client", async (original) => ({
  ...(await original<object>()),
  captureProfileRequestContext: () => ({
    profileId: "profile",
    serverOrigin: "http://localhost:3000",
    account: identity.account,
  }),
  isCapturedProfileAuthorityActive: (context: { account: number }) =>
    identity.current && context.account === identity.account,
}));
vi.mock("@/player/initial-v2", () => ({
  initialPlaybackCapabilities: async () => {
    throw new Error("unavailable");
  },
  offerPendingInitialStart: vi.fn(),
}));
beforeEach(() => {
  identity.current = true;
  identity.account = 1;
});

const playerModule = vi.hoisted(() => {
  let resolve!: () => void;
  return {
    requested: vi.fn(),
    ready: new Promise<void>((done) => {
      resolve = done;
    }),
    resolve: () => resolve(),
  };
});

vi.mock("./AudiobookPlayer", async () => {
  playerModule.requested();
  await playerModule.ready;
  return {
    default: ({ title, onClose }: AudiobookPlayerProps) => (
      <div aria-label="Audiobook player">
        {title}
        <button onClick={onClose}>Close player</button>
      </div>
    ),
  };
});

function PlaybackControls() {
  const playback = useAudiobookPlaybackController()!;
  return (
    <>
      <input aria-label="Library search" defaultValue="Unchanged page" />
      <button
        onClick={() =>
          playback.startPlayback({ contentId: "book-1", title: "First book", files: [] })
        }
      >
        Start audiobook
      </button>
      <button onClick={playback.stopPlayback}>Stop audiobook</button>
    </>
  );
}

it("loads the player on demand, preserves the page while pending, and honors cancellation", async () => {
  render(
    <AudiobookPlaybackProvider>
      <PlaybackControls />
    </AudiobookPlaybackProvider>,
  );
  const search = screen.getByRole("textbox", { name: "Library search" });
  fireEvent.change(search, { target: { value: "My search" } });
  expect(playerModule.requested).not.toHaveBeenCalled();

  fireEvent.click(screen.getByRole("button", { name: "Start audiobook" }));
  await waitFor(() => expect(playerModule.requested).toHaveBeenCalledOnce());
  expect(search).toBeVisible();
  expect(search).toHaveValue("My search");
  expect(screen.queryByLabelText("Audiobook player")).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Stop audiobook" }));
  await act(async () => playerModule.resolve());
  expect(screen.queryByLabelText("Audiobook player")).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Start audiobook" }));
  expect(await screen.findByLabelText("Audiobook player")).toHaveTextContent("First book");
  expect(screen.getByRole("textbox", { name: "Library search" })).toBe(search);
  expect(search).toHaveValue("My search");
  fireEvent.click(screen.getByRole("button", { name: "Close player" }));
  expect(screen.queryByLabelText("Audiobook player")).not.toBeInTheDocument();
});

it("unmounts the old player on account switch and requires a new request", async () => {
  playerModule.resolve();
  const { rerender } = render(
    <AudiobookPlaybackProvider>
      <PlaybackControls />
    </AudiobookPlaybackProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Start audiobook" }));
  expect(await screen.findByLabelText("Audiobook player")).toBeVisible();
  identity.account = 2;
  rerender(
    <AudiobookPlaybackProvider>
      <PlaybackControls />
    </AudiobookPlaybackProvider>,
  );
  expect(screen.queryByLabelText("Audiobook player")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Start audiobook" }));
  expect(await screen.findByLabelText("Audiobook player")).toBeVisible();
});
