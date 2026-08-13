import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  AdminContextProvider,
  OrganizationContextGuard,
  PlatformContextGuard,
  useAdminContext,
} from "./AdminContextProvider";
import { setAccessToken } from "@/api/client";
import type { User } from "@/api/types";

const platformAdmin: User = {
  id: 1,
  username: "admin",
  email: "admin@example.test",
  role: "admin",
  permissions: [],
  download_allowed: true,
};

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function organizationsResponse() {
  return jsonResponse({
    organizations: [
      {
        id: "org-a",
        slug: "org-a",
        name: "Org A",
        default: true,
        membership_id: "membership-a",
        membership_role: "admin",
        policy_revision: 7,
        security_revision: 11,
      },
      {
        id: "org-b",
        slug: "org-b",
        name: "Org B",
        default: false,
        membership_id: "membership-b",
        membership_role: "admin",
        policy_revision: 3,
        security_revision: 5,
      },
    ],
  });
}

function sessionResponse(key: "platform" | "organization:org-a" | "organization:org-b") {
  const organizationId = key.startsWith("organization:") ? key.slice("organization:".length) : null;
  const name =
    organizationId === "org-a" ? "Org A" : organizationId === "org-b" ? "Org B" : "Platform";
  return jsonResponse({
    access_token: `token-${key}`,
    expires_at: "2026-08-13T12:15:00Z",
    context: {
      key,
      scope: organizationId ? "organization" : "platform",
      ...(organizationId ? { organization_id: organizationId } : {}),
      name,
      status: "active",
      authority: organizationId ? "organization_admin" : "platform_admin",
      policy_revision: organizationId === "org-a" ? 7 : organizationId === "org-b" ? 3 : 0,
      security_revision: organizationId === "org-a" ? 11 : organizationId === "org-b" ? 5 : 0,
    },
  });
}

function ContextHarness() {
  const context = useAdminContext();
  const navigate = useNavigate();
  const member = context.active?.key === "organization:org-a" ? "Org A member" : null;
  return (
    <>
      <h1 tabIndex={-1}>{context.active?.name ?? "Choose context"}</h1>
      {member}
      <button type="button" onClick={() => void context.switchContext("organization:org-b")}>
        Switch to Org B
      </button>
      <button type="button" onClick={() => navigate("/admin/platform/organizations")}>
        Platform route
      </button>
      <span>{context.switching ? "Switching" : "Ready"}</span>
    </>
  );
}

