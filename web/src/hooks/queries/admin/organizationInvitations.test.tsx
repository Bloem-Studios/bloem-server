// @vitest-environment jsdom
import type { PropsWithChildren } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { activateAdminV2Context, clearAdminV2Context } from "@/api/adminV2Client";
import { setAccessToken } from "@/api/client";
import type { AdminContextSummary } from "@/api/types";
import {
  useAdminInvitations,
  useCreateInvitation,
  useResendInvitation,
} from "./organizationInvitations";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));
const context: AdminContextSummary = {
  key: "organization:org-1",
  scope: "organization",
  organizationId: "org-1",
  name: "Test organization",
  status: "active",
  authority: "organization_admin",
  policyRevision: 7,
  securityRevision: 2,
};
const invitation = {
  id: 3,
  email: "new@example.test",
  role: "user",
  status: "pending",
  create_profile: true,
  show_tour: true,
  invited_by: 1,
  created_at: "2026-09-18T00:00:00Z",
  expires_at: "2026-09-25T00:00:00Z",
};
const token = "dummy-handoff-token";
const body = { email: invitation.email, role: "admin", create_profile: true, show_tour: true };
const fetchMock = vi.fn<typeof fetch>();
const clients: QueryClient[] = [];
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((yes) => {
    resolve = yes;
  });
  return { promise, resolve };
}
function listResponse(native = true) {
  return new Response(JSON.stringify(native ? { invitations: [invitation] } : [invitation]));
}
function claimResponse(native = true) {
  return new Response(
    JSON.stringify(
      native
        ? { invitation, claim_token: token }
        : {
            invitation,
            email_sent: true,
            claim_url: `https://server.test/invite/${token}`,
          },
    ),
  );
}
function mountInvitations(native = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: 3 } },
  });
  clients.push(client);
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return {
    client,
    ...renderHook(
      () => ({
        list: useAdminInvitations(native ? context : null),
        create: useCreateInvitation(native ? context : null),
        resend: useResendInvitation(native ? context : null),
      }),
      { wrapper },
    ),
  };
}
function expectNoCachedToken(client: QueryClient) {
  expect(client.getMutationCache().getAll()).toHaveLength(0);
  expect(
    JSON.stringify(
      client
        .getQueryCache()
        .getAll()
        .map((query) => query.state),
    ),
  ).not.toContain(token);
}
beforeEach(() => {
  activateAdminV2Context("dummy-admin-authority", context.key);
  setAccessToken("dummy-account-authority");
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  cleanup();
  clients.splice(0).forEach((client) => client.clear());
  clearAdminV2Context();
  setAccessToken(null);
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe.each(["create", "resend"] as const)("%s direct invitation request", (action) => {
  it.each(["success", "lost response"])(
    "awaits readback after %s and prevents duplicate submission",
    async (outcome) => {
      const readback = deferred<Response>();
      let reads = 0;
      fetchMock.mockImplementation(async (_url, init) => {
        if (init?.method === "POST") {
          if (outcome === "lost response") throw new TypeError("Response lost after commit");
          return claimResponse();
        }
        return ++reads === 1 ? listResponse() : readback.promise;
      });
      const view = mountInvitations();
      await waitFor(() => expect(view.result.current.list.isSuccess).toBe(true));
      const send = () =>
        action === "create"
          ? view.result.current.create.send(body)
          : view.result.current.resend.send(3);
      let operation!: ReturnType<typeof send>;
      let settled = false;
      act(() => {
        operation = send().finally(() => {
          settled = true;
        });
      });
      await waitFor(() => expect(reads).toBe(2));
      expect(view.result.current[action].isPending).toBe(true);
      expect(settled).toBe(false);
      await act(async () => {
        expect(await send()).toBeUndefined();
      });
      expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(1);
      const write = fetchMock.mock.calls.find(([, init]) => init?.method === "POST")!;
      expect(write[0]).toBe(
        `/api/bloem/v1/admin/organization/invitations${action === "resend" ? "/3/resend" : ""}`,
      );
      expect(new Headers(write[1]?.headers).get("Authorization")).toBe(
        "Bearer dummy-admin-authority",
      );
      expect(JSON.parse(String(write[1]?.body))).toEqual(
        action === "create"
          ? {
              email: invitation.email,
              create_profile: true,
              show_tour: true,
              expected_revision: 7,
            }
          : { expected_revision: 7 },
      );
      await act(async () => {
        readback.resolve(listResponse());
        const handoff = await operation;
        if (outcome === "success") expect(handoff?.claim_url).toContain(token);
        else expect(handoff).toBeUndefined();
      });
      expect(view.result.current[action].isPending).toBe(false);
      if (outcome === "lost response")
        expect(toast.error).toHaveBeenCalledWith("Response lost after commit");
      expectNoCachedToken(view.client);
    },
  );

  it.each(["before request", "during request", "during readback", "unmount"])(
    "drops the handoff on authority loss %s",
    async (phase) => {
      const pending = deferred<Response>();
      let reads = 0;
      fetchMock.mockImplementation(async (_url, init) => {
        if (init?.method === "POST")
          return phase === "during readback" ? claimResponse() : pending.promise;
        reads++;
        return phase === "during readback" && reads === 2 ? pending.promise : listResponse();
      });
      const view = mountInvitations();
      await waitFor(() => expect(view.result.current.list.isSuccess).toBe(true));
      const send =
        action === "create"
          ? () => view.result.current.create.send(body)
          : () => view.result.current.resend.send(3);
      let operation!: ReturnType<typeof send>;
      if (phase === "before request") activateAdminV2Context("replacement", context.key);
      act(() => {
        operation = send();
      });
      if (phase !== "before request") {
        await waitFor(() =>
          expect(fetchMock.mock.calls.some(([, init]) => init?.method === "POST")).toBe(true),
        );
        if (phase === "during readback") await waitFor(() => expect(reads).toBe(2));
        if (phase === "unmount") view.unmount();
        else activateAdminV2Context("replacement", context.key);
      }
      await act(async () => {
        pending.resolve(phase === "during readback" ? listResponse() : claimResponse());
        expect(await operation).toBeUndefined();
      });
      expectNoCachedToken(view.client);
      expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(
        phase === "before request" ? 0 : 1,
      );
      if (phase === "during request") expect(reads).toBe(1);
    },
  );

  it("preserves legacy delivery and role without caching the response", async () => {
    fetchMock.mockImplementation(async (_url, init) =>
      init?.method === "POST" ? claimResponse(false) : listResponse(false),
    );
    const view = mountInvitations(false);
    await waitFor(() => expect(view.result.current.list.isSuccess).toBe(true));
    await act(async () => {
      const handoff = await (action === "create"
        ? view.result.current.create.send(body)
        : view.result.current.resend.send(3));
      expect(handoff).toEqual({
        invitation,
        email_sent: true,
        claim_url: `https://server.test/invite/${token}`,
      });
    });
    const write = fetchMock.mock.calls.find(([, init]) => init?.method === "POST")!;
    expect(write[0]).toBe(`/api/v1/admin/invitations${action === "resend" ? "/3/resend" : ""}`);
    if (action === "create") expect(JSON.parse(String(write[1]?.body))).toEqual(body);
    expectNoCachedToken(view.client);
  });
});
