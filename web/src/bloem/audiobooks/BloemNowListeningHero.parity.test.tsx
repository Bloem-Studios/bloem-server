// Drift guard: the owned hero repeats Silo's NowListeningHero markup. Fed the
// full mixed item, it must render exactly what Silo's hero renders for the
// same item restricted to the chosen presentation. A Silo change to the hero
// fails this test until BloemNowListeningHero is brought back in line.
import { describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import type { ItemDetail, ResolvedSection } from "@/api/types";
import NowListeningHero from "@/components/NowListeningHero";
import { F1_LOCAL_ID, F1_WIRE, F7_CAPABILITIES, fixtureItem } from "./fixtures";
import { presentationChoiceKey } from "./presentationChoice";
import { itemAudiobookPresentations, itemForPresentation } from "./presentations";

const mocks = vi.hoisted(() => ({ detail: undefined as ItemDetail | undefined }));

vi.mock("@/hooks/queries/catalogRead", () => ({
  useCatalogItemDetail: () => ({ data: mocks.detail }),
}));
vi.mock("@/hooks/queries/useBloemCapabilities", () => ({
  useBloemCapabilities: () => ({ data: F7_CAPABILITIES, isPending: false }),
}));
vi.mock("@/hooks/useCurrentProfile", () => ({
  useCurrentProfile: () => ({ profile: { id: "profile-a" } }),
}));
vi.mock("@/hooks/useAmbientColor", () => ({ useAmbientColor: () => undefined }));
vi.mock("@/hooks/useOverlayPrefs", () => ({ useOverlayPrefs: () => ({ prefs: {} }) }));
vi.mock("@/pages/audiobooks/player/audiobookPlaybackContext", () => ({
  useAudiobookPlaybackController: () => null,
}));
// The rest-of-deck cards need the watch player; both heroes import the same card.
vi.mock("@/components/ContinueWatchingCard", () => ({
  default: ({ sectionItem }: { sectionItem: { content_id: string } }) => (
    <div data-card={sectionItem.content_id} />
  ),
}));

vi.mock("@/components/MediaCarousel", () => ({
  default: ({ title, children }: { title: string; children: React.ReactNode }) => (
    <section data-carousel={title}>{children}</section>
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
      poster_url: "/poster.jpg",
      position_seconds: 100,
      duration_seconds: 150,
    },
    {
      content_id: "other",
      type: "audiobook",
      title: "Another Book",
      position_seconds: 5,
      duration_seconds: 50,
    },
  ],
} as unknown as ResolvedSection;

function html(element: React.ReactElement, detail: ItemDetail) {
  mocks.detail = detail;
  const { container } = render(<MemoryRouter>{element}</MemoryRouter>);
  const markup = container.innerHTML;
  cleanup();
  return markup;
}

describe("BloemNowListeningHero parity with Silo's hero", () => {
  it.each([
    ["native (last_file_id)", null],
    ["remembered local", F1_LOCAL_ID],
  ])("renders Silo's markup for the %s presentation", (_, remembered) => {
    localStorage.clear();
    if (remembered) {
      localStorage.setItem(
        presentationChoiceKey({
          server: window.location.origin,
          profileId: "profile-a",
          contentId: "7301928374650001",
        }),
        remembered,
      );
    }
    const item = fixtureItem(F1_WIRE);
    const presentations = itemAudiobookPresentations(item);
    const chosen = presentations.find((entry) =>
      remembered ? entry.id === remembered : entry.native,
    );

    const silo = html(
      <NowListeningHero section={section} libraryId={7} />,
      itemForPresentation(item, chosen),
    );
    const bloem = html(<BloemNowListeningHero section={section} libraryId={7} />, item);

    expect(bloem).toBe(silo);
    expect(bloem).toContain(remembered ? "Chapter 2 of 2" : "Chapter 4 of 4");
  });
});
