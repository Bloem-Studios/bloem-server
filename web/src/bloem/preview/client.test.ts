import { beforeEach, expect, it, vi } from "vitest";

beforeEach(() => {
  vi.resetModules();
  localStorage.clear();
  sessionStorage.clear();
  history.replaceState(null, "", "/");
});
it("restores and rotates a guest token without reading or replacing the subscriber token", async () => {
  const session = await import("./session");
  session.storeGuestSession({
    refresh_token: "guest",
    expires_at: "2099-01-01T00:00:00Z",
    item: "",
  });
  localStorage.setItem("refresh_token", "subscriber");
  const requests: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url, options) => {
      requests.push(JSON.parse(options.body).refresh_token);
      return Response.json({
        access_token: "guest-access",
        refresh_token: "guest-rotated",
        expires_in: 60,
      });
    }),
  );
  const client = await import("@/api/client");
  expect(await client.bootstrapAccessToken()).toBe(true);
  expect(requests).toEqual(["guest"]);
  expect(session.guestRefreshToken()).toBe("guest-rotated");
  expect(localStorage.getItem("refresh_token")).toBe("subscriber");
  client.setProfileId(null);
  client.setProfileToken(null);
  expect(client.getAccessToken()).toBe("guest-access");
  vi.unstubAllGlobals();
});
it("recognizes a guest 401 with the native session-expired code without redirecting to login", async () => {
  const session = await import("./session");
  session.storeGuestSession({
    refresh_token: "guest",
    expires_at: "2099-01-01T00:00:00Z",
    item: "",
  });
  const client = await import("@/api/client");
  client.setAccessToken("guest-access");
  client.onSessionRejected(() => session.endGuestSession());
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      Response.json({ error: "unauthorized", code: "session_expired" }, { status: 401 }),
    ),
  );
  await client.fetchWithSession("/api/v2/account/me", {});
  expect(session.guestSession()?.ended).toBe(true);
  vi.unstubAllGlobals();
});
