import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  adminV2Api,
  mintAdminContextSession,
  onAdminV2ContextFailure,
  setAdminV2Token,
} from "./adminV2Client";

describe("adminV2 client", () => {
  beforeEach(() => {
    window.localStorage.clear();
    window.sessionStorage.clear();
    setAdminV2Token(null);
  });

  afterEach(() => {
    onAdminV2ContextFailure(null);
    vi.unstubAllGlobals();
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
      "/api/v2/admin/organization/overview",
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
      "/api/v2/admin/session",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ authorization: "Bearer account-token" }),
        body: JSON.stringify({ scope: "organization", organization_id: "org-a" }),
      }),
    );
    expect(result.context.organizationId).toBe("org-a");
  });
});
