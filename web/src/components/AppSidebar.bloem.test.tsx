// Bloem: Live TV in the app sidebar. The harness follows Silo's AppSidebar.test.tsx.
import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter, Route, Routes } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";

import AppSidebar from "./AppSidebar";

let mockLiveTVAccess = {
  data: { supported: true, allowed: false, available: false },
  isError: false,
};

vi.mock("@/api/v2/request", async () => ({
  ...(await vi.importActual<typeof import("@/api/v2/request")>("@/api/v2/request")),
  v2: vi.fn(),
}));
vi.mock("@/hooks/useAuth", () => {
  const useAuth = () => ({
    user: { id: 1, username: "alex", role: "admin" },
    logout: vi.fn(),
    clearProfile: vi.fn(),
  });
  return { useAuth, useOptionalAuth: useAuth };
});
vi.mock("@/hooks/useCurrentProfile", () => ({
  useCurrentProfile: () => ({ profile: { name: "Alex", requests_allowed: true } }),
}));
vi.mock("@/hooks/queries/profiles", () => ({
  useProfiles: () => ({ data: [], isSuccess: false, maxProfiles: null }),
}));
vi.mock("@/hooks/queries/libraries", () => ({
  useUserLibraries: () => ({ data: [{ id: 7, name: "Movies", type: "movies" }] }),
}));
vi.mock("@/hooks/queries/sidebarPins", () => ({
  useSidebarPins: () => ({ pins: {} }),
  useToggleSidebarPin: () => ({ togglePin: vi.fn(), canToggle: true }),
}));
vi.mock("@/hooks/useUICustomization", () => ({
  useUICustomization: () => ({
    primaryMenu: null,
    shortcuts: { items: [] },
    cardPresentation: { poster_size: "standard" },
    isLoading: false,
  }),
}));
vi.mock("@/hooks/queries/pluginSettings", () => ({
  usePluginSettingsList: () => ({ data: { installations: [] } }),
}));
vi.mock("@/hooks/queries/useRequests", () => ({
  useRequestFeatureStatus: () => ({ data: { requests_enabled: false } }),
  useMyMediaRequests: () => ({ data: undefined }),
}));
vi.mock("@/hooks/queries/useLiveTV", () => ({
  useLiveTVChannels: () => ({ data: [] }),
}));
vi.mock("@/hooks/queries/useLiveTVAccess", () => ({
  useLiveTVAccess: () => mockLiveTVAccess,
}));
vi.mock("@/hooks/queries/notifications", () => ({
  useUnreadNotificationCount: () => ({ data: 0 }),
}));
vi.mock("@/hooks/queries/notificationWebhooks", () => ({
  useNotificationCapability: () => ({ data: { in_app: { enabled: true } }, isError: false }),
}));
vi.mock("@/hooks/useViewTransition", () => ({
  useViewTransitionNavigate: () => vi.fn(),
}));
vi.mock("@/hooks/useServerBranding", () => ({
  useServerBranding: () => ({ serverName: "Bloem" }),
}));
vi.mock("@/components/ui/avatar", () => ({
  Avatar: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  AvatarFallback: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));
vi.mock("@/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DropdownMenuItem: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DropdownMenuLabel: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  DropdownMenuSeparator: () => <hr />,
}));

function renderSidebar(entry: string) {
  return renderToStaticMarkup(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={[entry]}>
        <Routes>
          <Route path="*" element={<AppSidebar />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("Live TV navigation permission", () => {
  it("hides a denied Live TV destination even on a direct link", () => {
    mockLiveTVAccess = {
      data: { supported: true, allowed: false, available: false },
      isError: false,
    };
    expect(renderSidebar("/livetv")).not.toContain('href="/livetv"');
  });
  it("shows Live TV when the viewer is allowed and channels are available", () => {
    mockLiveTVAccess = {
      data: { supported: true, allowed: true, available: true },
      isError: false,
    };
    expect(renderSidebar("/")).toContain('href="/livetv"');
  });
  it("hides Live TV when the latest permission check fails", () => {
    mockLiveTVAccess = { data: { supported: true, allowed: true, available: true }, isError: true };
    expect(renderSidebar("/")).not.toContain('href="/livetv"');
  });
});
