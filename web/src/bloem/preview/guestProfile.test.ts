import { beforeEach, describe, expect, it, vi } from "vitest";

// The v2 home and library layouts require X-Profile-Id. A guest selects the
// shared account's profile like anyone else, but the preview host must not
// write that authority to storage, so it lives in the tab's memory.
describe("preview guest profile", () => {
  beforeEach(() => {
    vi.resetModules();
    localStorage.clear();
    sessionStorage.clear();
    history.replaceState(null, "", "/");
  });

  it("scopes requests to the selected profile without storing it", async () => {
    const session = await import("./session");
    const client = await import("@/api/client");
    session.setGuestHost(true);
    client.setAccessToken("guest-access");
    expect(client.captureProfileRequestContext()).toBeNull();

    client.setProfileId("guest-profile");
    expect(client.captureProfileRequestContext()?.profileId).toBe("guest-profile");
    expect(localStorage.getItem("profile_id")).toBeNull();
    expect(sessionStorage.getItem("profile_id")).toBeNull();

    client.setProfileId(null);
    expect(client.captureProfileRequestContext()).toBeNull();
  });

  it("leaves a subscriber's stored profile alone", async () => {
    const session = await import("./session");
    const client = await import("@/api/client");
    session.setGuestHost(false);
    client.setAccessToken("subscriber-access");
    client.setProfileId("subscriber-profile");
    expect(client.captureProfileRequestContext()?.profileId).toBe("subscriber-profile");
    expect(localStorage.getItem("profile_id")).not.toBeNull();
  });
});
