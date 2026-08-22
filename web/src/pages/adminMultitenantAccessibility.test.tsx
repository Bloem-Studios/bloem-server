// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import AdminContextSwitcher from "@/components/admin/AdminContextSwitcher";
import BulkPeopleActionBar from "@/components/admin/people/BulkPeopleActionBar";
import PeopleTable from "@/components/admin/people/PeopleTable";
import PersonDetailSheet from "@/components/admin/people/PersonDetailSheet";
import AdminAccessGroups from "./AdminAccessGroups";
import OrganizationOverviewPage from "./admin-organization/OrganizationOverviewPage";
import OrganizationsPage from "./admin-platform/OrganizationsPage";

const state = vi.hoisted(() => ({
  switchContext: vi.fn(),
  active: {
    key: "organization:org-north",
    scope: "organization" as const,
    organizationId: "org-north",
    name: "North Sea Media",
    status: "active" as const,
    authority: "organization_admin" as const,
    policyRevision: 7,
    securityRevision: 3,
  },
}));

const defaultGroup = {
  id: 1,
  name: "Default",
  description: "",
  library_ids: null,
  max_playback_quality: "source",
  download_allowed: true,
  download_transcode_allowed: true,
  max_streams: 0,
  max_transcodes: 0,
  allowed_permissions: null,
  requests_allowed: true,
  is_default: true,
  member_count: 8,
  created_at: "2026-08-13T12:00:00Z",
  updated_at: "2026-08-13T12:00:00Z",
};
const kidsGroup = { ...defaultGroup, id: 2, name: "Kids", is_default: false, member_count: 4 };
const person = {
  organization_id: "org-north",
  account_id: 11,
  email: "alex@example.test",
  display_name: "Alex",
  membership_id: "membership-11",
  membership_status: "active" as const,
  legacy_role: "user" as const,
  security_revision: 3,
  last_activity: "2026-08-13T12:00:00Z",
  profiles: [
    {
      id: "profile-11",
      name: "Alex",
      group_id: 1,
      group_name: "Default",
      updated_at: "2026-08-13T12:00:00Z",
    },
  ],
};

vi.mock("@/contexts/AdminContextProvider", () => ({
  useAdminContext: () => ({
    active: state.active,
    available: [
      state.active,
      {
        key: "platform",
        scope: "platform",
        name: "Platform",
        status: "active",
        authority: "platform_admin",
      },
    ],
    switching: false,
    failure: null,
    switchContext: state.switchContext,
  }),
}));

