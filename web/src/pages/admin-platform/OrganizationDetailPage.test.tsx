// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

import { adminV2Api, AdminV2ClientError } from "@/api/adminV2Client";
import OrganizationDetailPage from "./OrganizationDetailPage";

vi.mock("@/api/adminV2Client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/adminV2Client")>();
  return { ...actual, adminV2Api: vi.fn() };
});

const organization = {
  id: "11111111-1111-1111-1111-111111111111",
  name: "North Sea Media",
  slug: "north-sea-media",
  status: "active",
  owner_account_id: 7,
  policy_revision: 4,
  membership_count: 2,
  active_membership_count: 2,
  profile_count: 5,
  library_count: 3,
  entitlement_count: 1,
};

const memberships = [
  {
    id: "m1",
    organization_id: organization.id,
    account_id: 7,
    email: "owner@example.com",
    username: "Owner",
    status: "active",
    legacy_role: "admin",
    security_revision: 1,
  },
  {
    id: "m2",
    organization_id: organization.id,
    account_id: 8,
    email: "admin@example.com",
    username: "Admin",
    status: "active",
    legacy_role: "admin",
    security_revision: 1,
  },
];

function entitlementRead(path: unknown) {
  const url = String(path);
  if (url.endsWith("/platform/entitlement-templates?status=enabled")) {
    return { templates: [] };
  }
  if (url.endsWith(`/organizations/${organization.id}/entitlement`)) {
    return {
      template_key: null,
      template_revision: 0,
      managed_default_group: null,
      tenant_limits: { slots: 0, transcodes: 0 },
      library_ids: null,
      last_reconciled_at: null,
      audit_history_href: null,
    };
  }
  return null;
}

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[`/admin/platform/organizations/${organization.id}`]}>
        <Routes>
          <Route path="/admin/platform/organizations/:id" element={<OrganizationDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("OrganizationDetailPage", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("names the organization and active membership impact before suspension", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      const entitlement = entitlementRead(path);
      if (entitlement) return entitlement as never;
      return String(path).endsWith("/memberships")
        ? ({ memberships } as never)
        : ({ organization } as never);
    });
    renderPage();

    expect(await screen.findByRole("heading", { name: "North Sea Media" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Suspend organization" }));

    expect(screen.getByText(/Suspend North Sea Media/)).toBeInTheDocument();
    expect(screen.getByText(/2 active memberships/)).toBeInTheDocument();
    expect(screen.queryByText(/delete organization/i)).not.toBeInTheDocument();
  });

  it("requires typed organization name and password before transferring ownership", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      const entitlement = entitlementRead(path);
      if (entitlement) return entitlement as never;
      return String(path).endsWith("/memberships")
        ? ({ memberships } as never)
        : ({ organization } as never);
    });
    renderPage();
    await screen.findByRole("heading", { name: "North Sea Media" });

    fireEvent.click(screen.getByRole("button", { name: "Transfer ownership" }));
    const submit = screen.getByRole("button", { name: "Confirm transfer" });
    expect(submit).toBeDisabled();
    fireEvent.change(screen.getByLabelText("New owner"), { target: { value: "8" } });
    fireEvent.change(screen.getByLabelText("Type North Sea Media to confirm"), {
      target: { value: "North Sea Media" },
    });
    fireEvent.change(screen.getByLabelText("Account password"), { target: { value: "secret" } });
    expect(submit).toBeEnabled();
    fireEvent.click(submit);

    await waitFor(() =>
      expect(
        vi
          .mocked(adminV2Api)
          .mock.calls.some(
            ([path, init]) =>
              String(path).endsWith("/transfer-ownership") &&
              String(init?.body).includes('"password":"secret"'),
          ),
      ).toBe(true),
    );
  });

  it("renders ownership eligibility validation beside the new owner field", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      const entitlement = entitlementRead(path);
      if (entitlement) return entitlement as never;
      if (String(path).endsWith("/memberships")) return { memberships } as never;
      if (String(path).endsWith("/transfer-ownership") && init?.method === "POST") {
        throw new AdminV2ClientError(422, "validation_failed", "Invalid fields", {
          owner_account_id: "Must identify an enabled organization member.",
        });
      }
      return { organization } as never;
    });
    renderPage();
    await screen.findByRole("heading", { name: "North Sea Media" });
    fireEvent.click(screen.getByRole("button", { name: "Transfer ownership" }));
    fireEvent.change(screen.getByLabelText("New owner"), { target: { value: "8" } });
    fireEvent.change(screen.getByLabelText("Type North Sea Media to confirm"), {
      target: { value: "North Sea Media" },
    });
    fireEvent.change(screen.getByLabelText("Account password"), { target: { value: "secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirm transfer" }));
    expect(
      await screen.findByText("Must identify an enabled organization member."),
    ).toBeInTheDocument();
  });

  it("reloads current revisions after a stale lifecycle mutation", async () => {
    let detailLoads = 0;
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      const entitlement = entitlementRead(path);
      if (entitlement) return entitlement as never;
      if (String(path).endsWith("/memberships")) return { memberships } as never;
      if (String(path).endsWith("/suspend"))
        throw new AdminV2ClientError(
          409,
          "authorization_state_changed",
          "Authorization state changed; reload and retry",
        );
      detailLoads += 1;
      return {
        organization: { ...organization, policy_revision: detailLoads === 1 ? 4 : 5 },
      } as never;
    });
    renderPage();
    await screen.findByRole("heading", { name: "North Sea Media" });
    fireEvent.click(screen.getByRole("button", { name: "Suspend organization" }));
    fireEvent.click(screen.getByRole("button", { name: /^Suspend$/ }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/changed.*reloaded/i);
    await waitFor(() => expect(detailLoads).toBeGreaterThan(1));
  });

  it("preserves unsaved identity edits while reloading a stale revision", async () => {
    let detailLoads = 0;
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      const entitlement = entitlementRead(path);
      if (entitlement) return entitlement as never;
      if (String(path).endsWith("/memberships")) return { memberships } as never;
      if (String(path).endsWith("/suspend"))
        throw new AdminV2ClientError(409, "authorization_state_changed", "stale");
      detailLoads += 1;
      return {
        organization: { ...organization, policy_revision: detailLoads === 1 ? 4 : 5 },
      } as never;
    });
    renderPage();
    await screen.findByRole("heading", { name: "North Sea Media" });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Unsaved draft" } });
    fireEvent.click(screen.getByRole("button", { name: "Suspend organization" }));
    fireEvent.click(screen.getByRole("button", { name: /^Suspend$/ }));
    await waitFor(() => expect(detailLoads).toBe(2));
    expect(screen.getByLabelText("Name")).toHaveValue("Unsaved draft");
    expect(screen.getByRole("button", { name: "Save details" })).toBeEnabled();
  });

  it("paginates memberships and uses the exact active count for suspension impact", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      const entitlement = entitlementRead(path);
      if (entitlement) return entitlement as never;
      if (String(path).includes("/memberships?cursor=page-2")) {
        return {
          memberships: [{ ...memberships[1], id: "m3", username: "Next page" }],
        } as never;
      }
      if (String(path).endsWith("/memberships")) {
        return { memberships: [memberships[0]], next_cursor: "page-2" } as never;
      }
      return {
        organization: { ...organization, membership_count: 80, active_membership_count: 73 },
      } as never;
    });
    renderPage();
    expect((await screen.findAllByText("Owner")).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("button", { name: "Next membership page" }));
    expect(await screen.findByText("Next page")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Suspend organization" }));
    expect(screen.getByText(/73 active memberships/)).toBeInTheDocument();
  });
});
