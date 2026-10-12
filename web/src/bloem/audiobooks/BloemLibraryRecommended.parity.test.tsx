// Drift guard: BloemLibraryRecommended repeats Silo's LibraryRecommended and
// changes only which component draws an audiobook library's resume hero. For
// every page state the two must render the same markup, apart from that hero.
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ResolvedSection } from "@/api/types";
import LibraryRecommended from "@/pages/LibraryRecommended";

interface LayoutSection {
  id: string;
  section_type: string;
  title: string;
  featured: boolean;
  item_limit: number;
  is_custom: boolean;
  customized: boolean;
}

const mocks = vi.hoisted(() => ({
  layout: {
    data: undefined as { sections: LayoutSection[] } | undefined,
    isLoading: false,
    isError: false,
    error: null as unknown,
  },
  sections: {} as Record<string, ResolvedSection | Error>,
  hasItems: true,
}));

vi.mock("@/hooks/queries/sections", () => ({
  useLibraryLayout: () => ({ ...mocks.layout, refetch: vi.fn(), isFetching: false }),
  fetchLibrarySectionItems: async (_libraryId: number, sectionId: string) => {
    const section = mocks.sections[sectionId];
    if (section instanceof Error) throw section;
    return { section };
  },
}));
vi.mock("@/hooks/queries/catalog", () => ({
  useLibraryHasItems: () => ({ data: mocks.hasItems }),
}));
vi.mock("@/hooks/queries/libraryCollections", () => ({
  useLibraryCollectionItems: () => ({
    data: { items: [{ content_id: "pinned-1", title: "Pinned book" }], has_more: false },
    isLoading: false,
  }),
}));
vi.mock("@/hooks/queries/sidebarPins", () => ({
  useSidebarPins: () => ({ pins: { "7": [{ type: "collection", id: "c1", label: "Pinned" }] } }),
}));
vi.mock("@/pages/homeSurfaceRefresh", () => ({ useSectionRefreshSignal: () => ({ data: 0 }) }));
vi.mock("@/hooks/useOverlayPrefs", () => ({ useOverlayPrefs: () => ({ prefs: {} }) }));
vi.mock("@/hooks/useUICustomization", () => ({
  useUICustomization: () => ({ cardPresentation: { poster_size: "medium" } }),
}));
vi.mock("@/hooks/useIsActingAdmin", () => ({ useIsActingAdmin: () => false }));
vi.mock("@/components/MediaCarousel", () => ({
  default: ({
    title,
    loading,
    children,
  }: {
    title: string;
    loading?: boolean;
    children: ReactNode;
  }) => (
    <section data-carousel={title} data-loading={loading ? "yes" : "no"}>
      {children}
    </section>
  ),
}));
vi.mock("@/components/ItemCard", () => ({
  default: ({ item }: { item: { content_id: string } }) => <div data-card={item.content_id} />,
}));
vi.mock("@/components/SectionRow", () => ({
  default: ({ section }: { section: ResolvedSection }) => <div data-row={section.title} />,
}));
vi.mock("@/components/HeroBanner", () => ({
  default: ({ items }: { items: unknown[] }) => <div data-banner={items.length} />,
}));
vi.mock("@/components/LibraryEmptyState", () => ({
  default: () => <div data-empty-library="" />,
}));
vi.mock("@/components/PageUnavailable", () => ({
  default: ({ title }: { title: string }) => <div data-unavailable={title} />,
}));
vi.mock("@/components/RefreshFailedNotice", () => ({
  default: ({ message }: { message: string }) => <div data-refresh-failed={message} />,
}));
vi.mock("@/components/NowListeningHero", () => ({
  default: ({ section }: { section: ResolvedSection }) => (
    <div data-hero={section.items[0]?.content_id} data-owner="silo" />
  ),
}));
vi.mock("./BloemNowListeningHero", () => ({
  default: ({ section }: { section: ResolvedSection }) => (
    <div data-hero={section.items[0]?.content_id} data-owner="bloem" />
  ),
}));

import BloemLibraryRecommended from "./BloemLibraryRecommended";

function layoutSection(id: string, sectionType: string, featured = false): LayoutSection {
  return {
    id,
    section_type: sectionType,
    title: `${id} title`,
    featured,
    item_limit: 10,
    is_custom: false,
    customized: false,
  };
}

function resolved(id: string, sectionType: string, count: number): ResolvedSection {
  return {
    id,
    section_type: sectionType,
    title: `${id} title`,
    items: Array.from({ length: count }, (_, index) => ({
      content_id: `${id}-${index}`,
      type: "audiobook",
      title: `${id} ${index}`,
    })),
  } as unknown as ResolvedSection;
}

async function renderMarkup(
  Component: typeof LibraryRecommended,
  libraryType: string,
): Promise<{ markup: string; heroStates: boolean[] }> {
  const heroStates: boolean[] = [];
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <Component
          libraryId={7}
          libraryType={libraryType}
          onHeroStateChange={(rendered) => heroStates.push(rendered)}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  // Settled once no section is still loading.
  await waitFor(() => {
    expect(container.querySelector('[data-loading="yes"]')).toBeNull();
  });
  await new Promise((resolve) => setTimeout(resolve, 0));
  const markup = container.innerHTML;
  cleanup();
  return { markup, heroStates };
}

async function compare(libraryType: string) {
  const silo = await renderMarkup(LibraryRecommended, libraryType);
  const bloem = await renderMarkup(BloemLibraryRecommended, libraryType);
  expect(bloem.markup.replaceAll('data-owner="bloem"', 'data-owner="silo"')).toBe(silo.markup);
  expect(bloem.heroStates).toEqual(silo.heroStates);
  return bloem.markup;
}

afterEach(() => {
  mocks.layout = { data: undefined, isLoading: false, isError: false, error: null };
  mocks.sections = {};
  mocks.hasItems = true;
});

describe("BloemLibraryRecommended parity with Silo's LibraryRecommended", () => {
  it("swaps only the audiobook resume hero", async () => {
    mocks.layout.data = {
      sections: [
        layoutSection("continue", "continue_watching", true),
        layoutSection("recent", "recently_added"),
        layoutSection("broken", "recently_added"),
        layoutSection("nothing", "recently_added"),
      ],
    };
    mocks.sections = {
      continue: resolved("continue", "continue_watching", 2),
      recent: resolved("recent", "recently_added", 3),
      broken: new Error("section failed"),
      nothing: resolved("nothing", "recently_added", 0),
    };

    const markup = await compare("audiobooks");
    expect(markup).toContain('data-owner="bloem"');
    expect(markup).toContain('data-row="recent title"');
    expect(markup).toContain('data-carousel="Pinned"');
  });

  it("keeps the cinematic banner for other featured sections", async () => {
    mocks.layout.data = { sections: [layoutSection("recent", "recently_added", true)] };
    mocks.sections = { recent: resolved("recent", "recently_added", 2) };

    expect(await compare("audiobooks")).toContain('data-banner="2"');
  });

  it("matches the layout failure and empty-library states", async () => {
    mocks.layout = { data: undefined, isLoading: false, isError: true, error: new Error("down") };
    expect(await compare("audiobooks")).toContain("data-unavailable");

    mocks.layout = {
      data: { sections: [layoutSection("nothing", "recently_added")] },
      isLoading: false,
      isError: false,
      error: null,
    };
    mocks.sections = { nothing: resolved("nothing", "recently_added", 0) };
    mocks.hasItems = false;
    expect(await compare("audiobooks")).toContain("data-empty-library");
  });
});
