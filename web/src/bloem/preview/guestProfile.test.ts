import { beforeEach, describe, expect, it, vi } from "vitest";

// The v2 home and library layouts require X-Profile-Id. A guest never picks a
// profile, so its tab session carries the shared account's profile, and only
// that session: nothing reaches subscriber storage.
describe("preview guest profile", () => {
  beforeEach(() => {
    vi.resetModules();
    localStorage.clear();
    sessionStorage.clear();
    history.replaceState(null, "", "/");
  });

  it("scopes guest requests to the session's profile without storing it", async () => {
    const session = await import("./session");
    const client = await import("@/api/client");
    session.storeGuestSession({
      refresh_token: "guest",
      expires_at: "2099-01-01T00:00:00Z",
      item: "",
    });
    client.setAccessToken("guest-access");
    expect(client.captureProfileRequestContext()).toBeNull();

    session.setGuestProfile("guest-profile");
    // A sign-in clears the selected profile; the guest's must survive it.
    client.setProfileId(null);
    expect(client.captureProfileRequestContext()?.profileId).toBe("guest-profile");
    expect(localStorage.getItem("profile_id")).toBeNull();

    session.endGuestSession();
    expect(client.captureProfileRequestContext()).toBeNull();
  });

  it("keeps the profile across a reload of the guest tab", async () => {
    let session = await import("./session");
    session.storeGuestSession({
      refresh_token: "guest",
      expires_at: "2099-01-01T00:00:00Z",
      item: "",
    });
    session.setGuestProfile("guest-profile");
    vi.resetModules();
    session = await import("./session");
    expect(session.guestProfileId()).toBe("guest-profile");
  });
});
