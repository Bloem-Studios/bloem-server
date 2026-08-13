import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AdminLayout from "./AdminLayout";

vi.mock("@/components/AdminSidebar", () => ({
  default: ({ embedded, onNavigate }: { embedded?: boolean; onNavigate?: () => void }) =>
    embedded ? (
      <button type="button" onClick={onNavigate}>
        Complete context switch
      </button>
    ) : null,
}));

vi.mock("@/components/ServerActivity", () => ({ default: () => null }));
vi.mock("@/hooks/useDocumentTitle", () => ({ useDocumentTitle: vi.fn() }));
vi.mock("@/lib/documentTitle", () => ({ resolveAdminDocumentTitle: () => "Admin" }));
vi.mock("@/playback/watchPlaybackContext", () => ({
  useWatchPlaybackController: () => ({ isBackgroundBarVisible: false }),
}));
vi.mock("@/pages/audiobooks/player/audiobookPlaybackContext", () => ({
  useAudiobookPlaybackController: () => null,
}));

describe("AdminLayout mobile navigation", () => {
  beforeEach(() => {
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      value: vi.fn().mockReturnValue({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    });
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
