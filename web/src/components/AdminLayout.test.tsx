import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, MemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AdminLayout from "./AdminLayout";

const mockFetch = vi.fn();

const mocks = vi.hoisted(() => ({
  useAdminServerStatus: vi.fn(),
}));

vi.mock("@/components/AdminSidebar", () => ({
  default: ({ embedded, onNavigate }: { embedded?: boolean; onNavigate?: () => void }) =>
    embedded ? (
      <button type="button" onClick={onNavigate}>
        Complete context switch
      </button>
    ) : null,
}));

vi.mock("@/contexts/AdminContextProvider", () => ({
  useAdminContext: () => ({ active: { scope: "organization" } }),
}));
vi.mock("@/hooks/useDocumentTitle", () => ({ useDocumentTitle: vi.fn() }));
vi.mock("@/lib/documentTitle", () => ({ resolveAdminDocumentTitle: () => "Admin" }));
vi.mock("@/hooks/queries/admin/settings", () => ({
  useAdminServerStatus: () => mocks.useAdminServerStatus(),
}));
vi.mock("@/hooks/queries/admin/plugins", () => ({
  useAdminPluginInstallations: () => ({ data: undefined }),
}));
vi.mock("@/hooks/queries/admin/policy", () => ({
  usePolicyCapability: () => ({ data: undefined }),
}));
vi.mock("@/components/AdminSectionCommandDialog", () => ({
  AdminSectionCommandDialog: () => null,
}));
vi.mock("@/playback/watchPlaybackContext", () => ({
  useWatchPlaybackController: () => ({ isBackgroundBarVisible: false }),
}));
vi.mock("@/pages/audiobooks/player/audiobookPlaybackContext", () => ({
  useAudiobookPlaybackController: () => null,
}));

// The dashboard and the users page stand in for "any admin page that is not
// settings" — the shell is the only thing that renders the restart prompt, so
// both must show it.
function renderAdmin(initialPath = "/admin") {
  const router = createMemoryRouter(
    [
      {
        path: "/admin",
        element: <AdminLayout />,
        children: [
          { index: true, element: <h1>Admin dashboard</h1> },
          { path: "users", element: <h1>Admin users</h1> },
        ],
      },
    ],
    { initialEntries: [initialPath] },
  );

  return { router, ...render(<RouterProvider router={router} />) };
}

beforeEach(() => {
  mocks.useAdminServerStatus.mockReturnValue({ data: { restart_required: true } });
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: query === "(min-width: 64rem)",
    media: query,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("AdminLayout mobile navigation", () => {
  beforeEach(() => {
    mockFetch.mockReset();
    vi.stubGlobal("fetch", mockFetch);
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      value: vi.fn().mockReturnValue({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    });
  });

  it("does not mount server activity or execute its legacy hooks in organization scope", () => {
    render(
      <MemoryRouter initialEntries={["/admin/organization"]}>
        <AdminLayout />
      </MemoryRouter>,
    );

    expect(screen.queryByRole("button", { name: /^Server activity/ })).not.toBeInTheDocument();
    expect(mockFetch).not.toHaveBeenCalled();
  });

  it("closes the mobile sheet when a context switch succeeds", async () => {
    render(
      <MemoryRouter initialEntries={["/admin"]}>
        <AdminLayout />
      </MemoryRouter>,
    );

    await userEvent.click(screen.getByRole("button", { name: "Open admin navigation" }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Complete context switch" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });
});

describe("AdminLayout search shortcut hint", () => {
  function stubUserAgent(value: string) {
    vi.spyOn(window.navigator, "userAgent", "get").mockReturnValue(value);
  }

  // The dialog opens on Cmd or Ctrl, so the advertised hint has to name the key
  // this keyboard actually has — a hardcoded ⌘ is a dead instruction on Windows
  // and Linux, which is most self-hosters.
  it("names Ctrl off Apple platforms", () => {
    stubUserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36");
    renderAdmin();

    const [search] = screen.getAllByRole("button", { name: "Search admin sections" });
    expect(search).toHaveAttribute("title", "Search admin sections (Ctrl K)");
    expect(screen.getByText("Ctrl K")).toBeInTheDocument();
    expect(screen.queryByText(/⌘/)).not.toBeInTheDocument();
  });

  it("names the command glyph on Apple platforms", () => {
    stubUserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15");
    renderAdmin();

    const [search] = screen.getAllByRole("button", { name: "Search admin sections" });
    expect(search).toHaveAttribute("title", "Search admin sections (⌘ K)");
    expect(screen.getByText("⌘ K")).toBeInTheDocument();
  });
});

describe("AdminLayout restart banner", () => {
  it("stays quiet while no restart is owed", () => {
    mocks.useAdminServerStatus.mockReturnValue({ data: { restart_required: false } });
    renderAdmin();

    expect(screen.getByRole("heading", { name: "Admin dashboard" })).toBeInTheDocument();
    expect(screen.queryByText("Restart required")).not.toBeInTheDocument();
  });

  it("prompts on a page outside settings, above the routed page", () => {
    renderAdmin("/admin/users");

    const banner = screen.getByRole("status");
    const page = screen.getByRole("heading", { name: "Admin users" });

    expect(banner).toHaveTextContent("Restart required");
    // Node.DOCUMENT_POSITION_FOLLOWING: the page comes after the banner.
    expect(banner.compareDocumentPosition(page) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("keeps a dismissal across admin navigation", async () => {
    const { router } = renderAdmin();

    await userEvent.click(screen.getByRole("button", { name: "Later" }));
    expect(screen.queryByText("Restart required")).not.toBeInTheDocument();

    // The shell owns the banner, so moving between admin pages neither
    // resurrects the prompt nor loses the admin's "Later".
    await act(async () => {
      await router.navigate("/admin/users");
    });

    expect(screen.getByRole("heading", { name: "Admin users" })).toBeInTheDocument();
    expect(screen.queryByText("Restart required")).not.toBeInTheDocument();
  });
});
