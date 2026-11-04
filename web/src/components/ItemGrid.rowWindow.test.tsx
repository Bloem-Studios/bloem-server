import { createRef } from "react";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { BrowseItem } from "@/api/types";
import { resetScrollRestoration } from "@/lib/scrollRestoration";
import ItemGrid, { type ItemGridHandle } from "./ItemGrid";

// The real window virtualizer, with two 300px columns: a touch grid's window
// holds 1,200,000 / 300 = 4,000 rows, a desktop grid's 12,000,000 / 300 =
// 40,000.
vi.mock("@/hooks/useGridLayout", async () => {
  const { useRef } = await import("react");
  return {
    useGridLayout: () => ({
      containerRef: useRef<HTMLDivElement | null>(null),
      layout: { columnCount: 2, rowHeight: 300 },
    }),
  };
});
vi.mock("@/hooks/useOverlayPrefs", () => ({
  useOverlayPrefs: () => ({ prefs: {}, quickActionMode: "none" }),
}));
vi.mock("@/hooks/useUICustomization", () => ({
  useUICustomization: () => ({ cardPresentation: { poster_size: "standard" } }),
}));
vi.mock("./ItemCard", () => ({
  default: ({ item }: { item: BrowseItem }) => <div data-id={item.content_id}>card</div>,
}));

const PAGE_SIZE = 100;

function pagesAround(...pageIndexes: number[]) {
  return new Map(
    pageIndexes.map((page) => [
      page,
      Array.from(
        { length: PAGE_SIZE },
        (_, i) => ({ content_id: `item-${page * PAGE_SIZE + i}` }) as BrowseItem,
      ),
    ]),
  );
}

function scrollTo(top: number) {
  Object.defineProperty(window, "scrollY", { configurable: true, value: top });
  window.dispatchEvent(new Event("scroll"));
}

function usePointer(coarse: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: coarse && query === "(pointer: coarse)",
    media: query,
    onchange: null,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    addListener: () => undefined,
    removeListener: () => undefined,
    dispatchEvent: () => false,
  }));
}

function renderGrid(totalItems: number, pages: Map<number, BrowseItem[]>) {
  const gridRef = createRef<ItemGridHandle>();
  const onTopRowChange = vi.fn();
  const onVisibleRangeChange = vi.fn();
  const view = render(
    <ItemGrid
      gridRef={gridRef}
      totalItems={totalItems}
      pages={pages}
      pageSize={PAGE_SIZE}
      onVisibleRangeChange={onVisibleRangeChange}
      onTopRowChange={onTopRowChange}
    />,
  );
  const extent = () => (view.getByRole("list").parentElement as HTMLElement).style.height;
  return { ...view, gridRef, onTopRowChange, onVisibleRangeChange, extent };
}

describe("ItemGrid row window", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    vi.spyOn(window, "scrollTo").mockImplementation(
      (options: ScrollToOptions | number, y?: number) => {
        const top = typeof options === "number" ? y : options.top;
        if (top !== undefined && top !== window.scrollY) scrollTo(top);
      },
    );
    // The virtualizer clamps a scroll to the document's height.
    vi.spyOn(document.documentElement, "scrollHeight", "get").mockReturnValue(100_000_000);
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
      () => ({ top: -window.scrollY, height: 0 }) as DOMRect,
    );
    // Cards fill the 300px pitch exactly, so measuring never changes it.
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockImplementation(function (
      this: HTMLElement,
    ) {
      return this.dataset.id ? 287 : 0;
    });
    scrollTo(0);
  });
  afterEach(() => {
    cleanup();
    vi.clearAllTimers();
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    resetScrollRestoration();
    scrollTo(0);
  });

  it("keeps a desktop grid at the whole list's height", () => {
    usePointer(false);
    const { gridRef, extent } = renderGrid(60_000, pagesAround(0, 500));
    expect(extent()).toBe(`${30_000 * 300}px`);

    act(() => gridRef.current?.scrollToItem(50_001));

    expect(window.scrollY).toBe(25_000 * 300);
  });

  it("windows a desktop grid taller than a browser lets an element grow", () => {
    usePointer(false);
    const { gridRef, extent, onTopRowChange, container } = renderGrid(100_000, pagesAround(0, 800));
    expect(extent()).toBe(`${40_000 * 300}px`);

    act(() => gridRef.current?.scrollToItem(80_001));

    // Row 40,000 is in the list's last window, which starts at row 10,000.
    expect(window.scrollY).toBe(30_000 * 300);
    expect(onTopRowChange).toHaveBeenLastCalledWith(80_000, 80_001);
    expect(container.querySelector('[data-id="item-80000"]')).not.toBeNull();
  });

  it("jumps deep into a touch grid without scrolling the page deep", () => {
    usePointer(true);
    const { gridRef, extent, onTopRowChange, onVisibleRangeChange, container } = renderGrid(
      100_000,
      pagesAround(0, 799, 800, 801),
    );
    expect(extent()).toBe(`${4_000 * 300}px`);

    act(() => gridRef.current?.scrollToItem(80_001));

    // Row 40,000 lands in the middle of a window starting at row 38,000.
    expect(window.scrollY).toBe(2_000 * 300);
    expect(onTopRowChange).toHaveBeenLastCalledWith(80_000, 80_001);
    expect(onVisibleRangeChange.mock.lastCall?.[0]).toBeLessThanOrEqual(80_000);
    expect(onVisibleRangeChange.mock.lastCall?.[1]).toBeGreaterThan(80_001);
    expect(container.querySelector('[data-id="item-80000"]')).not.toBeNull();
    expect(extent()).toBe(`${4_000 * 300}px`);
  });

  it("moves the window on near its end, keeping the row on screen in place", () => {
    usePointer(true);
    const { gridRef, onTopRowChange, container } = renderGrid(100_000, pagesAround(821, 822, 823));
    act(() => gridRef.current?.scrollToItem(80_000));

    // 17px into the window's row 3,100 (row 41,100), in its last quarter.
    act(() => scrollTo(3_100 * 300 + 17));

    // The window now starts at row 40,000: the same row is its row 1,100.
    expect(window.scrollY).toBe(1_100 * 300 + 17);
    expect(onTopRowChange).toHaveBeenLastCalledWith(82_200, 82_201);
    expect(container.querySelector('[data-id="item-82200"]')).not.toBeNull();
  });

  it("puts the window back at the start of the list on a scroll to the top", () => {
    usePointer(true);
    const { gridRef, onTopRowChange, container } = renderGrid(100_000, pagesAround(0, 800));
    act(() => gridRef.current?.scrollToItem(80_000));
    expect(window.scrollY).toBe(2_000 * 300);

    act(() => scrollTo(0));

    expect(window.scrollY).toBe(0);
    expect(onTopRowChange).toHaveBeenLastCalledWith(0, 1);
    expect(container.querySelector('[data-id="item-0"]')).not.toBeNull();
  });

  it("keeps a short touch grid at its full height", () => {
    usePointer(true);
    const { gridRef, extent } = renderGrid(1_000, pagesAround(0, 1, 9));
    expect(extent()).toBe(`${500 * 300}px`);

    act(() => gridRef.current?.scrollToItem(901));

    expect(window.scrollY).toBe(450 * 300);
  });
});
