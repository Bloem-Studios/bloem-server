// Bloem: AdminLayout behavior in organization and platform admin contexts.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AdminLayout from "./AdminLayout";

const mockFetch = vi.fn();

const mocks = vi.hoisted(() => ({
  useAdminServerStatus: vi.fn(),
  shortcutLabel: "Ctrl K",
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
  useOptionalAdminContext: () => ({ active: { scope: "organization" } }),
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
vi.mock("@/lib/keyboardShortcut", () => ({
  get SEARCH_SHORTCUT_LABEL() {
    return mocks.shortcutLabel;
  },
}));

function renderInQueryClient(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

beforeEach(() => {
  mocks.useAdminServerStatus.mockReturnValue({ data: { restart_required: true } });
  mocks.shortcutLabel = "Ctrl K";
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
    renderInQueryClient(
      <MemoryRouter initialEntries={["/admin/organization"]}>
        <AdminLayout />
      </MemoryRouter>,
    );

    expect(screen.queryByRole("button", { name: /^Server activity/ })).not.toBeInTheDocument();
    expect(mockFetch).not.toHaveBeenCalled();
  });

  it("closes the mobile sheet when a context switch succeeds", async () => {
    renderInQueryClient(
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
