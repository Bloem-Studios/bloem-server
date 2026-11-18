import { beforeEach, describe, expect, it, vi } from "vitest";

// Silo's useAuth.tsx reads storage and the stored impersonation session through
// Bloem's adapters (bloem-preview-plugin.ts). On the guest host they serve the
// guest's session and touch nothing of the account's; elsewhere they are Silo's.
describe("useAuth's preview adapters", () => {
  beforeEach(() => {
    vi.resetModules();
    localStorage.clear();
    sessionStorage.clear();
  });

  async function load(guest: boolean) {
    const session = await import("./session");
    const auth = await import("./adapters/authStorage");
    const impersonation = await import("./adapters/authImpersonation");
    const silo = await import("@/utils/storage");
    session.setGuestHost(guest);
    if (guest) {
      session.storeGuestSession({
        refresh_token: "guest-refresh",
        expires_at: "2099-01-01T00:00:00Z",
        item: "",
      });
    }
    silo.storage.set(silo.storage.KEYS.REFRESH_TOKEN, "account-refresh");
    silo.storage.set(silo.storage.KEYS.CURRENT_PROFILE, '{"id":"p1"}');
    const siloImpersonation = await import("@/lib/impersonationSession");
    siloImpersonation.saveStoredImpersonationAdminSession({
      accessToken: "admin-access",
      refreshToken: "admin-refresh",
      returnPath: "/admin",
    });
    return { auth, impersonation, silo };
  }

  it("serves the guest's session on the guest host", async () => {
    const { auth, impersonation, silo } = await load(true);
    expect(auth.storage.get(auth.storage.KEYS.REFRESH_TOKEN)).toBe("guest-refresh");
    auth.storage.remove(auth.storage.KEYS.CURRENT_PROFILE);
    expect(silo.storage.get(silo.storage.KEYS.CURRENT_PROFILE)).toBe('{"id":"p1"}');
    expect(impersonation.loadStoredImpersonationAdminSession()).toBeNull();
    impersonation.clearStoredImpersonationAdminSession();
    expect(
      (await import("@/lib/impersonationSession")).loadStoredImpersonationAdminSession(),
    ).not.toBeNull();
  });

  it("is Silo's storage everywhere else", async () => {
    const { auth, impersonation, silo } = await load(false);
    expect(auth.storage.get(auth.storage.KEYS.REFRESH_TOKEN)).toBe("account-refresh");
    auth.storage.remove(auth.storage.KEYS.CURRENT_PROFILE);
    expect(silo.storage.get(silo.storage.KEYS.CURRENT_PROFILE)).toBeNull();
    expect(impersonation.loadStoredImpersonationAdminSession()?.refreshToken).toBe("admin-refresh");
    impersonation.clearStoredImpersonationAdminSession();
    expect(impersonation.loadStoredImpersonationAdminSession()).toBeNull();
  });
});
