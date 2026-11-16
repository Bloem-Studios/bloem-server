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
  // What each page passed to the layout and section reads, compared below.
  layoutOptions: [] as unknown[],
  sectionReads: [] as string[],
}));

vi.mock("@/hooks/queries/sections", () => ({
  useLibraryLayout: (_libraryId: number, options?: unknown) => {
    mocks.layoutOptions.push(options);
    return { ...mocks.layout, refetch: vi.fn(), isFetching: false };
  },
  fetchLibrarySectionItems: async (
    _libraryId: number,
    sectionId: string,
    options?: { featured?: boolean },
  ) => {
    mocks.sectionReads.push(`${sectionId} featured=${options?.featured === true}`);
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
// The collection's current title, which differs from the label saved on the pin.
vi.mock("@/hooks/queries/pinLabels", () => ({
  usePinLabel: () => (pin: { label: string }) => `${pin.label} (renamed)`,
}));
// Recently added rows drop items an earlier row already shows.
vi.mock("@/lib/recipes", () => ({
  fetchRecipeCatalog: async () => ({
    categories: { discovery: [{ type: "recently_added", avoid_duplicates: true }] },
  }),
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
  default: ({ section, pinLabel }: { section: ResolvedSection; pinLabel?: string }) => (
    <div data-row={section.title} data-cards={section.items.length} data-pin-label={pinLabel} />
  ),
}));
vi.mock("@/components/HeroBanner", () => ({
  default: ({ items, onRefreshItems }: { items: unknown[]; onRefreshItems?: () => void }) => (
    <div data-banner={items.length} data-refreshable={onRefreshItems ? "yes" : "no"} />
  ),
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
    title: `${id} layout title`,
    featured,
    item_limit: 10,
    is_custom: false,
    customized: false,
  };
}

function resolved(
  id: string,
  sectionType: string,
  count: number,
  itemPrefix = id,
): ResolvedSection {
  return {
    id,
    section_type: sectionType,
    title: `${id} title`,
    total_count: count,
    items: Array.from({ length: count }, (_, index) => ({
      content_id: `${itemPrefix}-${index}`,
      type: "audiobook",
      title: `${id} ${index}`,
    })),
  } as unknown as ResolvedSection;
}

// The distinct options a page passed, in first-use order. How many times a hook
// ran depends on how many renders the page made before it settled, which moves
// with scheduling; what the page asked for does not.
function distinctOptions(calls: unknown[]): unknown[] {
  const seen = new Set<string>();
  return calls.filter((options) => {
    const key = JSON.stringify(options) ?? "undefined";
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

async function renderMarkup(
  Component: typeof LibraryRecommended,
  libraryType: string,
  ready?: string,
): Promise<{
  markup: string;
  heroStates: boolean[];
  layoutOptions: unknown[];
  sectionReads: string[];
}> {
  const heroStates: boolean[] = [];
  mocks.layoutOptions = [];
  mocks.sectionReads = [];
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
    if (ready) expect(container.querySelector(ready)).not.toBeNull();
  });
  await new Promise((resolve) => setTimeout(resolve, 0));
  const markup = container.innerHTML;
  cleanup();
  return {
    markup,
    heroStates,
    layoutOptions: distinctOptions(mocks.layoutOptions),
    sectionReads: [...mocks.sectionReads].sort(),
  };
}

async function compare(libraryType: string, ready?: string) {
  const silo = await renderMarkup(LibraryRecommended, libraryType, ready);
  const bloem = await renderMarkup(BloemLibraryRecommended, libraryType, ready);
  expect(bloem.markup.replaceAll('data-owner="bloem"', 'data-owner="silo"')).toBe(silo.markup);
  expect(bloem.heroStates).toEqual(silo.heroStates);
  // Both pages must have read the layout, and asked for the same thing.
  expect(silo.layoutOptions.length).toBeGreaterThan(0);
  expect(bloem.layoutOptions).toEqual(silo.layoutOptions);
  expect(bloem.sectionReads).toEqual(silo.sectionReads);
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
    expect(markup).toContain('data-carousel="Pinned (renamed)"');
  });

  it("keeps the cinematic banner for other featured sections", async () => {
    mocks.layout.data = { sections: [layoutSection("recent", "recently_added", true)] };
    mocks.sections = { recent: resolved("recent", "recently_added", 2) };

    const markup = await compare("audiobooks");
    expect(markup).toContain('data-banner="2"');
    expect(markup).toContain('data-refreshable="yes"');
  });

  it("drops a later row's items that an earlier row already shows", async () => {
    mocks.layout.data = {
      sections: [
        layoutSection("first", "recently_added"),
        layoutSection("second", "recently_added"),
      ],
    };
    mocks.sections = {
      first: resolved("first", "recently_added", 2, "shared"),
      second: resolved("second", "recently_added", 3, "shared"),
    };

    const markup = await compare("audiobooks", '[data-row="second title"][data-cards="1"]');
    expect(markup).toContain('data-pin-label="second layout title"');
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
