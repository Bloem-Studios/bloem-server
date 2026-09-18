// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { activateAdminV2Context, clearAdminV2Context, adminV2Api } from "@/api/adminV2Client";
import InvitationsTab from "./InvitationsTab";

const adminState = vi.hoisted(() => ({
  active: {
    key: "organization:org-1" as `organization:${string}`,
    scope: "organization" as const,
    name: "North Sea Media",
    policyRevision: 7,
  },
}));

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
  useAdminContext: () => ({ active: adminState.active }),
}));

vi.mock("@/hooks/queries/useBloemCapabilities", () => ({
  useBloemCapabilities: () => ({
    data: { feature_tokens: ["organization_invitation_lifecycle_v1"] },
  }),
}));

describe("InvitationsTab organization context", () => {
  beforeEach(() => {
    adminState.active = {
      key: "organization:org-1",
      scope: "organization",
      name: "North Sea Media",
      policyRevision: 7,
    };
    activateAdminV2Context("test-authority", "organization:org-1");
  });
  vi.stubGlobal("ResizeObserver", ResizeObserverStub);
  afterEach(() => {
    cleanup();
    clearAdminV2Context();
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
    expect(vi.mocked(adminV2Api)).toHaveBeenCalledWith("/organization/invitations", {}, "safe", {
      key: "organization:org-1",
      generation: expect.any(Number),
    });
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
    fireEvent.click(screen.getByRole("button", { name: "Create invite link" }));

    await waitFor(() => {
      const call = vi
        .mocked(adminV2Api)
        .mock.calls.find(([path, init]) => path === "/organization/invitations" && init?.method);
      expect(call?.[2]).toBe("none");
      expect(call?.[3]).toEqual({ key: "organization:org-1", generation: expect.any(Number) });
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

  it("confirms link rotation, binds authority, and hides platform invitation actions", async () => {
    const invitation = {
      id: 3,
      email: "local@example.test",
      role: "user",
      status: "pending",
      created_at: "2026-08-13T08:00:00Z",
      expires_at: "2099-08-20T08:00:00Z",
    };
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/organization/invitations")
        return {
          invitations: [
            invitation,
            { ...invitation, id: 4, role: "admin", email: "operator@example.test" },
          ],
        } as never;
      if (path === "/organization/invitations/3/resend")
        return { invitation, claim_token: "one-time" } as never;
      throw new Error(`unexpected ${path}`);
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = render(
      <QueryClientProvider client={client}>
        <InvitationsTab />
      </QueryClientProvider>,
    );
    await screen.findByText("local@example.test");
    expect(screen.getAllByRole("button", { name: "Resend with a fresh link" })).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "Resend with a fresh link" }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("No email is sent");
    expect(vi.mocked(adminV2Api).mock.calls.some(([path]) => path.endsWith("/resend"))).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Regenerate link" }));
    await screen.findByText("No email was sent. Deliver this one-time link yourself.");
    expect(adminV2Api).toHaveBeenCalledWith(
      "/organization/invitations/3/resend",
      { method: "POST", body: JSON.stringify({ expected_revision: 7 }) },
      "none",
      { key: "organization:org-1", generation: expect.any(Number) },
    );
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    fireEvent.click(screen.getByRole("button", { name: "Resend with a fresh link" }));
    activateAdminV2Context("replacement-authority", "organization:org-1");
    view.rerender(
      <QueryClientProvider client={client}>
        <InvitationsTab />
      </QueryClientProvider>,
    );
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });
});

const handoffInvitation = {
  id: 3,
  email: "handoff@example.test",
  role: "user",
  create_profile: true,
  show_tour: true,
  invited_by: 7,
  status: "pending",
  created_at: "2026-08-13T08:00:00Z",
  expires_at: "2099-08-20T08:00:00Z",
};

function expectNoCachedHandoff(client: QueryClient, token: string) {
  expect(
    JSON.stringify(
      client
        .getQueryCache()
        .getAll()
        .map((query) => query.state),
    ),
  ).not.toContain(token);
  expect(
    JSON.stringify(
      client
        .getMutationCache()
        .getAll()
        .map((mutation) => mutation.state),
    ),
  ).not.toContain(token);
  // A secret handoff must never enter mutation data, variables, or callbacks.
  expect(client.getMutationCache().getAll()).toHaveLength(0);
}

describe.each(["create", "resend"] as const)("%s invitation handoff cache lifetime", (action) => {
  beforeEach(() => {
    adminState.active = {
      key: "organization:org-1",
      scope: "organization",
      name: "North Sea Media",
      policyRevision: 7,
    };
    activateAdminV2Context("test-authority", "organization:org-1");
    vi.stubGlobal("ResizeObserver", ResizeObserverStub);
  });
  afterEach(() => {
    cleanup();
    clearAdminV2Context();
    vi.clearAllMocks();
  });

  it.each(["success", "dialog close", "same-key generation replacement", "context switch"])(
    "keeps one-time responses out of both caches after %s",
    async (phase) => {
      const token = "dummy-claim-token-not-for-cache";
      vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
        if (init?.method === "POST")
          return { invitation: handoffInvitation, claim_token: token } as never;
        if (path === "/organization/invitations")
          return { invitations: [handoffInvitation] } as never;
        if (path === "/organization/groups") return { groups: [] } as never;
        if (path === "/organization/libraries") return { libraries: [] } as never;
        throw new Error(`unexpected ${path}`);
      });
      const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      const tree = () => (
        <QueryClientProvider client={client}>
          <InvitationsTab />
        </QueryClientProvider>
      );
      const view = render(tree());
      await screen.findByText(handoffInvitation.email);
      if (action === "create") {
        fireEvent.click(screen.getByRole("button", { name: "Invite someone" }));
        fireEvent.change(screen.getByLabelText("Email address"), {
          target: { value: "new@example.test" },
        });
        fireEvent.click(screen.getByRole("button", { name: "Create invite link" }));
      } else {
        fireEvent.click(screen.getByRole("button", { name: "Resend with a fresh link" }));
        fireEvent.click(screen.getByRole("button", { name: "Regenerate link" }));
      }
      const url = `${window.location.origin}/invite/${token}`;
      await screen.findByText(url);
      if (phase === "dialog close") {
        fireEvent.click(screen.getByRole("button", { name: "Done" }));
        await waitFor(() => expect(screen.queryByText(url)).not.toBeInTheDocument());
        if (action === "create") {
          fireEvent.click(screen.getByRole("button", { name: "Invite someone" }));
          expect(screen.getByLabelText("Email address")).toHaveValue("");
        }
      } else if (phase !== "success") {
        await act(async () => {
          // Match the provider's query-only cleanup; mutation secrets would survive it.
          if (phase === "context switch") {
            client.removeQueries({ queryKey: ["admin-v2", "organization:org-1"] });
            adminState.active = { ...adminState.active, key: "organization:org-2" };
          }
          activateAdminV2Context("replacement-authority", adminState.active.key);
          view.rerender(tree());
        });
        expect(screen.queryByText(url)).not.toBeInTheDocument();
      }
      expectNoCachedHandoff(client, token);
      view.unmount();
      client.clear();
    },
  );
});
