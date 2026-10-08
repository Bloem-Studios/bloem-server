import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AdminSidebar from "./AdminSidebar";
import type { BuildInfo } from "@/hooks/queries/admin/system";
import type { AdminContextSummary } from "@/api/bloemTypes";

interface MockBuildInfoResult {
  data?: BuildInfo;
  isPending: boolean;
  isError: boolean;
}

const mockUseServerBranding = vi.fn(() => ({
  serverName: "Silo",
  loginSubtitle: "Sign in with an existing account.",
}));
const defaultBuildInfo: BuildInfo = {
  display: "b4c5aae1+dirty",
  revision: "b4c5aae18aa653725ac697b29a05eac797576008",
  dirty: true,
  vcs_time: "2026-04-05T22:24:40Z",
  build_number: 411,
  built_at: "2026-08-19T19:45:00Z",
  available: true,
};
const mockUseBuildInfo = vi.fn<(_enabled?: boolean) => MockBuildInfoResult>(() => ({
  data: defaultBuildInfo,
  isPending: false,
  isError: false,
}));
const mockUseAdminSessions = vi.fn((_enabled?: boolean) => ({ data: [] }));
const mockUseAdminPluginInstallations = vi.fn((_enabled?: boolean) => ({ data: [] }));
const mockUseAdminRequestCounts = vi.fn<() => { data?: { needs_approval: number } }>(() => ({
  data: { needs_approval: 0 },
}));
const mockUsePolicyCapability = vi.fn((_enabled?: boolean) => ({
  data: {
    enabled: true,
    editor_available: true,
    decision_types: [],
    generation: 1,
  },
}));
const mockAdminContext = vi.fn<() => { active: AdminContextSummary | null }>(() => ({
  active: {
    key: "platform" as const,
    scope: "platform" as const,
    name: "Platform",
    status: "active" as const,
    authority: "platform_admin" as const,
    policyRevision: 0,
    securityRevision: 0,
  },
}));

vi.mock("@/contexts/AdminContextProvider", () => ({
  useAdminContext: () => mockAdminContext(),
}));

vi.mock("@/components/admin/AdminContextSwitcher", () => ({
  default: ({ onSwitchSuccess }: { onSwitchSuccess?: () => void }) => (
    <button type="button" onClick={onSwitchSuccess}>
      Administrative context switcher
    </button>
  ),
}));

vi.mock("@/hooks/useServerBranding", () => ({
  useServerBranding: () => mockUseServerBranding(),
}));

vi.mock("@/hooks/queries/admin/system", () => ({
  useBuildInfo: (enabled?: boolean) => mockUseBuildInfo(enabled),
}));

vi.mock("@/hooks/queries/admin/stats", () => ({
  useAdminSessions: (enabled?: boolean) => mockUseAdminSessions(enabled),
}));

vi.mock("@/hooks/queries/admin/plugins", () => ({
  useAdminPluginInstallations: (enabled?: boolean) => mockUseAdminPluginInstallations(enabled),
}));

vi.mock("@/hooks/queries/admin/policy", () => ({
  usePolicyCapability: (enabled?: boolean) => mockUsePolicyCapability(enabled),
}));

vi.mock("@/hooks/queries/admin/requests", () => ({
  useAdminRequestCounts: () => mockUseAdminRequestCounts(),
}));

function renderSidebar(embedded = false) {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={["/admin"]}>
      <AdminSidebar embedded={embedded} />
    </MemoryRouter>,
  );
}