function renderProvider(queryClient: QueryClient, initialEntry = "/admin/organization") {
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <AdminContextProvider user={platformAdmin}>
          <Routes>
            <Route path="/admin" element={<ContextHarness />} />
            <Route path="/admin/organization" element={<ContextHarness />} />
            <Route element={<PlatformContextGuard />}>
              <Route path="/admin/platform/organizations" element={<div>Platform protected</div>} />
            </Route>
            <Route element={<OrganizationContextGuard />}>
              <Route
                path="/admin/organization/people"
                element={<div>Organization protected</div>}
              />
            </Route>
            <Route path="/admin/context" element={<div>Context selection</div>} />
          </Routes>
        </AdminContextProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("AdminContextProvider", () => {
  beforeEach(() => {
    window.localStorage.clear();
    window.sessionStorage.clear();
    setAccessToken("account-token");
  });

  afterEach(() => {
    setAccessToken(null);
    vi.unstubAllGlobals();
  });

  it("clears the prior tenant before rendering the new context", async () => {
    window.localStorage.setItem("silo-admin-context", "organization:org-a");
    let resolveOrgB!: (response: Response) => void;
    const orgBSession = new Promise<Response>((resolve) => {
      resolveOrgB = resolve;
    });
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/api/v2/organizations") return Promise.resolve(organizationsResponse());
        const body = JSON.parse(String(init?.body ?? "{}")) as { organization_id?: string };
        if (body.organization_id === "org-b") return orgBSession;
        return Promise.resolve(sessionResponse("organization:org-a"));
      }),
    );
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(["admin-v2", "organization:org-a", "people"], ["Org A member"]);
    queryClient.setQueryData(["admin-v2", "organization:org-b", "people"], ["Org B member"]);
    queryClient.setQueryData(["unrelated"], "keep me");
    const cancelSpy = vi.spyOn(queryClient, "cancelQueries");
    const removeSpy = vi.spyOn(queryClient, "removeQueries");
    renderProvider(queryClient);

    expect(await screen.findByRole("heading", { name: "Org A" })).toBeInTheDocument();
    expect(screen.getByText("Org A member")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Switch to Org B" }));

    expect(cancelSpy).toHaveBeenCalledWith({ queryKey: ["admin-v2", "organization:org-a"] });
    expect(removeSpy).toHaveBeenCalledWith({ queryKey: ["admin-v2", "organization:org-a"] });
    expect(screen.queryByText("Org A member")).not.toBeInTheDocument();
    expect(queryClient.getQueryData(["admin-v2", "organization:org-b", "people"])).toEqual([
      "Org B member",
    ]);
    expect(queryClient.getQueryData(["unrelated"])).toBe("keep me");

    await act(async () => resolveOrgB(sessionResponse("organization:org-b")));
    expect(await screen.findByRole("heading", { name: "Org B" })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("heading", { name: "Org B" })).toHaveFocus());
  });

  it("redirects a mismatched protected route before mounting its page", async () => {
    window.localStorage.setItem("silo-admin-context", "organization:org-a");
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) =>
        Promise.resolve(
          String(input) === "/api/v2/organizations"
            ? organizationsResponse()
            : sessionResponse("organization:org-a"),
        ),
      ),
    );
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderProvider(queryClient, "/admin/platform/organizations");

    expect(await screen.findByRole("heading", { name: "Org A" })).toBeInTheDocument();
    expect(screen.queryByText("Platform protected")).not.toBeInTheDocument();
  });

  it("redirects an organization route in platform context before mounting it", async () => {
    window.localStorage.setItem("silo-admin-context", "platform");
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) =>
        Promise.resolve(
          String(input) === "/api/v2/organizations"
            ? organizationsResponse()
            : sessionResponse("platform"),
        ),
      ),
    );
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderProvider(queryClient, "/admin/organization/people");

    expect(await screen.findByRole("heading", { name: "Platform" })).toBeInTheDocument();
    expect(screen.queryByText("Organization protected")).not.toBeInTheDocument();
  });

  it("clears scoped queries and returns to context selection when authority is stale", async () => {
    window.localStorage.setItem("silo-admin-context", "organization:org-a");
    let protectedCalls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url === "/api/v2/organizations") return Promise.resolve(organizationsResponse());
        if (url === "/api/v2/admin/session") {
          return Promise.resolve(sessionResponse("organization:org-a"));
        }
        protectedCalls += 1;
        return Promise.resolve(
          jsonResponse(
            { error: "authorization_state_stale", message: "Tenant authorization state is stale" },
            401,
          ),
        );
      }),
    );
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(["admin-v2", "organization:org-a", "people"], ["Org A member"]);
    renderProvider(queryClient);
    expect(await screen.findByRole("heading", { name: "Org A" })).toBeInTheDocument();

    const { adminV2Api } = await import("@/api/adminV2Client");
    await act(async () => {
      await expect(adminV2Api("/organization/overview")).rejects.toMatchObject({
        code: "authorization_state_stale",
      });
    });

    expect(await screen.findByText("Context selection")).toBeInTheDocument();
    expect(queryClient.getQueryData(["admin-v2", "organization:org-a", "people"])).toBeUndefined();
    expect(window.localStorage.getItem("silo-admin-context")).toBeNull();
    expect(protectedCalls).toBeGreaterThan(0);
  });

  it("re-mints the same context after tenant_session_required without retaining old data", async () => {
    window.localStorage.setItem("silo-admin-context", "organization:org-a");
    let sessionCalls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url === "/api/v2/organizations") return Promise.resolve(organizationsResponse());
        if (url === "/api/v2/admin/session") {
          sessionCalls += 1;
          return Promise.resolve(sessionResponse("organization:org-a"));
        }
        return Promise.resolve(
          jsonResponse(
            { error: "tenant_session_required", message: "Administrative session required" },
            401,
          ),
        );
      }),
    );
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(["admin-v2", "organization:org-a", "people"], ["Org A member"]);
    renderProvider(queryClient);
    expect(await screen.findByRole("heading", { name: "Org A" })).toBeInTheDocument();

    const { adminV2Api } = await import("@/api/adminV2Client");
    await act(async () => {
      await expect(adminV2Api("/organization/overview")).rejects.toMatchObject({
        code: "tenant_session_required",
      });
    });

    await waitFor(() => expect(sessionCalls).toBe(2));
    expect(await screen.findByRole("heading", { name: "Org A" })).toBeInTheDocument();
    expect(queryClient.getQueryData(["admin-v2", "organization:org-a", "people"])).toBeUndefined();
  });

  it("falls back from lost platform authority to an authorized organization", async () => {
    window.localStorage.setItem("silo-admin-context", "platform");
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/api/v2/organizations") return Promise.resolve(organizationsResponse());
        if (url === "/api/v2/admin/session") {
          const body = JSON.parse(String(init?.body ?? "{}")) as { scope?: string };
          return Promise.resolve(
            sessionResponse(body.scope === "platform" ? "platform" : "organization:org-a"),
          );
        }
        return Promise.resolve(
          jsonResponse(
            {
              error: "insufficient_platform_authority",
              message: "Platform administrator authority required",
            },
            403,
          ),
        );
      }),
    );
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderProvider(queryClient, "/admin");
    expect(await screen.findByRole("heading", { name: "Platform" })).toBeInTheDocument();

    const { adminV2Api } = await import("@/api/adminV2Client");
    await act(async () => {
      await expect(adminV2Api("/platform/organizations")).rejects.toMatchObject({
        code: "insufficient_platform_authority",
      });
    });

    expect(await screen.findByRole("heading", { name: "Org A" })).toBeInTheDocument();
  });

  it("removes a suspended organization and falls back without retaining its label", async () => {
    window.localStorage.setItem("silo-admin-context", "organization:org-a");
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/api/v2/organizations") return Promise.resolve(organizationsResponse());
        if (url === "/api/v2/admin/session") {
          const body = JSON.parse(String(init?.body ?? "{}")) as { scope?: string };
          return Promise.resolve(
            sessionResponse(body.scope === "platform" ? "platform" : "organization:org-a"),
          );
        }
        return Promise.resolve(
          jsonResponse(
            { error: "organization_suspended", message: "Tenant access is suspended" },
            403,
          ),
        );
      }),
    );
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderProvider(queryClient);
    expect(await screen.findByRole("heading", { name: "Org A" })).toBeInTheDocument();

    const { adminV2Api } = await import("@/api/adminV2Client");
    await act(async () => {
      await expect(adminV2Api("/organization/overview")).rejects.toMatchObject({
        code: "organization_suspended",
      });
    });

    expect(await screen.findByRole("heading", { name: "Platform" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Org A" })).not.toBeInTheDocument();
  });
});
