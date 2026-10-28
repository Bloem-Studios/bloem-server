import { act, fireEvent, render, screen } from "@testing-library/react";
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
    subscribe_label: "",
    subscribe_url: "",
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
const guestPresentation = (subscribe_url: string) => ({
  kind: "guest" as const,
  ends_at: null,
  cta_url: "https://store.test/trial",
  cta_label: "Start a trial!",
  item_cta_label: "",
  cta_message: "",
  subscribe_label: "Subscribe!",
  subscribe_url,
  launch_url: "",
  storefront_url: "https://store.test",
});
it("offers a guest the trial and the subscription side by side", async () => {
  const requests: string[] = [];
  vi.stubGlobal("fetch", async (url: unknown) => {
    requests.push(String(url));
    return new Response(null, { status: 204 });
  });
  render(
    <PreviewModeContext.Provider value={guestPresentation("https://store.test/plans")}>
      <PreviewBanner />
    </PreviewModeContext.Provider>,
  );
  expect(screen.getByRole("button", { name: "Start a trial!" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Exit preview" })).not.toBeInTheDocument();
  await act(async () => fireEvent.click(screen.getByRole("button", { name: "Subscribe!" })));
  // Leaving for the subscription ends the guest visit, as Exit preview does.
  expect(requests).toContain("/api/v2/auth/logout");
});
it("keeps Exit preview when the operator gives no subscription link", () => {
  render(
    <PreviewModeContext.Provider value={guestPresentation("")}>
      <PreviewBanner />
    </PreviewModeContext.Provider>,
  );
  expect(screen.getByRole("button", { name: "Exit preview" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Subscribe!" })).not.toBeInTheDocument();
});
