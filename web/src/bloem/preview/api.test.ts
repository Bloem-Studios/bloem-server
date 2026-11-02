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
it("marks the preview status pending until it answers, and knows the host before it clears", async () => {
  vi.resetModules();
  sessionStorage.clear();
  history.replaceState(null, "", "/");
  let answer: (response: Response) => void = () => {};
  vi.stubGlobal(
    "fetch",
    () =>
      new Promise<Response>((resolve) => {
        answer = resolve;
      }),
  );
  const session = await import("./session");
  const api = await import("./api");
  const seen: string[] = [];
  session.subscribePreviewSession(() =>
    seen.push(`pending=${session.previewDiscoveryPending()} host=${session.guestHost()}`),
  );

  const discovery = api.discoverPreviewHost();
  expect(session.previewDiscoveryPending()).toBe(true);
  answer(Response.json({ enabled: true, brand: "Example" }));
  await discovery;

  expect(session.previewDiscoveryPending()).toBe(false);
  expect(seen.at(-1)).toBe("pending=false host=true");
  expect(seen).not.toContain("pending=false host=false");
});
it("clears the pending preview status when the status request fails", async () => {
  vi.resetModules();
  sessionStorage.clear();
  history.replaceState(null, "", "/");
  vi.stubGlobal("fetch", async () => {
    throw new Error("offline");
  });
  const session = await import("./session");
  const api = await import("./api");
  await api.discoverPreviewHost();
  expect(session.previewDiscoveryPending()).toBe(false);
  expect(session.guestHost()).toBe(false);
});
