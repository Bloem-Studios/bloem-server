import { expect, test, type Page } from "@playwright/test";
import { previewFixture, type PreviewFixtureState } from "./preview-fixtures";
async function install(page: Page, state: PreviewFixtureState) {
  await page.route("**/api/**", async (route) => {
    const req = route.request();
    const pathname = decodeURIComponent(new URL(req.url()).pathname);
    if (!pathname.startsWith("/api/")) return route.continue();
    const response = previewFixture(req.method(), pathname, state);
    await route.fulfill({
      status: response.status,
      contentType: response.status >= 400 ? "application/problem+json" : "application/json",
      body: response.body === undefined ? "" : JSON.stringify(response.body),
    });
  });
  await page.addInitScript(() => {
    localStorage.setItem("refresh_token", "preserved-subscriber");
  });
}
test("launch, browse series/season/episode, and subscribe without writes or error toasts", async ({
  page,
}) => {
  const state: PreviewFixtureState = { exchanges: 0, unexpected: [] };
  await install(page, state);
  await page.goto("/preview/start#ticket=synthetic-ticket");
  await expect(page.getByRole("heading", { name: "Severance", exact: true })).toBeVisible();
  expect(new URL(page.url()).hash).toBe("");
  expect(state.exchanges).toBe(1);
  await page.getByRole("link", { name: "Details", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Seasons", exact: true })).toBeVisible();
  await page
    .getByRole("link", { name: /season 1/i })
    .first()
    .click();
  await expect(page.getByRole("heading", { name: /Episodes/i }).first()).toBeVisible();
  await page
    .getByRole("link", { name: /Good News About Hell/i })
    .first()
    .click();
  await expect(page.getByRole("button", { name: "Subscribe to watch", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Subscribe to watch", exact: true }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(
    page.getByRole("dialog").getByRole("link", { name: "Subscribe to Acme" }),
  ).toHaveAttribute("href", /src=preview&item=episode%3Aseverance-s01e01&ref=signed-affiliate/);
  expect(state.unexpected).toEqual([]);
  await expect(page.locator('[data-sonner-toast][data-type="error"]')).toHaveCount(0);
  expect(await page.evaluate(() => localStorage.getItem("refresh_token"))).toBe(
    "preserved-subscriber",
  );
});
test("used ticket lands on operator launch and trial options", async ({ page }) => {
  const state: PreviewFixtureState = { exchanges: 0, unexpected: [], invalidTicket: true };
  await install(page, state);
  await page.goto("/preview/start#ticket=used");
  await expect(page.getByRole("heading", { name: "Start browsing from Acme" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Browse the library" })).toHaveAttribute(
    "href",
    "https://store.example.test/preview/launch",
  );
  expect(new URL(page.url()).hash).toBe("");
  expect(state.exchanges).toBe(1);
});
test("collections browse without personal collections or editing controls", async ({ page }) => {
  const state: PreviewFixtureState = { exchanges: 0, unexpected: [] };
  await install(page, state);
  await page.goto("/preview/start#ticket=synthetic-ticket");
  await expect(page.getByRole("heading", { name: "Severance", exact: true })).toBeVisible();
  await page.goto("/collections");
  await expect(page.getByRole("heading", { name: "Collections", exact: true })).toBeVisible();
  await expect(page.getByRole("status", { name: "Loading collections…" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "New collection", exact: true })).toHaveCount(0);
  expect(state.unexpected).toEqual([]);
});
test("expired guest reload keeps the session-ended options without login", async ({ page }) => {
  const state: PreviewFixtureState = { exchanges: 0, unexpected: [] };
  await install(page, state);
  await page.goto("/preview/start#ticket=synthetic-ticket");
  await expect(page.getByRole("heading", { name: "Severance", exact: true })).toBeVisible();
  state.expired = true;
  await page.reload();
  await expect
    .poll(() =>
      page.evaluate(() => {
        const value = JSON.parse(sessionStorage.getItem("bloem-preview-session") ?? "null");
        return { ended: value?.ended, presentation: Boolean(value?.presentation) };
      }),
    )
    .toEqual({ ended: true, presentation: true });
  await expect(page.getByRole("dialog")).toContainText("Your preview session has ended");
  await expect(page.getByRole("link", { name: "Keep browsing" })).toHaveAttribute(
    "href",
    "https://store.example.test/preview/launch",
  );
  await expect(page.getByRole("link", { name: "Start your trial" })).toHaveAttribute(
    "href",
    /src=preview&ref=signed-affiliate/,
  );
  expect(new URL(page.url()).pathname).not.toBe("/login");
  await page.reload();
  await expect(page.getByRole("dialog")).toContainText("Your preview session has ended");
  expect(state.exchanges).toBe(1);
  expect(state.unexpected).toEqual([]);
});
test("personal preview bypasses onboarding and redirects direct reading to the CTA", async ({
  page,
}) => {
  const state: PreviewFixtureState = { exchanges: 0, unexpected: [], kind: "personal" };
  await install(page, state);
  await page.goto("/reader/ebook/episode:severance-s01e01");
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(
    page.locator("[role=status]").filter({ hasText: "Your preview ends on" }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: "Keep browsing" })).toHaveCount(0);
  expect(new URL(page.url()).pathname).toBe("/item/episode:severance-s01e01");
  expect(state.unexpected).toEqual([]);
});

test("guards every action route before its protected requests can run", async ({ page }) => {
  const state: PreviewFixtureState = { exchanges: 0, unexpected: [], kind: "personal" };
  await install(page, state);
  for (const path of [
    "/watch/episode:severance-s01e01",
    "/reader/ebook/episode:severance-s01e01",
    "/watch/live/channel",
    "/livetv",
    "/rooms/join",
    "/taste-seed",
    "/settings/playback",
    "/profiles",
    "/notifications",
    "/collections/new",
    "/collections/list/edit",
    "/requests",
    "/title/movie/12",
    "/activate",
    "/household-setup",
    "/recommendations",
  ]) {
    await page.goto(path);
    await expect(page.getByRole("dialog"), path).toBeVisible();
    expect(new URL(page.url()).pathname, path).not.toBe(path);
    expect(state.unexpected, path).toEqual([]);
  }
});

test("preview host login links use the public landing instead of signing in", async ({ page }) => {
  const state: PreviewFixtureState = { exchanges: 0, unexpected: [] };
  await install(page, state);
  await page.addInitScript(() => localStorage.removeItem("refresh_token"));
  await page.goto("/login");
  await expect(page.getByRole("heading", { name: "Start browsing from Acme" })).toBeVisible();
  await expect(page.getByRole("textbox")).toHaveCount(0);
  expect(state.exchanges).toBe(0);
});
