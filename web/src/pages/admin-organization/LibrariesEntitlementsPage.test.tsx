// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { adminV2Api } from "@/api/adminV2Client";
import LibrariesEntitlementsPage from "./LibrariesEntitlementsPage";

vi.mock("@/api/adminV2Client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/adminV2Client")>();
  return { ...actual, adminV2Api: vi.fn() };
});
vi.mock("@/contexts/AdminContextProvider", () => ({
  useAdminContext: () => ({
    active: {
      key: "organization:org-1",
      scope: "organization",
      name: "North Sea Media",
      policyRevision: 7,
    },
  }),
}));

describe("LibrariesEntitlementsPage", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("distinguishes owned libraries from entitlements and explains effective access", async () => {
    vi.mocked(adminV2Api).mockResolvedValue({
      libraries: [
        { folder_id: 4, name: "Local Movies", type: "movies", access_kind: "owned" },
        {
          folder_id: 8,
          name: "Platform Series",
          type: "series",
          access_kind: "entitled",
          entitlement: { id: "ent-1", status: "active", security_revision: 3 },
        },
      ],
    } as never);

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <LibrariesEntitlementsPage />
      </QueryClientProvider>,
    );

    expect(await screen.findByText("Local Movies")).toBeInTheDocument();
    expect(screen.getByText("Owned by North Sea Media")).toBeInTheDocument();
    expect(screen.getByText("Platform entitlement")).toBeInTheDocument();
    expect(screen.getByText(/intersection of this organization ceiling/i)).toBeInTheDocument();
    expect(screen.queryByText(/unconditional access/i)).not.toBeInTheDocument();
    expect(vi.mocked(adminV2Api)).toHaveBeenCalledWith("/organization/libraries");
  });
});
