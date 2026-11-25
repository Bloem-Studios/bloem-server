import { describe, expect, it } from "vitest";

import {
  cometPeriodS,
  lapFraction,
  legAt,
  routePhase,
  trailFraction,
  trailFractions,
  trailWindow,
  COMET_MAX_PERIOD_S,
  COMET_MIN_PERIOD_S,
} from "./cometPath";

const LEGS = [
  { start: 0, end: 0.25 },
  { start: 0.25, end: 1 },
];

describe("comet timing", () => {
  it("laps short hops quickly and long crossings slowly, within bounds", () => {
    expect(cometPeriodS(0, 1)).toBe(COMET_MIN_PERIOD_S);
    expect(cometPeriodS(7000, 1)).toBeCloseTo(7, 10);
    expect(cometPeriodS(50_000, 1)).toBe(COMET_MAX_PERIOD_S);
    expect(cometPeriodS(7000, 1.7)).toBeCloseTo(11.9, 10);
  });

  it("keeps the trail to at most half a lap", () => {
    expect(trailFraction(1.2, 12)).toBeCloseTo(0.1, 10);
    expect(trailFraction(10, 4)).toBe(0.5);
    expect(trailFraction(1, 0)).toBe(0);
  });

  it("gives each route a stable phase in [0, 1)", () => {
    const phase = routePhase("session-1");
    expect(routePhase("session-1")).toBe(phase);
    expect(phase).toBeGreaterThanOrEqual(0);
    expect(phase).toBeLessThan(1);
    expect(routePhase("session-2")).not.toBe(phase);
  });

  it("finds the head's place in its lap from the clock alone", () => {
    expect(lapFraction(0, 4, 0)).toBe(0);
    expect(lapFraction(1, 4, 0)).toBe(0.25);
    expect(lapFraction(5, 4, 0)).toBe(0.25);
    expect(lapFraction(-1, 4, 0)).toBe(0.75);
    expect(lapFraction(1, 4, 0.5)).toBe(0.75);
  });

  it("starts each lap's trail from nothing", () => {
    expect(trailWindow(0.05, 0.1)).toEqual([0, 0.05]);
    expect(trailWindow(0.5, 0.1)).toEqual([0.4, 0.5]);
  });

  it("places a journey fraction on its leg", () => {
    expect(legAt(LEGS, 0.125)).toEqual({ leg: 0, u: 0.5 });
    expect(legAt(LEGS, 0.625)).toEqual({ leg: 1, u: 0.5 });
    expect(legAt([], 0.5)).toEqual({ leg: 0, u: 0 });
  });

  it("turns the trail's corner exactly at a hop", () => {
    const fractions = trailFractions(LEGS, 0.2, 0.3, 5);
    expect(fractions).toHaveLength(5);
    expect(fractions[0]).toBe(0.2);
    expect(fractions[4]).toBe(0.3);
    expect(fractions).toContain(0.25);
  });
});
