import { act, render } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import PreviewBanner from "./PreviewBanner";
import { PreviewModeContext } from "./usePreviewMode";
import { storeGuestSession } from "./session";
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({ logout: () => {} }) }));
vi.mock("@/api/client", () => ({ getAccessToken: () => "synthetic-preview" }));
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it("records absolute guest expiry once even when the banner clock continues", () => {
  vi.useFakeTimers();
  const events: unknown[] = [];
  vi.stubGlobal("fetch", async (_url: unknown, init: RequestInit) => {
    events.push(JSON.parse(init.body as string));
    return new Response(null, { status: 204 });
  });
  const presentation = {
    kind: "guest" as const,
    ends_at: "2000-01-01T00:00:00Z",
    cta_url: "https://store.test/trial",
    cta_label: "Subscribe",
    item_cta_label: "",
    cta_message: "",
    launch_url: "",
    storefront_url: "",
  };
  storeGuestSession({
    refresh_token: "synthetic",
    expires_at: presentation.ends_at,
    item: "",
    presentation,
  });
  render(
    <PreviewModeContext.Provider value={presentation}>
      <PreviewBanner />
    </PreviewModeContext.Provider>,
  );
  act(() => vi.advanceTimersByTime(3000));
  expect(events).toEqual([{ event: "session_ended" }]);
});
