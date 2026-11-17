// Bloem: the admin sidebar in an organization administrator's context.
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AdminSidebar from "./AdminSidebar";

const mocks = vi.hoisted(() => ({
  scope: "organization" as "organization" | "platform",
  useAdminSessions: vi.fn((_enabled?: boolean) => ({ data: [{ id: "s1" }, { id: "s2" }] })),
  useBuildInfo: vi.fn((_enabled?: boolean) => ({
    data: undefined,
    isPending: true,
    isError: false,
  })),
}));

vi.mock("@/contexts/AdminContextProvider", () => {
  const context = () => ({ active: { key: mocks.scope, scope: mocks.scope } });
  return { useAdminContext: context, useOptionalAdminContext: context };
});
vi.mock("./admin/AdminContextSwitcher", () => ({
  default: () => <div>Context switcher</div>,
}));
vi.mock("@/hooks/useServerBranding", () => ({
  useServerBranding: () => ({ serverName: "Bloem", loginSubtitle: "" }),
}));
vi.mock("@/hooks/queries/admin/system", () => ({
  useBuildInfo: (enabled?: boolean) => mocks.useBuildInfo(enabled),
}));
vi.mock("@/hooks/queries/admin/stats", () => ({
  useAdminSessions: (enabled?: boolean) => mocks.useAdminSessions(enabled),
}));
vi.mock("@/hooks/queries/admin/plugins", () => ({
  useAdminPluginInstallations: () => ({ data: [] }),
}));
vi.mock("@/hooks/queries/admin/policy", () => ({
  usePolicyCapability: () => ({ data: { editor_available: true } }),
}));
vi.mock("@/hooks/queries/admin/requests", () => ({
  useAdminRequestCounts: () => ({ data: { needs_approval: 0 } }),
}));
vi.mock("@/hooks/queries/admin/settings", () => ({
  usePrefetchAdminServerSettings: () => vi.fn(),
}));
vi.mock("@/hooks/admin/useSettingsHealth", () => ({
  useSettingsAttention: () => [],
}));

function renderSidebar() {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={["/admin/organization"]}>
      <AdminSidebar />
    </MemoryRouter>,
  );
}

describe("AdminSidebar in an administrative context", () => {
  beforeEach(() => {
    mocks.useAdminSessions.mockClear();
    mocks.useBuildInfo.mockClear();
  });

  it("does not read platform sessions or the build route for an organization administrator", () => {
    mocks.scope = "organization";
    const markup = renderSidebar();
    expect(markup).toContain("Context switcher");
    expect(mocks.useAdminSessions).toHaveBeenCalledWith(false);
    expect(mocks.useAdminSessions).not.toHaveBeenCalledWith(true);
    expect(mocks.useBuildInfo).toHaveBeenCalledWith(false);
    expect(markup).not.toContain('href="/admin/plugins"');
    expect(markup).toContain('href="/admin/organization/storage"');
  });

  it("reads them in the platform context", () => {
    mocks.scope = "platform";
    const markup = renderSidebar();
    expect(mocks.useAdminSessions).toHaveBeenCalledWith(true);
    expect(mocks.useBuildInfo).toHaveBeenCalledWith(true);
    expect(markup).toContain('href="/admin/platform/organizations"');
  });
});
