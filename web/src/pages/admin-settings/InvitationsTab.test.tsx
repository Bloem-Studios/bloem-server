// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { adminV2Api } from "@/api/adminV2Client";
import InvitationsTab from "./InvitationsTab";

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}

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
  vi.stubGlobal("ResizeObserver", ResizeObserverStub);
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

  it("posts the exact organization v2 invitation contract without a legacy role", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/organization/groups") return { groups: [] } as never;
      if (path === "/organization/libraries") return { libraries: [] } as never;
      if (path === "/organization/invitations" && init?.method === "POST") {
        return {
          invitation: {
            id: 4,
            email: "new@example.test",
            role: "user",
            create_profile: true,
            show_tour: true,
            invited_by: 7,
            status: "pending",
            expires_at: "2026-08-20T08:00:00Z",
            created_at: "2026-08-13T08:00:00Z",
          },
          claim_token: "secret",
        } as never;
      }
      if (path === "/organization/invitations") return { invitations: [] } as never;
      throw new Error(`unexpected ${path}`);
    });

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <InvitationsTab />
      </QueryClientProvider>,
    );
    await screen.findByText(/No invitations yet/);
    fireEvent.click(screen.getByRole("button", { name: "Invite someone" }));
    fireEvent.change(screen.getByLabelText("Email address"), {
      target: { value: "new@example.test" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send invite" }));

    await waitFor(() => {
      const call = vi
        .mocked(adminV2Api)
        .mock.calls.find(([path, init]) => path === "/organization/invitations" && init?.method);
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({
        email: "new@example.test",
        expected_revision: 7,
        access_group_id: null,
        library_ids: null,
        create_profile: true,
        show_tour: true,
      });
    });
  });
});
