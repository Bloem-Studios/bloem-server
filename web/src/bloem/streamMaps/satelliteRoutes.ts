import type * as CesiumModule from "cesium";

import {
  CALM_SPEED,
  COMET_TRAIL_S,
  cometPeriodS,
  lapFraction,
  legAt,
  routePhase,
  trailFraction,
  trailFractions,
  trailWindow,
  TRAIL_SAMPLES,
} from "./cometPath";
import type { RoutePoint, StreamRoute } from "./scene";

// Bloem: the satellite view's route entities, as Proxy Manager's web Satellite
// draws them (web/src/lib/cesiumComet.ts) in its Calm mood: a glowing line per
// leg, and for a moving route one comet flying the whole journey. Cesium is
// passed in rather than imported, so the view keeps its single lazily loaded
// Cesium import. The comet's head and trail are callback properties evaluated
// from the clock every frame: nothing is generated ahead of time, so a route
// costs the same however long it lives.

type Cesium = typeof CesiumModule;

/** Segments each drawn leg is sampled into. */
export const ARC_STEPS = 40;
/** Height of an Earth-hugging route: low and constant, so it never z-fights the imagery. */
export const FLAT_ROUTE_HEIGHT_M = 8_000;
/** Proxy Manager's Calm mood. */
const CALM = { width: 2, opacity: 0.78, glow: 0.1, headPixels: 2.5 } as const;

/**
 * Route samples between two points, ARC_STEPS + 1 of them as [lon, lat,
 * height] triples: arched with a parabolic altitude scaled to distance, or
 * hugging the surface. Equally spaced along the geodesic, so a comet moving
 * through them at an even rate keeps one ground speed.
 */
export function arcPositions(
  Cesium: Cesium,
  from: RoutePoint,
  to: RoutePoint,
  arched: boolean,
): number[] {
  const out: number[] = [];
  const geodesic = new Cesium.EllipsoidGeodesic(
    Cesium.Cartographic.fromDegrees(from.lon, from.lat),
    Cesium.Cartographic.fromDegrees(to.lon, to.lat),
  );
  const maxAlt = arched
    ? Math.min(2_000_000, Math.max(120_000, geodesic.surfaceDistance * 0.18))
    : FLAT_ROUTE_HEIGHT_M;
  for (let i = 0; i <= ARC_STEPS; i++) {
    const t = i / ARC_STEPS;
    const point = geodesic.interpolateUsingFraction(t, new Cesium.Cartographic());
    const alt = arched ? maxAlt * 4 * t * (1 - t) : FLAT_ROUTE_HEIGHT_M;
    out.push(Cesium.Math.toDegrees(point.longitude), Cesium.Math.toDegrees(point.latitude), alt);
  }
  return out;
}

export interface RouteEntitiesOptions {
  route: StreamRoute;
  arched: boolean;
  /** Fly a comet: the window is live and reduced motion is not asked for. */
  comet: boolean;
  /** The viewer's animation epoch, fixed when the viewer was created. */
  epoch: CesiumModule.JulianDate;
}

/**
 * Entity options for one route: one static line per leg (dashed when the leg
 * touches an approximate viewer), then, when `comet` is set, the comet.
 */
export function routeEntityOptions(
  Cesium: Cesium,
  { route, arched, comet, epoch }: RouteEntitiesOptions,
): CesiumModule.Entity.ConstructorOptions[] {
  const legs = route.legs;
  if (legs.length === 0) return [];
  const color = Cesium.Color.fromCssColorString(route.colour);
  const glow = (c: CesiumModule.Color, power: number) =>
    new Cesium.PolylineGlowMaterialProperty({ color: c, glowPower: power, taperPower: 0.6 });
  const lineColor = color.withAlpha(CALM.opacity);
  const width = route.live ? CALM.width : CALM.width * 0.75;
  const legPoints = legs.map((leg) =>
    Cesium.Cartesian3.fromDegreesArrayHeights(arcPositions(Cesium, leg.from, leg.to, arched)),
  );
  const out: CesiumModule.Entity.ConstructorOptions[] = legs.map((leg, i) => ({
    polyline: {
      positions: legPoints[i],
      width,
      material: leg.dotted
        ? new Cesium.PolylineDashMaterialProperty({ color: lineColor, dashLength: 10 })
        : glow(lineColor, CALM.glow),
      arcType: Cesium.ArcType.NONE,
    },
  }));
  if (!comet) return out;

  const periodS = cometPeriodS(route.km, CALM_SPEED);
  const phase = routePhase(route.id);
  const trailFrac = trailFraction(COMET_TRAIL_S, periodS);
  const headAt = (time: CesiumModule.JulianDate | undefined) =>
    lapFraction(Cesium.JulianDate.secondsDifference(time ?? epoch, epoch), periodS, phase);
  // Journey fraction -> point on the drawn path. Leg samples are evenly
  // spaced, so interpolating between neighbours keeps the speed constant.
  const pointAt = (f: number, result: CesiumModule.Cartesian3) => {
    const { leg, u } = legAt(legs, f);
    const points = legPoints[leg] ?? [];
    const k = u * ARC_STEPS;
    const i = Math.min(ARC_STEPS - 1, Math.floor(k));
    const a = points[i];
    const b = points[i + 1];
    if (!a || !b) return result;
    return Cesium.Cartesian3.lerp(a, b, k - i, result);
  };
  const fractions: number[] = [];
  const trailPoints: CesiumModule.Cartesian3[] = [];
  out.push({
    position: new Cesium.CallbackPositionProperty(
      (time, result) => pointAt(headAt(time), result ?? new Cesium.Cartesian3()),
      false,
    ),
    point: {
      pixelSize: CALM.headPixels,
      color: Cesium.Color.WHITE.withAlpha(0.95),
      outlineColor: color.withAlpha(0.8),
      outlineWidth: 2,
    },
    polyline: {
      // The slice of the current lap behind the head: it starts empty at the
      // lap start and never reaches back into the previous lap.
      positions: new Cesium.CallbackProperty((time, result?: CesiumModule.Cartesian3[]) => {
        const [from, to] = trailWindow(headAt(time), trailFrac);
        trailFractions(legs, from, to, TRAIL_SAMPLES, fractions);
        // Cesium's polyline updater passes the same array every frame; refill
        // it with this comet's own points instead of allocating new ones.
        const reuse = Array.isArray(result);
        const positions = reuse ? result : [];
        positions.length = fractions.length;
        for (let i = 0; i < fractions.length; i++) {
          const target = reuse
            ? (trailPoints[i] ??= new Cesium.Cartesian3())
            : new Cesium.Cartesian3();
          positions[i] = pointAt(fractions[i] ?? 0, target);
        }
        return positions;
      }, false),
      width,
      material: glow(color, 0.3),
      arcType: Cesium.ArcType.NONE,
    },
  });
  return out;
}
