// Bloem: Proxy Manager's comet timing for the Satellite view
// (web/src/lib/cometPath.ts), free of Cesium so it is tested on its own
// (satelliteRoutes.ts turns it into entities).
//
// A comet flies one stream's whole drawn journey, lap after lap at one
// constant speed. Where it is follows from the clock alone:
//
//   f(t) = ((t - epoch + phase * P) mod P) / P
//
// `f` is the head's fraction of the journey, `epoch` is fixed once per viewer,
// `phase` is a stable hash of the stream's id (so a rebuilt route resumes where
// it was and routes do not all start together) and `P` is the lap period. The
// trail is the slice [max(0, f - trailFrac), f] of the same lap, so it never
// joins the end of one lap to the start of the next.

import type { StreamLeg } from "./scene";

/** Shortest lap before the speed factor. */
export const COMET_MIN_PERIOD_S = 2.2;
/** Longest lap before the speed factor. */
export const COMET_MAX_PERIOD_S = 12;
/** Longest share of a lap the trail may cover, so a short lap never reads as a solid line. */
export const COMET_MAX_TRAIL_FRACTION = 0.5;
/** Proxy Manager's Calm speed factor (larger is slower). */
export const CALM_SPEED = 1.7;
/** Streams carry no byte count on the map, so every trail is Proxy Manager's middle length. */
export const COMET_TRAIL_S = 1.2;

/** Seconds per lap of a journey `km` long: short hops snap, ocean crossings sail. */
export function cometPeriodS(km: number, speed: number): number {
  const base = Math.min(
    COMET_MAX_PERIOD_S,
    Math.max(COMET_MIN_PERIOD_S, 2 + Math.max(0, km) / 1400),
  );
  return base * speed;
}

/** The trail as a share of one lap, at most `COMET_MAX_TRAIL_FRACTION`. */
export function trailFraction(trailS: number, periodS: number): number {
  if (!(periodS > 0)) return 0;
  return Math.min(COMET_MAX_TRAIL_FRACTION, Math.max(0, trailS) / periodS);
}

/** A stable value in [0, 1) for a route id (32-bit FNV-1a). */
export function routePhase(key: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < key.length; i++) {
    hash ^= key.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0) / 0x1_0000_0000;
}

/** The head's fraction of the journey, in [0, 1), `secondsSinceEpoch` after the epoch. */
export function lapFraction(secondsSinceEpoch: number, periodS: number, phase: number): number {
  if (!(periodS > 0) || !Number.isFinite(secondsSinceEpoch)) return 0;
  const f = ((((secondsSinceEpoch + phase * periodS) % periodS) + periodS) % periodS) / periodS;
  // Rounding can land exactly on 1, which is the next lap's start.
  return f >= 1 ? 0 : f;
}

/** The trail's slice of the current lap, never reaching back past its start. */
export function trailWindow(f: number, trailFrac: number): [number, number] {
  return [Math.max(0, f - trailFrac), f];
}

export type JourneySlice = Pick<StreamLeg, "start" | "end">;

/**
 * Which leg holds journey fraction `f`, and how far along that leg it is (`u`
 * in [0, 1]). Legs are sized by length, so equal steps of `f` cover equal
 * distances on every leg and the comet keeps one speed through each hop.
 */
export function legAt(legs: readonly JourneySlice[], f: number): { leg: number; u: number } {
  if (legs.length === 0) return { leg: 0, u: 0 };
  let leg = legs.length - 1;
  for (let i = 0; i < legs.length - 1; i++) {
    if (f < (legs[i]?.end ?? 1)) {
      leg = i;
      break;
    }
  }
  const { start, end } = legs[leg] ?? { start: 0, end: 1 };
  const u = end > start ? (f - start) / (end - start) : 0;
  return { leg, u: Math.min(1, Math.max(0, u)) };
}

/** Points in every comet trail. */
export const TRAIL_SAMPLES = 24;

/**
 * Journey fractions for a trail from `from` to `to`: `samples` evenly spaced
 * points, except that the one nearest each leg boundary inside the window
 * moves onto it, so the trail turns the corner at each hop with the drawn
 * route. The count never changes, so Cesium updates a trail in place. Written
 * into `out`, which is returned, so a per-frame caller reuses one array.
 */
export function trailFractions(
  legs: readonly JourneySlice[],
  from: number,
  to: number,
  samples = TRAIL_SAMPLES,
  out: number[] = [],
): number[] {
  const last = Math.max(1, samples - 1);
  out.length = last + 1;
  for (let j = 0; j <= last; j++) out[j] = j === last ? to : from + (to - from) * (j / last);
  if (to > from && last >= 2) {
    for (let i = 0; i + 1 < legs.length; i++) {
      const boundary = legs[i]?.end;
      if (boundary === undefined || boundary <= from || boundary >= to) continue;
      // The nearest sample is within half a spacing of the boundary, so the
      // points stay in order; the two ends are never moved.
      const j = Math.min(
        last - 1,
        Math.max(1, Math.round(((boundary - from) / (to - from)) * last)),
      );
      out[j] = boundary;
    }
  }
  return out;
}
