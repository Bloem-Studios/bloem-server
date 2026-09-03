// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  adminV2Api,
  adminV2QueryKey,
  activateAdminV2Context,
  mintAdminContextSession,
  onAdminV2ContextFailure,
  setAdminV2Token,
} from "./adminV2Client";
import { setAccessToken } from "./client";

describe("adminV2 client", () => {
  beforeEach(() => {
    if (!window.localStorage) {
      const values = new Map<string, string>();
      Object.defineProperty(window, "localStorage", {
        configurable: true,
        value: {
          clear: () => values.clear(),
          getItem: (key: string) => values.get(key) ?? null,
          removeItem: (key: string) => values.delete(key),
          setItem: (key: string, value: string) => values.set(key, value),
        },
      });
      Object.defineProperty(window, "sessionStorage", {
        configurable: true,
        value: {
          clear: () => values.clear(),
          getItem: (key: string) => values.get(key) ?? null,
          removeItem: (key: string) => values.delete(key),
          setItem: (key: string, value: string) => values.set(key, value),
        },
      });
    }
    window.localStorage.clear();
    window.sessionStorage.clear();
    setAdminV2Token(null);
  });

  afterEach(() => {
    onAdminV2ContextFailure(null);
    vi.unstubAllGlobals();
  });

  it("preserves field-addressable validation errors", async () => {
    activateAdminV2Context("admin-token", "platform");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: "validation_failed",
            message: "Invalid fields",
            fields: { slug: "is already used" },
          }),
          { status: 422, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );
    await expect(adminV2Api("/platform/organizations")).rejects.toMatchObject({
      code: "validation_failed",
      fields: { slug: "is already used" },
    });
  });

  it("does not replay an ordinary unsafe request after a 401", async () => {
    activateAdminV2Context("expired-admin", "platform");
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        Response.json({ error: "tenant_session_required", message: "expired" }, { status: 401 }),
      );
    vi.stubGlobal("fetch", fetchMock);

    await expect(adminV2Api("/unsafe", { method: "POST" }, "none")).rejects.toMatchObject({
      status: 401,
    });
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("retries lifecycle availability failures with one key and body", async () => {
    activateAdminV2Context("admin-token", "platform");
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(Response.json({ error: "busy" }, { status: 503 }))
      .mockResolvedValueOnce(Response.json({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      adminV2Api(
        "/platform/organizations",
        { method: "POST", body: '{"name":"A"}' },
        "idempotentLifecycle",
      ),
    ).resolves.toEqual({ ok: true });
    const first = fetchMock.mock.calls[0]?.[1] as RequestInit;
    const second = fetchMock.mock.calls[1]?.[1] as RequestInit;
    expect(second.body).toBe(first.body);
    expect((second.headers as Record<string, string>)["idempotency-key"]).toBe(
      (first.headers as Record<string, string>)["idempotency-key"],
    );
  });

  it("remints the selected context once before replaying a lifecycle 401", async () => {
    setAccessToken("account-token");
    activateAdminV2Context("expired-admin", "organization:org-a");
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/api/bloem/v1/admin/session") {
        return jsonResponse({
          access_token: "fresh-admin",
          expires_at: "2026-08-13T12:15:00Z",
          context: {
            key: "organization:org-a",
            scope: "organization",
            organization_id: "org-a",
            name: "Org A",
            status: "active",
            authority: "organization_admin",
          },
        });
      }
      const token = (init?.headers as Record<string, string>).authorization;
      return token === "Bearer expired-admin"
        ? Response.json({ error: "tenant_session_required", message: "expired" }, { status: 401 })
        : Response.json({ ok: true });
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      adminV2Api(
        "/organization/people/7/profiles/p",
        { method: "PATCH", body: '{"group_id":2}' },
        "idempotentLifecycle",
      ),
    ).resolves.toEqual({ ok: true });
    const mutationCalls = fetchMock.mock.calls.filter(
      ([input]) => String(input) !== "/api/bloem/v1/admin/session",
    );
    expect(mutationCalls).toHaveLength(2);
    expect((mutationCalls[1]?.[1]?.headers as Record<string, string>).authorization).toBe(
      "Bearer fresh-admin",
    );
    expect((mutationCalls[1]?.[1]?.headers as Record<string, string>)["idempotency-key"]).toBe(
      (mutationCalls[0]?.[1]?.headers as Record<string, string>)["idempotency-key"],
    );
  });

  it("keeps the administrative token in memory while attaching it to v2 requests", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ organization: "org-a" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    setAdminV2Token("short-lived-admin-token");
    await adminV2Api("/organization/overview");

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/bloem/v1/admin/organization/overview",
      expect.objectContaining({
        headers: expect.objectContaining({ authorization: "Bearer short-lived-admin-token" }),
      }),
    );
    expect(JSON.stringify({ ...window.localStorage })).not.toContain("short-lived-admin-token");
    expect(JSON.stringify({ ...window.sessionStorage })).not.toContain("short-lived-admin-token");
  });

  it("discards a stale administrative token before notifying the provider", async () => {
    const failure = vi.fn();
    onAdminV2ContextFailure(failure);
    setAdminV2Token("stale-token");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: "authorization_state_stale",
            message: "Tenant authorization state is stale",
          }),
          { status: 401, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    await expect(adminV2Api("/organization/overview")).rejects.toMatchObject({
      status: 401,
      code: "authorization_state_stale",
    });
    expect(failure).toHaveBeenCalledWith({
      code: "authorization_state_stale",
      message: "Tenant authorization state is stale",
    });
    await expect(adminV2Api("/organization/overview")).rejects.toMatchObject({
      code: "tenant_session_required",
    });
  });

  it.each([
    [401, "tenant_session_required"],
    [401, "authorization_state_stale"],
    [403, "insufficient_platform_authority"],
    [403, "organization_suspended"],
  ])("invalidates the context token on %s %s", async (status, code) => {
    const failure = vi.fn();
    onAdminV2ContextFailure(failure);
    activateAdminV2Context("context-token", "platform");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: code, message: code }), {
          status,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    await expect(adminV2Api("/platform/organizations")).rejects.toMatchObject({ code });
    expect(failure).toHaveBeenCalledWith({ code, message: code });
    await expect(adminV2Api("/platform/organizations")).rejects.toMatchObject({
      code: "tenant_session_required",
    });
  });

  it("aborts and ignores an old context response after switching", async () => {
    let resolveOld!: (response: Response) => void;
    const oldResponse = new Promise<Response>((resolve) => {
      resolveOld = resolve;
    });
    const fetchMock = vi.fn().mockReturnValue(oldResponse);
    vi.stubGlobal("fetch", fetchMock);
    activateAdminV2Context("org-a-token", "organization:org-a");

    const request = adminV2Api<{ member: string }>("/organization/people", { method: "POST" });
    const oldSignal = (fetchMock.mock.calls[0]?.[1] as RequestInit).signal;
    activateAdminV2Context("org-b-token", "organization:org-b");

    expect(oldSignal?.aborted).toBe(true);
    resolveOld(jsonResponse({ member: "Org A member" }));
    await expect(request).rejects.toMatchObject({ name: "AbortError" });
  });

  it("builds every administrative query key under its context identity", () => {
    expect(adminV2QueryKey("organization:org-a", "people", { status: "active" })).toEqual([
      "admin-v2",
      "organization:org-a",
      "people",
      { status: "active" },
    ]);
  });

  it("mints a selected organization session with account authentication", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          access_token: "context-token",
          expires_at: "2026-08-13T12:15:00Z",
          context: {
            key: "organization:org-a",
            scope: "organization",
            organization_id: "org-a",
            name: "Org A",
            status: "active",
            authority: "organization_admin",
            policy_revision: 7,
            security_revision: 11,
          },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const result = await mintAdminContextSession("organization:org-a", "account-token");

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/bloem/v1/admin/session",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ authorization: "Bearer account-token" }),
        body: JSON.stringify({ scope: "organization", organization_id: "org-a" }),
      }),
    );
    expect(result.context.organizationId).toBe("org-a");
  });
});

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}
