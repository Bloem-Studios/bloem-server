// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  activateAdminV2Context,
  adminV2Api,
  AdminV2ClientError,
  clearAdminV2Context,
} from "@/api/adminV2Client";
import type { OrganizationLibraryProjection } from "@/hooks/queries/admin/libraries";
import LibrariesEntitlementsPage from "./LibrariesEntitlementsPage";

const mockUseAdminContext = vi.hoisted(() => vi.fn());
vi.mock("@/api/adminV2Client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/adminV2Client")>();
  return { ...actual, adminV2Api: vi.fn() };
});
vi.mock("@/contexts/AdminContextProvider", () => ({
  useAdminContext: mockUseAdminContext,
}));

let libraries: OrganizationLibraryProjection[];
function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const tree = () => (
    <QueryClientProvider client={client}>
      <LibrariesEntitlementsPage />
    </QueryClientProvider>
  );
  const view = render(tree());
  return { ...view, rerenderPage: () => view.rerender(tree()) };
}

async function confirm(label: string) {
  const dialog = await screen.findByRole("alertdialog");
  fireEvent.click(within(dialog).getByRole("button", { name: label }));
}

function writes() {
  return vi.mocked(adminV2Api).mock.calls.filter(([, init]) => init?.method);
}

describe("LibrariesEntitlementsPage", () => {
  beforeEach(() => {
    activateAdminV2Context("test-admin", "organization:org-1");
    mockUseAdminContext.mockReturnValue({
      active: {
        key: "organization:org-1",
        scope: "organization",
        name: "North Sea Media",
        policyRevision: 7,
      },
    });
    libraries = [
      { folder_id: 4, name: "Local Movies", type: "movies", access_kind: "owned" },
      {
        folder_id: 8,
        name: "Platform Series",
        type: "series",
        access_kind: "entitled",
        entitlement: { id: "ent-1", status: "active", security_revision: 3 },
      },
    ];
    vi.mocked(adminV2Api).mockImplementation(async (_path, init) => {
      if (init?.method === "DELETE") {
        libraries = libraries.filter((item) => item.folder_id !== 8);
        return undefined;
      }
      if (init?.method === "PUT") {
        const body = JSON.parse(String(init.body));
        libraries = libraries.map((item) =>
          item.folder_id === 8 && item.entitlement
            ? {
                ...item,
                entitlement: {
                  ...item.entitlement,
                  status: body.status,
                  security_revision: item.entitlement.security_revision + 1,
                },
              }
            : item,
        );
        return { entitlement: libraries[1]!.entitlement };
      }
      return { libraries };
    });
  });

  afterEach(() => {
    cleanup();
    clearAdminV2Context();
    vi.clearAllMocks();
  });

  it("distinguishes owned libraries from entitlements and explains effective access", async () => {
    setup();
    expect(await screen.findByText("Local Movies")).toBeInTheDocument();
    expect(screen.getByText("Owned by North Sea Media")).toBeInTheDocument();
    expect(screen.getByText("Platform entitlement")).toBeInTheDocument();
    expect(screen.getByText(/intersection of this organization ceiling/i)).toBeInTheDocument();
    expect(screen.queryByText(/unconditional access/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Manage Local Movies" })).not.toBeInTheDocument();
    expect(vi.mocked(adminV2Api)).toHaveBeenCalledWith("/organization/libraries");
  });

  it("confirms suspension, uses the grant revision rather than the organization revision, and reloads", async () => {
    setup();
    fireEvent.click(await screen.findByRole("button", { name: "Suspend access" }));
    expect(writes()).toHaveLength(0);
    await confirm("Suspend access");
    await screen.findByRole("button", { name: "Restore access" });
    expect(writes()).toEqual([
      [
        "/organization/entitlements/8",
        {
          method: "PUT",
          body: JSON.stringify({ expected_revision: 3, status: "suspended" }),
        },
        "none",
        expect.objectContaining({ key: "organization:org-1" }),
      ],
    ]);
    expect(screen.getByText(/security revision 4/)).toBeInTheDocument();
  });

  it("restores a suspended grant but never offers activation of a revoked grant", async () => {
    libraries[1]!.entitlement!.status = "suspended";
    libraries.push({
      ...libraries[1]!,
      folder_id: 9,
      name: "Withdrawn",
      entitlement: { id: "revoked", status: "revoked", security_revision: 5 },
    });
    setup();
    expect(await screen.findByRole("button", { name: "Restore access" })).toBeEnabled();
    expect(screen.queryByRole("group", { name: "Manage Withdrawn" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Restore access" }));
    await confirm("Restore access");
    await screen.findByRole("button", { name: "Suspend access" });
    expect(JSON.parse(String(writes()[0]![1]?.body))).toEqual({
      expected_revision: 3,
      status: "active",
    });
  });

  it("requires confirmation for withdrawal and removes only the grant after success", async () => {
    setup();
    fireEvent.click(await screen.findByRole("button", { name: "Withdraw grant" }));
    expect(screen.getByText(/cannot restore a withdrawn grant here/)).toBeInTheDocument();
    expect(writes()).toHaveLength(0);
    await confirm("Withdraw grant");
    await waitFor(() => expect(screen.queryByText("Platform Series")).not.toBeInTheDocument());
    expect(screen.getByText("Local Movies")).toBeInTheDocument();
    expect(writes()[0]![1]).toEqual({ method: "DELETE", body: '{"expected_revision":3}' });
  });

  it("does not mutate when confirmation is cancelled", async () => {
    setup();
    fireEvent.click(await screen.findByRole("button", { name: "Withdraw grant" }));
    fireEvent.click(
      within(screen.getByRole("alertdialog")).getByRole("button", { name: "Cancel" }),
    );
    expect(writes()).toHaveLength(0);
  });

  it("refreshes a stale grant without replaying the rejected write", async () => {
    setup();
    fireEvent.click(await screen.findByRole("button", { name: "Suspend access" }));
    libraries = libraries.map((item) =>
      item.entitlement
        ? { ...item, entitlement: { ...item.entitlement, security_revision: 4 } }
        : item,
    );
    vi.mocked(adminV2Api).mockRejectedValueOnce(
      new AdminV2ClientError(409, "authorization_state_changed", "Changed"),
    );
    await confirm("Suspend access");
    expect(await screen.findByRole("alert")).toHaveTextContent("This grant changed");
    expect(screen.getByText(/security revision 4/)).toBeInTheDocument();
    expect(writes()).toHaveLength(1);
    expect(JSON.parse(String(writes()[0]![1]?.body)).expected_revision).toBe(3);
  });

  it("shows read failures and allows a safe reload", async () => {
    vi.mocked(adminV2Api).mockRejectedValueOnce(new Error("Service unavailable"));
    setup();
    expect(await screen.findByRole("alert")).toHaveTextContent("Service unavailable");
    fireEvent.click(screen.getByRole("button", { name: "Reload libraries" }));
    expect(await screen.findByText("Local Movies")).toBeInTheDocument();
  });

  it("closes an old confirmation when the organization changes", async () => {
    const view = setup();
    fireEvent.click(await screen.findByRole("button", { name: "Withdraw grant" }));
    activateAdminV2Context("org-2", "organization:org-2");
    mockUseAdminContext.mockReturnValue({
      active: { key: "organization:org-2", scope: "organization", name: "Other" },
    });
    view.rerenderPage();
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(writes()).toHaveLength(0);
  });

  it("does not read libraries without an organization context", () => {
    mockUseAdminContext.mockReturnValue({ active: { key: "platform", scope: "platform" } });
    setup();
    expect(screen.getByRole("alert")).toHaveTextContent("Select an organization");
    expect(adminV2Api).not.toHaveBeenCalled();
  });
});
