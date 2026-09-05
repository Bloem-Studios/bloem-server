import { expect, it } from "vitest";
import { activeSeason, type SeasonalPack } from "./schedule";
it("turns off at the exact end and excludes other surfaces", () => {
  const pack: SeasonalPack = {
    id: "winter",
    effect_id: "snow",
    intensity: 0.4,
    surfaces: ["home"],
    window: { starts_at: "2026-12-01T00:00:00Z", ends_at: "2027-01-01T00:00:00Z" },
  };
  expect(activeSeason(pack, "home", Date.parse(pack.window.starts_at))).toBe(true);
  expect(activeSeason(pack, "home", Date.parse(pack.window.ends_at))).toBe(false);
  expect(activeSeason(pack, "login", Date.parse(pack.window.starts_at))).toBe(false);
  expect(
    activeSeason(
      { ...pack, window: { ...pack.window, ends_at: "invalid" } },
      "home",
      Date.parse(pack.window.starts_at),
    ),
  ).toBe(false);
});
