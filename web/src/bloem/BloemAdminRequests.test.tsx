import type { ReactNode } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AdminContextSummary } from "@/api/bloemTypes";
import type { BookRequest } from "@/api/bloemBookRequests";
import { activateAdminV2Context } from "@/api/adminV2Client";
import { setAccessToken } from "@/api/client";

const mocks = vi.hoisted(() => ({
  supported: true,
  list: vi.fn(),
  action: vi.fn(),
  active: null as AdminContextSummary | null,
  setActive: (_active: AdminContextSummary | null) => {},
}));
vi.mock("@/contexts/AdminContextProvider", async () => {
  const React = await import("react");
  const Context = React.createContext<AdminContextSummary | null>(null);
  return {
    useAdminContext: () => ({ active: React.useContext(Context), switching: false }),
    AdminContextProvider: ({ children }: { children: ReactNode }) => {
      const [active, setActive] = React.useState(mocks.active);
      mocks.setActive = setActive;
      return <Context.Provider value={active}>{children}</Context.Provider>;
    },
  };
});
vi.mock("@/pages/AdminRequests", () => ({ default: () => <h1>Video request queue</h1> }));
vi.mock("@/api/bloemClient", () => ({
  nativeApi: async () => ({ feature_tokens: mocks.supported ? ["audiobook_requests_v1"] : [] }),
}));
vi.mock("@/api/bloemBookRequests", async () => {
  const actual =
    await vi.importActual<typeof import("@/api/bloemBookRequests")>("@/api/bloemBookRequests");
  return { ...actual, bookRequestsApi: { adminList: mocks.list, adminAction: mocks.action } };
});
import { AdminContextProvider } from "@/contexts/AdminContextProvider";
import BloemAdminRequests from "./BloemAdminRequests";

function org(id: string): AdminContextSummary {
  return {
    key: `organization:${id}`,
    scope: "organization",
    organizationId: id,
    name: `Organization ${id}`,
    status: "active",
    authority: "organization_admin",
    policyRevision: 1,
    securityRevision: 1,
  };
}
function request(title: string): BookRequest {
  return {
    id: title,
    organization_id: "org-a",
    media_type: "audiobook",
    title,
    author: "Author",
    status: "pending",
    outcome: "active",
    fulfilment: "awaiting_fulfilment",
    revision: 5,
    requested_by_user_id: 1,
    requested_by_profile_id: "viewer",
    created_at: "2026-10-10T00:00:00Z",
    updated_at: "2026-10-10T00:00:00Z",
  };
}
function open() {
  render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
        })
      }
    >
      <AdminContextProvider platformAuthority={true}>
        <MemoryRouter initialEntries={["/admin/requests?kind=audiobook"]}>
          <BloemAdminRequests />
        </MemoryRouter>
      </AdminContextProvider>
    </QueryClientProvider>,
  );
}
beforeEach(() => {
  mocks.supported = true;
  mocks.active = org("a");
  setAccessToken("account-a");
  activateAdminV2Context("admin-a", "organization:a");
  mocks.list.mockReset().mockResolvedValue({ requests: [] });
  mocks.action.mockReset().mockResolvedValue({});
});

describe("organization audiobook queue", () => {
  it("requires an organization instead of granting a platform-wide queue", async () => {
    mocks.active = {
      key: "platform",
      scope: "platform",
      name: "Platform",
      status: "active",
      authority: "platform_admin",
      policyRevision: 1,
      securityRevision: 1,
    };
    activateAdminV2Context("platform", "platform");
    open();
    await screen.findByText(/Select an organization in the navigation/);
    expect(mocks.list).not.toHaveBeenCalled();
  });
  it("falls back to the native video queue without audiobook capability", async () => {
    mocks.supported = false;
    open();
    await screen.findByRole("heading", { name: "Video request queue" });
    expect(mocks.list).not.toHaveBeenCalled();
  });
  it("submits a reviewed row and reason under the selected organization", async () => {
    const row = request("Review book");
    mocks.list.mockResolvedValue({ requests: [row] });
    open();
    await screen.findByText("Review book");
    fireEvent.change(screen.getByRole("textbox", { name: "Reason (optional)" }), {
      target: { value: "Edition checked" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() =>
      expect(mocks.action).toHaveBeenCalledWith(
        row,
        "approve",
        "Edition checked",
        expect.objectContaining({ key: "organization:a" }),
      ),
    );
  });
  it("resets pagination, rows and reasons when administrative context changes", async () => {
    mocks.list.mockImplementation(async (context, offset) => ({
      requests:
        context.key === "organization:a"
          ? offset === 0
            ? Array.from({ length: 100 }, (_, i) => request(`A book ${i}`))
            : [request("A second page")]
          : [request("B book")],
    }));
    open();
    await screen.findByText("A book 0");
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    await screen.findByText("A second page");
    fireEvent.change(screen.getByRole("textbox", { name: "Reason (optional)" }), {
      target: { value: "A private reason" },
    });
    await act(async () => {
      activateAdminV2Context("admin-b", "organization:b");
      mocks.setActive(org("b"));
    });
    await screen.findByText("B book");
    expect(screen.queryByText("A second page")).not.toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Reason (optional)" })).toHaveValue("");
    expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled();
    expect(mocks.list.mock.calls.at(-1)?.[0]?.key).toBe("organization:b");
    expect(mocks.list.mock.calls.at(-1)?.[1]).toBe(0);
  });
});