vi.mock("@/hooks/useDocumentTitle", () => ({ useDocumentTitle: vi.fn() }));
vi.mock("@/hooks/queries/admin/organizations", () => ({
  usePlatformOrganizations: () => ({
    isLoading: false,
    isError: false,
    data: {
      organizations: [
        {
          id: "org-north",
          name: "North Sea Media",
          slug: "north-sea-media",
          status: "active",
          owner_account_id: 7,
          policy_revision: 7,
          membership_count: 12,
          profile_count: 18,
          library_count: 3,
          entitlement_count: 2,
        },
      ],
    },
  }),
  useCreateOrganization: () => ({
    mutateAsync: vi.fn(),
    reset: vi.fn(),
    isPending: false,
    error: null,
  }),
}));
vi.mock("@/hooks/queries/admin/organizationPeople", () => ({
  useOrganizationOverview: () => ({
    isLoading: false,
    isError: false,
    data: {
      id: "org-north",
      name: "North Sea Media",
      slug: "north-sea-media",
      status: "active",
      owner_account_id: 7,
      policy_revision: 7,
      membership_count: 12,
      profile_count: 18,
      library_count: 3,
      entitlement_count: 2,
    },
  }),
}));
vi.mock("@/hooks/queries/admin/accessGroups", () => ({
  useAccessGroups: () => ({ isLoading: false, data: [defaultGroup, kidsGroup] }),
  useCreateAccessGroup: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateAccessGroup: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteAccessGroup: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock("@/hooks/queries/admin/libraries", () => ({
  useAdminLibraries: () => ({ data: [] }),
  useOrganizationLibraries: () => ({ data: [] }),
}));

function renderAtWidth(width: number, node: React.ReactNode) {
  Object.defineProperty(window, "innerWidth", { configurable: true, value: width });
  return render(
    <MemoryRouter>
      <div data-release-viewport={`${width}x900`}>{node}</div>
    </MemoryRouter>,
  );
}

describe("multitenant administration accessibility", () => {
  beforeEach(() => {
    state.switchContext.mockReset().mockResolvedValue(undefined);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("supports keyboard context switching and announces the selected context", async () => {
    render(
      <MemoryRouter>
        <AdminContextSwitcher />
      </MemoryRouter>,
    );

    const switcher = screen.getByRole("combobox", { name: "Administrative context" });
    await userEvent.selectOptions(switcher, "platform");
    expect(state.switchContext).toHaveBeenCalledWith("platform", undefined);
    expect(screen.getByRole("status")).toHaveTextContent(
      "Administrative context changed to North Sea Media",
    );
  });

  it("keeps table headers, profile controls and bulk counts explicitly labeled", () => {
    render(
      <>
        <PeopleTable
          people={[person]}
          groups={[{ id: 1, name: "Default" }]}
          groupDrafts={{}}
          onInspect={vi.fn()}
          onChangeGroup={vi.fn()}
        />
        <BulkPeopleActionBar
          selection={{
            token: "opaque",
            matched: 1234,
            excluded: 0,
            expires_at: "2026-08-13T12:15:00Z",
          }}
          groups={[{ id: 1, name: "Default" }]}
          onApplyPolicy={() => undefined}
          onAction={vi.fn()}
        />
      </>,
    );

    expect(screen.getAllByRole("columnheader").map((header) => header.textContent)).toEqual([
      "Account",
      "Status",
      "Profiles and groups",
      "Last activity",
      "Security revision",
      "Details",
    ]);
    expect(screen.getAllByLabelText("Group for Alex profile")).not.toHaveLength(0);
    expect(screen.getByRole("complementary", { name: "Bulk people actions" })).toHaveTextContent(
      "1,234 people selected",
    );
  });

  it("renders person details as a full-height responsive sheet", () => {
    renderAtWidth(390, <PersonDetailSheet person={person} onOpenChange={vi.fn()} />);
    const sheet = screen.getByRole("dialog");
    expect(sheet).toHaveClass("h-full", "w-3/4");
    expect(sheet).toHaveClass("sm:max-w-lg");
  });

  it("names the organization and affected profile count before destructive group deletion", async () => {
    renderAtWidth(390, <AdminAccessGroups />);
    fireEvent.click(screen.getByRole("button", { name: /Kids/ }));
    fireEvent.click(screen.getByRole("button", { name: "Delete group" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/4 profiles/)).toBeInTheDocument();
    expect(within(dialog).getByText(/North Sea Media/)).toBeInTheDocument();
    expect(within(dialog).getByText(/Default/)).toBeInTheDocument();
  });
});

describe("multitenant administration responsive release snapshots", () => {
  afterEach(cleanup);

  for (const width of [1440, 390]) {
    it(`captures Platform Organizations at ${width}px`, () => {
      const { asFragment } = renderAtWidth(width, <OrganizationsPage />);
      expect(asFragment()).toMatchSnapshot();
    });

    it(`captures Organization Overview at ${width}px`, () => {
      const { asFragment } = renderAtWidth(width, <OrganizationOverviewPage />);
      expect(asFragment()).toMatchSnapshot();
    });

    it(`captures People at ${width}px`, () => {
      const { asFragment } = renderAtWidth(
        width,
        <>
          <h1 tabIndex={-1}>People</h1>
          <PeopleTable
            people={[person]}
            groups={[{ id: 1, name: "Default" }]}
            groupDrafts={{}}
            onInspect={vi.fn()}
            onChangeGroup={vi.fn()}
          />
          <BulkPeopleActionBar
            selection={{
              token: "opaque",
              matched: 1234,
              excluded: 0,
              expires_at: "2026-08-13T12:15:00Z",
            }}
            groups={[{ id: 1, name: "Default" }]}
            onApplyPolicy={() => undefined}
            onAction={vi.fn()}
          />
        </>,
      );
      expect(asFragment()).toMatchSnapshot();
    });

    it(`captures Access Groups at ${width}px`, () => {
      const { asFragment } = renderAtWidth(width, <AdminAccessGroups />);
      expect(asFragment()).toMatchSnapshot();
    });
  }
});
