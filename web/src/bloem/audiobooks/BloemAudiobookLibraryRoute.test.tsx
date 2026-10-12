import { useEffect } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation, useSearchParams } from "react-router";

const mocks = vi.hoisted(() => ({
  libraries: {
    data: [] as Array<{ id: number; name: string; type: string }> | undefined,
    isLoading: false,
  },
  pageState: {
    ownerKey: "account:profile-a" as string | null,
    isLoading: false,
    preference: { libraries: {} as Record<string, { search: string }> },
    rememberEnabled: true,
    saveLibrarySearch: vi.fn(async () => undefined),
  },
  heroRendered: false,
  /** What the stand-in Silo LibraryPage restores on mount, like its saved-state hydration. */
  restoreTo: null as string | null,
}));

vi.mock("@/hooks/queries/libraries", () => ({
  useUserLibraries: () => mocks.libraries,
}));
vi.mock("@/hooks/queries/libraryPageState", () => ({
  useLibraryPageStatePreference: () => mocks.pageState,
}));
vi.mock("@/hooks/queries/shuffles", () => ({
  useStartShuffle: () => ({ startShuffle: vi.fn(), isStarting: false }),
}));
vi.mock("./BloemLibraryRecommended", () => ({
  default: function RecommendedStub({
    libraryId,
    onHeroStateChange,
  }: {
    libraryId: number;
    onHeroStateChange?: (rendered: boolean) => void;
  }) {
    useEffect(() => onHeroStateChange?.(mocks.heroRendered), [onHeroStateChange]);
    return <div data-testid="bloem-recommended" data-library={libraryId} />;
  },
}));

import BloemAudiobookLibraryRoute from "./BloemAudiobookLibraryRoute";

function SiloLibraryPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  useEffect(() => {
    if (mocks.restoreTo !== null) {
      const target = mocks.restoreTo;
      mocks.restoreTo = null;
      setSearchParams(new URLSearchParams(target), { replace: true });
    }
  }, [setSearchParams]);
  return (
    <div data-testid="silo-library-page" data-search={searchParams.toString()}>
      <button type="button" onClick={() => setSearchParams(new URLSearchParams())}>
        Silo home tab
      </button>
    </div>
  );
}

function LocationProbe() {
  return <output data-testid="search">{useLocation().search}</output>;
}

function renderRoute(path = "/library/5") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route
          path="/library/:libraryId"
          element={
            <>
              <BloemAudiobookLibraryRoute libraryPage={<SiloLibraryPage />} />
              <LocationProbe />
            </>
          }
        />
      </Routes>
    </MemoryRouter>,
  );
}

const audiobooks = { id: 5, name: "Audiobooks", type: "audiobooks" };
const movies = { id: 6, name: "Movies", type: "movies" };

beforeEach(() => {
  mocks.libraries = { data: [audiobooks, movies], isLoading: false };
  mocks.pageState = {
    ownerKey: "account:profile-a",
    isLoading: false,
    preference: { libraries: {} },
    rememberEnabled: true,
    saveLibrarySearch: vi.fn(async () => undefined),
  };
  mocks.heroRendered = false;
  mocks.restoreTo = null;
});

describe("BloemAudiobookLibraryRoute", () => {
  it("owns the home tab of an audiobook library", () => {
    renderRoute();
    expect(screen.getByTestId("bloem-recommended")).toHaveAttribute("data-library", "5");
    expect(screen.queryByTestId("silo-library-page")).toBeNull();
    expect(screen.getByRole("tab", { name: "Home" })).toHaveAttribute("data-state", "active");
    expect(document.title).toContain("Audiobooks");
  });

  it("delegates other libraries, loading and unavailable libraries to Silo's page", () => {
    renderRoute("/library/6");
    expect(screen.getByTestId("silo-library-page")).toBeInTheDocument();

    mocks.libraries = { data: undefined, isLoading: true };
    renderRoute();
    expect(screen.getAllByTestId("silo-library-page")).toHaveLength(2);

    mocks.libraries = { data: [movies], isLoading: false };
    renderRoute();
    expect(screen.getAllByTestId("silo-library-page")).toHaveLength(3);
  });

  it("delegates the library and collections tabs", () => {
    renderRoute("/library/5?tab=library&genre=Mystery");
    expect(screen.getByTestId("silo-library-page")).toBeInTheDocument();
    renderRoute("/library/5?tab=collections");
    expect(screen.getAllByTestId("silo-library-page")).toHaveLength(2);
  });

  it("lets Silo restore a saved tab first, then keeps the home tab once the listener returns", async () => {
    mocks.pageState.preference.libraries["5"] = { search: "tab=library&type=authors" };
    mocks.restoreTo = "tab=library&type=authors";
    renderRoute();

    expect(screen.getByTestId("silo-library-page")).toBeInTheDocument();
    expect(screen.getByTestId("search")).toHaveTextContent("?tab=library&type=authors");

    await userEvent.click(screen.getByRole("button", { name: "Silo home tab" }));
    expect(screen.getByTestId("bloem-recommended")).toBeInTheDocument();
    expect(mocks.pageState.saveLibrarySearch).toHaveBeenCalledWith(5, "");
  });

  it("waits for the saved page state before choosing", () => {
    mocks.pageState.isLoading = true;
    renderRoute();
    expect(screen.getByTestId("silo-library-page")).toBeInTheDocument();
  });

  it("does not restore when remembering is off", () => {
    mocks.pageState.rememberEnabled = false;
    mocks.pageState.preference.libraries["5"] = { search: "tab=library" };
    renderRoute();
    expect(screen.getByTestId("bloem-recommended")).toBeInTheDocument();
    expect(mocks.pageState.saveLibrarySearch).not.toHaveBeenCalled();
  });

  it("re-checks the saved state when the profile changes", () => {
    const view = renderRoute();
    expect(screen.getByTestId("bloem-recommended")).toBeInTheDocument();

    mocks.pageState = {
      ...mocks.pageState,
      ownerKey: "account:profile-b",
      preference: { libraries: { "5": { search: "tab=collections" } } },
    };
    mocks.restoreTo = "tab=collections";
    view.rerender(
      <MemoryRouter initialEntries={["/library/5"]}>
        <Routes>
          <Route
            path="/library/:libraryId"
            element={
              <>
                <BloemAudiobookLibraryRoute libraryPage={<SiloLibraryPage />} />
                <LocationProbe />
              </>
            }
          />
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByTestId("silo-library-page")).toBeInTheDocument();
  });

  it("switches to Silo's page for the other tabs", async () => {
    renderRoute();
    await userEvent.click(screen.getByRole("tab", { name: "Library" }));
    expect(screen.getByTestId("search")).toHaveTextContent("?tab=library");
    expect(screen.getByTestId("silo-library-page")).toBeInTheDocument();
  });

  it("normalizes the home tab URL like Silo", () => {
    renderRoute("/library/5?tab=recommended&genre=Mystery&from=home");
    expect(screen.getByTestId("search")).toHaveTextContent("?from=home");
    expect(screen.getByTestId("bloem-recommended")).toBeInTheDocument();
  });

  it("draws the header over the hero only when a hero rendered", async () => {
    mocks.heroRendered = true;
    const { container } = renderRoute();
    await act(async () => undefined);
    expect(container.querySelector(".library-marquee-header")).toHaveClass("is-overlay");
  });

  it("offers no shuffle, as Silo does for audiobook libraries", () => {
    renderRoute();
    expect(screen.queryByRole("button", { name: "Shuffle" })).toBeNull();
  });
});
