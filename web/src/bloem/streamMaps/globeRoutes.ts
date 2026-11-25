// Bloem: Proxy Manager's globe route dashes (web/src/lib/globeRoutes.ts) for
// the stream globe, in its Calm mood. react-globe.gl dashes each path or arc
// datum on its own, in fractions of that datum's length; each drawn leg here
// is one datum carrying its angular length, so one dash size, gap and speed in
// degrees runs on every leg of every route, and each leg's pattern continues
// the one before it.

import { EARTH_RADIUS_KM, greatCircleKm, type RoutePoint, type StreamRoute } from "./scene";

export type GlobeRouteLayer = "path" | "arc";

export interface GlobeLegDatum {
  /** `${route id}|${leg index}`: one datum per drawn leg. */
  id: string;
  routeId: string;
  // The Arcs layer's default accessors read these four fields.
  startLat: number;
  startLng: number;
  endLat: number;
  endLng: number;
  /** `[[lat, lng], [lat, lng]]`, for the Paths layer's `pathPoints`. */
  points: [number, number][];
  color: string;
  /** A live stream's line is drawn heavier. */
  live: boolean;
  /** Touches an approximate viewer location: drawn as short dots. */
  dotted: boolean;
  /** Angular length, in degrees, of the line this layer draws for the leg. */
  legDeg: number;
  /** Angular length, in degrees, of the route's legs drawn before this one. */
  startDeg: number;
}

/** Proxy Manager's Calm dashes, in degrees along the line. */
export const GLOBE_DASH = { dashDeg: 5, gapDeg: 3, dotDeg: 0.8, msPerDeg: 250 } as const;

export const DOTS_PER_DASH = 4;

/** `moving`: dashes run. `still` (a past window, reduced motion): lines are solid; dotted legs stay dotted. */
export type GlobeRouteMotion = "moving" | "still";

const RAD_TO_DEG = 180 / Math.PI;
const modulo = (value: number, n: number) => ((value % n) + n) % n;

/** Great-circle angle between two points, in degrees. */
export function greatCircleDeg(a: RoutePoint, b: RoutePoint): number {
  return (greatCircleKm(a, b) / EARTH_RADIUS_KM) * RAD_TO_DEG;
}

/** three-globe's default `pathResolution`: the longest lat/lng step, in degrees, it draws as one segment. */
const PATH_RESOLUTION_DEG = 2;

/**
 * The angular length of the line the Paths layer draws from `from` to `to`:
 * three-globe crosses the antimeridian when that is shorter, interpolates in
 * latitude and longitude in steps of at most `PATH_RESOLUTION_DEG`, and joins
 * the steps with straight chords, whose summed length its dashes are relative to.
 */
export function pathLineDeg(from: RoutePoint, to: RoutePoint): number {
  let fromLon = from.lon;
  while (Math.abs(fromLon - to.lon) > 180) fromLon += 360 * (fromLon < to.lon ? 1 : -1);
  const span = Math.hypot(to.lat - from.lat, to.lon - fromLon);
  const steps = span > PATH_RESOLUTION_DEG ? Math.floor(span / PATH_RESOLUTION_DEG) + 1 : 1;
  let total = 0;
  let previous: RoutePoint = { lat: from.lat, lon: fromLon };
  for (let k = 1; k <= steps; k++) {
    const next = {
      lat: from.lat + ((to.lat - from.lat) * k) / steps,
      lon: fromLon + ((to.lon - fromLon) * k) / steps,
    };
    total += 2 * Math.sin(greatCircleDeg(previous, next) / RAD_TO_DEG / 2);
    previous = next;
  }
  return total * RAD_TO_DEG;
}

/** One datum per leg of every route, measured the way `layer` draws it. */
export function globeLegDatums(
  routes: readonly StreamRoute[],
  layer: GlobeRouteLayer,
): GlobeLegDatum[] {
  const out: GlobeLegDatum[] = [];
  for (const route of routes) {
    let startDeg = 0;
    route.legs.forEach((leg, i) => {
      const legDeg =
        layer === "path" ? pathLineDeg(leg.from, leg.to) : greatCircleDeg(leg.from, leg.to);
      out.push({
        id: `${route.id}|${i}`,
        routeId: route.id,
        startLat: leg.from.lat,
        startLng: leg.from.lon,
        endLat: leg.to.lat,
        endLng: leg.to.lon,
        points: [
          [leg.from.lat, leg.from.lon],
          [leg.to.lat, leg.to.lon],
        ],
        color: route.colour,
        live: route.live,
        dotted: leg.dotted,
        legDeg,
        startDeg,
      });
      startDeg += legDeg;
    });
  }
  return out;
}

