// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { adminV2Api } from "@/api/adminV2Client";
import InvitationsTab from "./InvitationsTab";

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

describe("InvitationsTab organization context", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("loads only the active organization's v2 invitations and labels their identity", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/organization/invitations") {
        return {
          invitations: [
            {
              id: 3,
              email: "local@example.test",
              role: "user",
              create_profile: true,
              show_tour: true,
              invited_by: 7,
              status: "pending",
              expires_at: "2026-08-20T08:00:00Z",
              created_at: "2026-08-13T08:00:00Z",
            },
          ],
        } as never;
      }
      if (path === "/organization/groups") return { groups: [] } as never;
      if (path === "/organization/libraries") return { libraries: [] } as never;
      throw new Error(`unexpected ${path}`);
    });

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <InvitationsTab />
      </QueryClientProvider>,
    );

    expect(await screen.findByText("local@example.test")).toBeInTheDocument();
    expect(screen.getByText(/North Sea Media/)).toBeInTheDocument();
    expect(vi.mocked(adminV2Api)).toHaveBeenCalledWith("/organization/invitations");
  });
});
