import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import type { ItemDetail, ResolvedSection } from "@/api/types";
import type { AudiobookFile } from "@/lib/audiobooks/types";
import { F1_LOCAL_ID, F1_WIRE, F7_CAPABILITIES, fixtureItem } from "./fixtures";
import { presentationChoiceKey } from "./presentationChoice";

const mocks = vi.hoisted(() => ({
  detail: undefined as ItemDetail | undefined,
  capabilities: { data: undefined as { feature_tokens: string[] } | undefined, isPending: false },
  startPlayback: vi.fn(),
}));

vi.mock("@/hooks/queries/catalogRead", () => ({
  useCatalogItemDetail: () => ({ data: mocks.detail }),
}));
vi.mock("@/hooks/queries/useBloemCapabilities", () => ({
  useBloemCapabilities: (enabled: boolean) =>
    enabled ? mocks.capabilities : { data: undefined, isPending: true },
}));
vi.mock("@/hooks/useCurrentProfile", () => ({
  useCurrentProfile: () => ({ profile: { id: "profile-a" } }),
}));
vi.mock("@/hooks/useAmbientColor", () => ({ useAmbientColor: () => undefined }));
vi.mock("@/hooks/useOverlayPrefs", () => ({ useOverlayPrefs: () => ({ prefs: {} }) }));
vi.mock("@/pages/audiobooks/player/audiobookPlaybackContext", () => ({
  useAudiobookPlaybackController: () => ({
    active: null,
    activeRequest: null,
    isBackgroundBarVisible: false,
    startPlayback: mocks.startPlayback,
    stopPlayback: vi.fn(),
    toggleActivePlayback: vi.fn(),
  }),
}));
vi.mock("@/components/NowListeningHero", () => ({
  default: ({ section }: { section: ResolvedSection }) => (
    <div data-testid="silo-now-listening">{section.items[0]?.content_id}</div>
  ),
}));

import BloemNowListeningHero from "./BloemNowListeningHero";

const section = {
  id: "continue",
  section_type: "continue_watching",
  title: "Continue listening",
  items: [
    {
      content_id: "7301928374650001",
      type: "audiobook",
      title: "The Example Book",
      position_seconds: 100,
      duration_seconds: 150,
    },
  ],
} as unknown as ResolvedSection;

function renderHero() {
  return render(
    <MemoryRouter>
      <BloemNowListeningHero section={section} libraryId={7} />
    </MemoryRouter>,
  );
}

function started() {
  const input = mocks.startPlayback.mock.lastCall?.[0] as {
    files: AudiobookFile[];
    initialPositionSeconds: number;
  };
  return { ids: input.files.map((file) => file.id), at: input.initialPositionSeconds };
}

beforeEach(() => {
  localStorage.clear();
  mocks.detail = fixtureItem(F1_WIRE);
  mocks.capabilities = { data: F7_CAPABILITIES, isPending: false };
  mocks.startPlayback.mockClear();
});

describe("BloemNowListeningHero", () => {
  it("resumes the chosen presentation from the library hero", async () => {
    renderHero();

    expect(screen.queryByTestId("silo-now-listening")).toBeNull();
    expect(screen.getByText("Chapter 4 of 4 · Chapter 04")).toBeInTheDocument();
    expect(screen.getByText("1 min left")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("link", { name: "Resume" }));
    expect(started()).toEqual({ ids: [601, 602], at: 100 });
  });

  it("resumes the remembered local presentation instead", async () => {
    localStorage.setItem(
      presentationChoiceKey({
        server: window.location.origin,
        profileId: "profile-a",
        contentId: "7301928374650001",
      }),
      F1_LOCAL_ID,
    );
    renderHero();

    expect(screen.getByText("Chapter 2 of 2 · 02 - Middle")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("link", { name: "Resume" }));
    expect(started()).toEqual({ ids: [501, 502], at: 100 });
  });

  it("falls through to the item page while the capability check is pending", async () => {
    mocks.capabilities = { data: undefined, isPending: true };
    renderHero();

    const resume = screen.getByRole("link", { name: "Resume" });
    expect(resume).toHaveAttribute("href", "/item/7301928374650001?libraryId=7&play=1");
    await userEvent.click(resume);
    expect(mocks.startPlayback).not.toHaveBeenCalled();
  });

  it("leaves an ordinary audiobook to Silo's hero", () => {
    const item = fixtureItem(F1_WIRE);
    mocks.detail = { ...item, versions: item.versions.slice(0, 2) };
    renderHero();
    expect(screen.getByTestId("silo-now-listening")).toHaveTextContent("7301928374650001");
  });

  it("leaves the hero to Silo until the deck's detail has loaded", () => {
    mocks.detail = undefined;
    renderHero();
    expect(screen.getByTestId("silo-now-listening")).toBeInTheDocument();
  });

  it("ignores a detail payload for another book", () => {
    mocks.detail = { ...fixtureItem(F1_WIRE), content_id: "another-book" };
    renderHero();
    expect(screen.getByTestId("silo-now-listening")).toBeInTheDocument();
  });
});
