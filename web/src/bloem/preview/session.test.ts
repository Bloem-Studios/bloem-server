import { beforeEach, describe, expect, it, vi } from "vitest";

describe("preview session isolation", () => {
  beforeEach(() => {
    vi.resetModules();
    localStorage.clear();
    sessionStorage.clear();
    history.replaceState(null, "", "/");
  });

  it("keeps guest refresh rotation and exit separate from subscriber storage", async () => {
    const session = await import("./session");
    localStorage.setItem("refresh_token", "subscriber");
    localStorage.setItem("current_profile", "subscriber profile");
    session.storeGuestSession({
      refresh_token: "guest",
      expires_at: "2099-01-01T00:00:00Z",
      item: "series",
      ref: "affiliate",
    });
    expect(session.guestRefreshToken()).toBe("guest");
    session.setGuestRefreshToken("rotated");
    expect(session.guestRefreshToken()).toBe("rotated");
    session.endGuestSession();
    expect(session.guestRefreshToken()).toBeNull();
    expect(localStorage.getItem("refresh_token")).toBe("subscriber");
    expect(localStorage.getItem("current_profile")).toBe("subscriber profile");
    expect(session.guestHost()).toBe(true);
  });

  it("retains the ended state and signed attribution across a reload", async () => {
    let session = await import("./session");
    session.storeGuestSession({
      refresh_token: "guest",
      expires_at: "2000-01-01T00:00:00Z",
      item: "episode",
      ref: "signed-ref",
    });
    session.endGuestSession();
    vi.resetModules();
    session = await import("./session");
    expect(session.guestRefreshToken()).toBeNull();
    expect(session.guestSession()?.ended).toBe(true);
    expect(session.guestSession()?.ref).toBe("signed-ref");
  });

  it("does not treat malformed storage as live credentials", async () => {
    sessionStorage.setItem("bloem-preview-session", '{"refresh_token":7}');
    const session = await import("./session");
    expect(session.guestRefreshToken()).toBeNull();
  });
});