describe("AdminSidebar", () => {
  beforeEach(() => {
    mockUsePolicyCapability.mockReturnValue({
      data: {
        enabled: true,
        editor_available: true,
        decision_types: [],
        generation: 1,
      },
    });
  });

  it("shows how many requests need approval on the Requests entry", () => {
    mockUseAdminRequestCounts.mockReturnValueOnce({ data: { needs_approval: 3 } });
    const markup = renderSidebar();
    const requestsLink = markup.match(/<a[^>]*href="\/admin\/requests"[^>]*>.*?<\/a>/)?.[0];

    expect(requestsLink).toContain('<span aria-hidden="true">3</span>');
    expect(requestsLink).toContain(", 3 need approval");
  });

  it("leaves the Requests entry plain when nothing needs approval", () => {
    const markup = renderSidebar();
    const requestsLink = markup.match(/<a[^>]*href="\/admin\/requests"[^>]*>.*?<\/a>/)?.[0];

    expect(requestsLink).toBeDefined();
    expect(requestsLink).not.toContain("need approval");
  });

  it("renders the grouped navigation sections", () => {
    const markup = renderSidebar();

    for (const section of ["Overview", "Content", "Automation", "Users", "Settings", "System"]) {
      expect(markup).toContain(`>${section}<`);
    }
    expect(markup).toContain('href="/admin/platform/organizations"');
    expect(markup).toContain('href="/admin/platform/direct-accounts"');
    expect(markup).not.toContain('href="/admin/organization/people"');
  });

  it("places the administrative context switcher above navigation", () => {
    const markup = renderSidebar();

    expect(markup.indexOf("Administrative context switcher")).toBeLessThan(
      markup.indexOf('aria-label="Admin navigation"'),
    );
  });

  it("shows organization navigation without platform links in organization context", () => {
    mockAdminContext.mockReturnValueOnce({
      active: {
        key: "organization:org-a",
        scope: "organization",
        name: "Org A",
        status: "active",
        authority: "organization_admin",
        policyRevision: 7,
        securityRevision: 11,
      },
    });

    const markup = renderSidebar();

    expect(markup).toContain('href="/admin/organization"');
    expect(markup).toContain(">People<");
    expect(markup).toContain('href="/admin/organization/policy-decisions"');
    expect(markup).toContain('href="/admin/organization/activity"');
    expect(markup).toContain(">Activity &amp; Audit<");
    expect(markup).not.toContain('href="/admin/platform/engagement"');
    expect(markup).not.toContain('href="/admin/plugins"');
    expect(markup).not.toContain(">Global Policy<");
    expect(mockUseAdminSessions).toHaveBeenLastCalledWith(false);
    expect(mockUseBuildInfo).toHaveBeenLastCalledWith(false);
    expect(mockUsePolicyCapability).toHaveBeenLastCalledWith(false);
    expect(mockUseAdminPluginInstallations).toHaveBeenLastCalledWith(false);
  });

  it("keeps settings as one sidebar destination", () => {
    const markup = renderSidebar();
    const settingsLinks = markup.match(/href="\/admin\/settings[^"]*"/g) ?? [];

    expect(settingsLinks).toEqual(['href="/admin/settings"']);
    expect(markup).not.toContain("/admin/settings?tab=");
  });

  it("includes a Sections link in the content navigation", () => {
    const markup = renderSidebar();

    expect(markup).toContain('href="/admin/sections"');
    expect(markup).toContain(">Sections<");
    expect(markup).not.toContain('href="/admin/home-rows"');
  });

  it("hides Policy navigation when the editor capability is unavailable", () => {
    mockUsePolicyCapability.mockReturnValueOnce({
      data: {
        enabled: true,
        editor_available: false,
        decision_types: [],
        generation: 1,
      },
    });

    const markup = renderSidebar();

    expect(markup).not.toContain('href="/admin/policy"');
    expect(markup).not.toContain(">Policy<");
  });

  it("renders the build identifier in the footer", () => {
    const markup = renderSidebar();

    expect(markup).toContain(">Build<");
    expect(markup).toContain(">411 · b4c5aae1+dirty<");
    expect(markup).toContain('title="Built 2026-08-19T19:45:00Z"');
  });

  it("renders dev build when build metadata is missing", () => {
    mockUseBuildInfo.mockReturnValueOnce({
      data: {
        ...defaultBuildInfo,
        display: "unavailable",
        revision: "",
        dirty: false,
        vcs_time: "",
        build_number: 0,
        built_at: "",
        available: false,
      },
      isPending: false,
      isError: false,
    });

    const markup = renderSidebar();

    expect(markup).toContain(">dev build<");
  });

  it("falls back to the revision for builds without an ordered number", () => {
    const legacyBuildInfo = { ...defaultBuildInfo };
    delete legacyBuildInfo.build_number;
    delete legacyBuildInfo.built_at;
    mockUseBuildInfo.mockReturnValueOnce({
      data: legacyBuildInfo,
      isPending: false,
      isError: false,
    });

    const markup = renderSidebar();

    expect(markup).toContain(">b4c5aae1+dirty<");
  });

  it("renders load failed when the build info query errors", () => {
    mockUseBuildInfo.mockReturnValueOnce({
      data: undefined,
      isPending: false,
      isError: true,
    });

    const markup = renderSidebar();

    expect(markup).toContain(">load failed<");
  });

  it("renders loading while the build info query is pending", () => {
    mockUseBuildInfo.mockReturnValueOnce({
      data: undefined,
      isPending: true,
      isError: false,
    });

    const markup = renderSidebar();

    expect(markup).toContain(">loading...<");
  });
});
