// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";

import { adminV2Api } from "@/api/adminV2Client";
import OrganizationOverviewPage from "./OrganizationOverviewPage";

vi.mock("@/api/adminV2Client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/adminV2Client")>();
  return { ...actual, adminV2Api: vi.fn() };
});
vi.mock("@/contexts/AdminContextProvider", () => ({
  useAdminContext: () => ({
    active: { key: "organization:org-1", name: "North Sea Media", scope: "organization" },
  }),
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("renders organization-scoped health and revision counts", async () => {
  vi.mocked(adminV2Api).mockResolvedValue({
    organization: {
      id: "org-1",
      slug: "north-sea",
      name: "North Sea Media",
      status: "active",
      owner_account_id: 7,
      policy_revision: 12,
      membership_count: 4200,
      profile_count: 6500,
      library_count: 18,
      entitlement_count: 4,
    },
  } as never);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <OrganizationOverviewPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );

  expect(await screen.findByRole("heading", { name: "North Sea Media" })).toBeInTheDocument();
  expect(screen.getByText("4,200")).toBeInTheDocument();
  expect(screen.getByText("6,500")).toBeInTheDocument();
  expect(screen.getByText("Revision 12")).toBeInTheDocument();
});
