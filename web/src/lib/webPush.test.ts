import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { setAccessToken, setProfileId, setProfileToken } from "@/api/client";
import { disableWebPush } from "./webPush";

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  setAccessToken("account");
  setProfileId("profile");
  setProfileToken(null);
  vi.stubGlobal("PushManager", class {});
  vi.stubGlobal("Notification", { permission: "granted" });
});
afterEach(() => vi.unstubAllGlobals());
function browser() {
  const unsubscribe = vi.fn().mockResolvedValue(true);
  const subscription = { endpoint: "https://push.example.test/opaque", unsubscribe };
  const getSubscription = vi.fn().mockResolvedValue(subscription);
  vi.stubGlobal("navigator", {
    userAgent: "Synthetic Browser",
    serviceWorker: {
      getRegistration: vi.fn().mockResolvedValue({ pushManager: { getSubscription } }),
    },
  });
  return { unsubscribe, getSubscription };
}
it("removes server registration before local unsubscribe", async () => {
  const { unsubscribe } = browser();
  const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => {
    expect(unsubscribe).not.toHaveBeenCalled();
    return new Response(null, { status: 204 });
  });
  vi.stubGlobal("fetch", fetch);
  await disableWebPush();
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(String(fetch.mock.calls[0]![0])).toContain("/api/v2/notifications/web-push/unsubscribe");
  expect(fetch.mock.calls[0]![1]?.body).toBe(
    JSON.stringify({ endpoint: "https://push.example.test/opaque" }),
  );
  expect(unsubscribe).toHaveBeenCalledTimes(1);
});
it("preserves local endpoint on failed or uncertain server removal without replay", async () => {
  for (const status of [401, 403, 500]) {
    const { unsubscribe } = browser();
    const fetch = vi.fn().mockResolvedValue(new Response(null, { status }));
    vi.stubGlobal("fetch", fetch);
    await expect(disableWebPush()).rejects.toThrow();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(unsubscribe).not.toHaveBeenCalled();
  }
  const { unsubscribe } = browser();
  const fetch = vi.fn().mockRejectedValue(new TypeError("network interrupted"));
  vi.stubGlobal("fetch", fetch);
  await expect(disableWebPush()).rejects.toThrow();
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(unsubscribe).not.toHaveBeenCalled();
});
it("fences authority replacement during browser discovery and HTTP", async () => {
  const first = browser();
  first.getSubscription.mockImplementation(async () => {
    setProfileToken("new-pin");
    return { endpoint: "opaque", unsubscribe: first.unsubscribe };
  });
  const fetch = vi.fn().mockImplementation(async () => {
    setProfileToken("next-pin");
    return new Response(null, { status: 204 });
  });
  vi.stubGlobal("fetch", fetch);
  await expect(disableWebPush()).rejects.toThrow();
  expect(fetch).not.toHaveBeenCalled();
  expect(first.unsubscribe).not.toHaveBeenCalled();
  const second = browser();
  await expect(disableWebPush()).rejects.toThrow();
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(second.unsubscribe).not.toHaveBeenCalled();
});
it("surfaces local unsubscribe refusal after server success", async () => {
  const { unsubscribe } = browser();
  unsubscribe.mockResolvedValue(false);
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
  await expect(disableWebPush()).rejects.toThrow("browser subscription could not be removed");
});

it("surfaces discovery failure without claiming local removal", async () => {
  const { getSubscription, unsubscribe } = browser();
  getSubscription.mockRejectedValue(new Error("browser storage unavailable"));
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  await expect(disableWebPush()).rejects.toThrow("browser storage unavailable");
  expect(fetch).not.toHaveBeenCalled();
  expect(unsubscribe).not.toHaveBeenCalled();
});
