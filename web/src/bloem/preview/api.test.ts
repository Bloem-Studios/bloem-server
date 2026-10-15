import { afterEach, expect, it, vi } from "vitest";
afterEach(() => vi.unstubAllGlobals());
it("does not turn the normal server host into a guest host after visiting preview/start", async () => {
  vi.resetModules();
  sessionStorage.clear();
  history.replaceState(null, "", "/preview/start");
  const paths: string[] = [];
  vi.stubGlobal("fetch", async (url: string) => {
    paths.push(url);
    return url.endsWith("/status")
      ? Response.json({ enabled: true })
      : Response.json({ code: "preview_unavailable" }, { status: 404 });
  });
  const session = await import("./session");
  const api = await import("./api");
  await expect(api.exchangePreviewTicket("synthetic")).rejects.toThrow();
  expect(session.guestHost()).toBe(false);
  expect(paths).toEqual(["/api/bloem/v1/preview/status"]);
});