/** The pattern a leg is drawn with, or null for a solid line. */
export function legDashPattern(
  dotted: boolean,
  motion: GlobeRouteMotion,
): { dashDeg: number; gapDeg: number } | null {
  if (dotted) {
    const period = (GLOBE_DASH.dashDeg + GLOBE_DASH.gapDeg) / DOTS_PER_DASH;
    return { dashDeg: GLOBE_DASH.dotDeg, gapDeg: period - GLOBE_DASH.dotDeg };
  }
  return motion === "still" ? null : { dashDeg: GLOBE_DASH.dashDeg, gapDeg: GLOBE_DASH.gapDeg };
}

/**
 * Where a leg's pattern starts, in degrees, so it continues the legs drawn
 * before it. Paths measure from the leg's start, arcs from its end (less one
 * period, so a negative gap never blanks the end of a leg).
 */
export function initialGapDeg(
  layer: GlobeRouteLayer,
  leg: Pick<GlobeLegDatum, "startDeg" | "legDeg">,
  periodDeg: number,
): number {
  return layer === "path"
    ? modulo(-leg.startDeg, periodDeg)
    : modulo(leg.startDeg + leg.legDeg, periodDeg) - periodDeg;
}

export interface GlobeDashAccessors {
  dashLength: (datum: object) => number;
  dashGap: (datum: object) => number;
  dashInitialGap: (datum: object) => number;
  dashAnimateTime: (datum: object) => number;
}

function makeDashAccessors(layer: GlobeRouteLayer, motion: GlobeRouteMotion): GlobeDashAccessors {
  // A leg with no length has nothing to dash.
  const pattern = (leg: GlobeLegDatum) =>
    leg.legDeg > 0 ? legDashPattern(leg.dotted, motion) : null;
  return {
    dashLength: (datum) => {
      const leg = datum as GlobeLegDatum;
      const dash = pattern(leg);
      return dash ? dash.dashDeg / leg.legDeg : 1;
    },
    dashGap: (datum) => {
      const leg = datum as GlobeLegDatum;
      const dash = pattern(leg);
      return dash ? dash.gapDeg / leg.legDeg : 0;
    },
    dashInitialGap: (datum) => {
      const leg = datum as GlobeLegDatum;
      const dash = pattern(leg);
      return dash ? initialGapDeg(layer, leg, dash.dashDeg + dash.gapDeg) / leg.legDeg : 0;
    },
    dashAnimateTime: (datum) => {
      const leg = datum as GlobeLegDatum;
      return motion === "moving" && pattern(leg) ? leg.legDeg * GLOBE_DASH.msPerDeg : 0;
    },
  };
}

const dashAccessorCache = new Map<string, GlobeDashAccessors>();

/** The same accessors for the same layer and motion, so react-globe.gl does not redraw on every render. */
export function globeDashAccessors(
  layer: GlobeRouteLayer,
  motion: GlobeRouteMotion,
): GlobeDashAccessors {
  const key = `${layer}|${motion}`;
  let accessors = dashAccessorCache.get(key);
  if (!accessors) {
    accessors = makeDashAccessors(layer, motion);
    dashAccessorCache.set(key, accessors);
  }
  return accessors;
}

/**
 * Keeps a datum's object identity while its drawn fields are unchanged, so a
 * refresh does not make react-globe.gl rebuild every line and marker.
 */
export class GlobeDatumCache<T extends object> {
  private items = new Map<string, { signature: string; datum: T }>();
  private previous: T[] = [];

  update(next: T[], key: (datum: T) => string): T[] {
    const items = new Map<string, { signature: string; datum: T }>();
    const result = next.map((datum) => {
      const id = key(datum);
      const signature = JSON.stringify(datum);
      const old = this.items.get(id);
      const value = old?.signature === signature ? old.datum : datum;
      items.set(id, { signature, datum: value });
      return value;
    });
    this.items = items;
    if (
      result.length === this.previous.length &&
      result.every((datum, i) => datum === this.previous[i])
    )
      return this.previous;
    this.previous = result;
    return result;
  }
}

/** Zoom never moves opposite the requested direction above the old upper limit. */
export function globeZoomAltitude(altitude: number, factor: number): number {
  return factor > 1
    ? Math.max(altitude, Math.min(6, altitude * factor))
    : Math.max(0.4, altitude * factor);
}

/** Resize the frame without ever moving a manually zoomed camera inside Earth. */
export function resizeCameraHeight(
  height: number,
  oldAspect: number,
  newAspect: number,
  radius: number,
  minimum: number,
): number {
  return Math.max(minimum, ((height + radius) * newAspect) / oldAspect - radius);
}
