import { describe, expect, it } from "vitest";
import { ChapterGate } from "./playback-policy";
describe("playback chapter gate", () => {
  it("requires a natural crossing and limits the episode to one overlay", () => {
    const g = new ChapterGate();
    expect(g.sample(9, 0, true, false, [10, 20], true)).toBe(false);
    expect(g.sample(10, 1000, true, false, [10, 20], true)).toBe(true);
    expect(g.sample(19, 2000, true, false, [10, 20], true)).toBe(false);
    expect(g.sample(20, 3000, true, false, [10, 20], true)).toBe(false);
  });
  it("rejects seeks, background gaps and pause/resume crossings", () => {
    for (const mode of ["seek", "gap", "pause"]) {
      const g = new ChapterGate();
      g.sample(9, 0, true, false, [10], true);
      expect(
        g.sample(
          mode === "seek" ? 60 : 10,
          mode === "gap" ? 10000 : 1000,
          mode !== "pause",
          mode === "seek",
          [10],
          true,
        ),
      ).toBe(false);
      expect(g.sample(11, 11000, true, false, [10], true)).toBe(false);
    }
  });
  it("does not consume a boundary when a card is unavailable", () => {
    const g = new ChapterGate();
    g.sample(9, 0, true, false, [10, 20], false);
    expect(g.sample(10, 1000, true, false, [10, 20], false)).toBe(false);
    g.sample(19, 2000, true, true, [10, 20], true);
    g.sample(19.5, 2500, true, false, [10, 20], true);
    expect(g.sample(20, 3000, true, false, [10, 20], true)).toBe(true);
  });
});
