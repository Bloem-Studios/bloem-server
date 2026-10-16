import { describe, expect, it } from "vitest";
import { previewRouteRedirect, subscriptionHref } from "./routes";

describe("preview route boundary", () => {
  it.each([
    "/watch/movie:1",
    "/reader/ebook/book:1",
    "/watch/live/12",
    "/livetv",
    "/rooms",
    "/rooms/join",
    "/rooms/abc",
    "/taste-seed",
    "/profiles",
    "/settings",
    "/settings/account",
    "/notifications",
    "/collections/new",
    "/collections/list/edit",
    "/requests",
    "/requests/discover/new",
    "/title/movie/123",
    "/activate",
    "/household-setup",
    "/recommendations",
    "/favorites",
    "/watchlist",
    "/history",
  ])("blocks %s before its page mounts", (route) => {
    expect(previewRouteRedirect(route, "")).toMatch(/cta=1/);
  });
  it("returns reader chapters to their containing item", () => {
    expect(
      previewRouteRedirect("/reader/ebook/chapter", "?backTo=%2Fitem%2Fseries%3FlibraryId%3D25"),
    ).toBe("/item/series?libraryId=25&cta=1");
  });
  it.each(["https://evil.test", "//evil.test", "/watch/evil", "/item/x#secret"])(
    "rejects unsafe backTo %s",
    (back) => {
      expect(
        previewRouteRedirect("/reader/ebook/chapter", `?backTo=${encodeURIComponent(back)}`),
      ).toBe("/item/chapter?cta=1");
    },
  );
  it.each([
    "/",
    "/library/25",
    "/catalog",
    "/item/series",
    "/person/cast",
    "/collections",
    "/calendar",
  ])("allows metadata route %s", (route) => {
    expect(previewRouteRedirect(route, "")).toBeNull();
  });
  it("also excludes personal catalog selectors", () => {
    expect(previewRouteRedirect("/catalog", "?source=favorites")).toBe("/?cta=1");
  });
});
describe("preview subscription attribution", () => {
  it("keeps the signed affiliate and other checkout parameters", () => {
    expect(
      subscriptionHref(
        "https://store.example.test/trial?plan=7&ref=old",
        "episode:1",
        "signed affiliate",
      ),
    ).toBe(
      "https://store.example.test/trial?plan=7&src=preview&preview_item=episode%3A1&ref=signed+affiliate",
    );
  });
  it("keeps the plan a store's cart link names with item", () => {
    expect(
      subscriptionHref(
        "https://store.example.test/orders/new?item=plan-1&price=price-1",
        "movie:1",
      ),
    ).toBe(
      "https://store.example.test/orders/new?item=plan-1&price=price-1&src=preview&preview_item=movie%3A1",
    );
  });
  it("does not invent an affiliate", () => {
    expect(subscriptionHref("https://store.example.test/trial?ref=old", undefined)).toBe(
      "https://store.example.test/trial?src=preview",
    );
  });
  it.each(["javascript:alert(1)", "data:text/html,hello", "//evil.test"])(
    "refuses unsafe CTA %s",
    (url) => {
      expect(subscriptionHref(url, "movie")).toBeNull();
    },
  );
});
