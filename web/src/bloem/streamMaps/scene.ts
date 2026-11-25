// Bloem: the server's stream trails (GET /api/bloem/v2/admin/platform/streams/trails) as
// one scene the three dashboard maps draw, as the Admin apps' Map page does
// (StreamTrailScene.kt). The route geometry is Proxy Manager's web traffic
// maps' (web/src/lib/trafficViz.ts), so the lines move like theirs.

/** A map's window: the streams playing now, or those started in the last 24 hours or 7 days. */
export type StreamWindow = "live" | "24h" | "7d";

export const STREAM_WINDOWS: readonly StreamWindow[] = ["live", "24h", "7d"];

export function streamWindow(value: string | undefined): StreamWindow {
  return STREAM_WINDOWS.find((window) => window === value) ?? "live";
}

/** One place a stream passed: viewer, cloudflare, edge, server or node. */
export interface StreamHop {
  kind: string;
  label: string;
  lat: number;
  lon: number;
  approximate: boolean;
}

export interface StreamTrail {
  session_id: string;
  live: boolean;
  transport: string;
  hops: StreamHop[];
}

export interface StreamTrails {
  trails: StreamTrail[];
  /** More streams than the 2000 listed were in the window. */
  truncated: boolean;
}

export interface StreamInsightsCapabilities {
  available: boolean;
  /** False without a GeoLite2-City database: no viewer locations. */
  locations: boolean;
  retention_days: number;
  edge_header: string;
}

/** How the viewer's leg is connected; a class this build does not know is `unknown`. */
export type TransportClass = "udp" | "tcp" | "cloudflare" | "unknown";

export const TRANSPORT_CLASSES: readonly TransportClass[] = ["udp", "tcp", "cloudflare", "unknown"];

export function transportClass(value: string): TransportClass {
  return TRANSPORT_CLASSES.find((known) => known === value) ?? "unknown";
}

/**
 * One fixed colour per class, bright enough on the globe's Blue Marble, the
 * grey map and satellite imagery alike, in either theme.
 */
export const TRANSPORT_COLOURS: Record<TransportClass, string> = {
  udp: "#22c55e",
  tcp: "#3b82f6",
  cloudflare: "#f97316",
  unknown: "#9ca3af",
};

/** Drawn first to last, so UDP, the move being followed, is on top. */
const LINE_ORDER: readonly TransportClass[] = ["unknown", "cloudflare", "tcp", "udp"];

export interface RoutePoint {
  lat: number;
  lon: number;
}

export interface StreamLeg {
  from: RoutePoint;
  to: RoutePoint;
  /** Draw dotted: one end is a viewer whose location is approximate. */
  dotted: boolean;
  /** Great-circle length. */
  km: number;
  /**
   * The leg's share of the journey: the first starts at 0, the last ends at 1,
   * and each starts where the one before ends. Sized by distance, so a dash
   * animated by these fractions moves at one speed through every hop.
   */
  start: number;
  end: number;
}

export interface StreamRoute {
  /** The stream's session id. */
  id: string;
  transport: TransportClass;
  colour: string;
  live: boolean;
  start: RoutePoint;
  legs: StreamLeg[];
  km: number;
  hops: StreamHop[];
}

export type MarkerRole = "start" | "via" | "end";

export interface StreamMarker {
  id: string;
  role: MarkerRole;
  lat: number;
  lon: number;
  label: string;
  kind: string;
  approximate: boolean;
  colour: string;
  /** How many streams pass this place. */
  streams: number;
  /** The stream this marker opens; null for a place several streams share. */
  routeId: string | null;
}

export interface StreamScene {
  routes: StreamRoute[];
  markers: StreamMarker[];
}

export const EARTH_RADIUS_KM = 6371;

const toRadians = (degrees: number) => (degrees * Math.PI) / 180;

/** Haversine distance in km. */
export function greatCircleKm(a: RoutePoint, b: RoutePoint): number {
  const dLat = toRadians(b.lat - a.lat);
  const dLon = toRadians(b.lon - a.lon);
  const h =
    Math.sin(dLat / 2) ** 2 +
    Math.cos(toRadians(a.lat)) * Math.cos(toRadians(b.lat)) * Math.sin(dLon / 2) ** 2;
  return 2 * EARTH_RADIUS_KM * Math.asin(Math.min(1, Math.sqrt(h)));
}

function legsThrough(hops: readonly StreamHop[]): StreamLeg[] {
  const legs: StreamLeg[] = [];
  let from: StreamHop | undefined;
  for (const to of hops) {
    if (from) {
      legs.push({
        from: { lat: from.lat, lon: from.lon },
        to: { lat: to.lat, lon: to.lon },
        dotted: legs.length === 0 && from.kind === "viewer" && from.approximate,
        km: greatCircleKm(from, to),
        start: 0,
        end: 0,
      });
    }
    from = to;
  }
  const total = legs.reduce((sum, leg) => sum + leg.km, 0);
  let at = 0;
  legs.forEach((leg, i) => {
    leg.start = at;
    // A journey with no length at all falls back to equal shares.
    at = i === legs.length - 1 ? 1 : total > 0 ? at + leg.km / total : (i + 1) / legs.length;
    leg.end = at;
  });
  return legs;
}

const fourDecimals = (value: number) => value.toFixed(4);

/**
 * One route per trail with a leg per pair of hops; a one-hop trail is a route
 * with no legs, drawn as its start, and a trail with no hops draws nothing.
 * Each stream's start is a marker in its colour that opens it; the places
 * after it (via and end) are one marker per role, label and place, in the
 * unknown colour, counting the streams that use them.
 */
export function buildStreamScene(trails: readonly StreamTrail[]): StreamScene {
  const drawn = trails.flatMap((trail) => {
    const [first] = trail.hops;
    if (!first) return [];
    const transport = transportClass(trail.transport);
    const legs = legsThrough(trail.hops);
    const route: StreamRoute = {
      id: trail.session_id,
      transport,
      colour: TRANSPORT_COLOURS[transport],
      live: trail.live,
      start: { lat: first.lat, lon: first.lon },
      legs,
      km: legs.reduce((sum, leg) => sum + leg.km, 0),
      hops: trail.hops,
    };
    const start: StreamMarker = {
      id: `start:${trail.session_id}`,
      role: "start",
      lat: first.lat,
      lon: first.lon,
      label: first.label,
      kind: first.kind,
      approximate: first.approximate,
      colour: route.colour,
      streams: 1,
      routeId: trail.session_id,
    };
    return [{ trail, route, start }];
  });
  drawn.sort(
    (a, b) => LINE_ORDER.indexOf(a.route.transport) - LINE_ORDER.indexOf(b.route.transport),
  );
  const routes = drawn.map(({ route }) => route);
  const starts = drawn.map(({ start }) => start);

  const places = new Map<string, { role: MarkerRole; hop: StreamHop; sessions: Set<string> }>();
  for (const { trail } of drawn) {
    trail.hops.forEach((hop, index) => {
      if (index === 0) return;
      const role: MarkerRole = index === trail.hops.length - 1 ? "end" : "via";
      const key = `${role}:${hop.label}:${fourDecimals(hop.lat)}:${fourDecimals(hop.lon)}`;
      const place = places.get(key) ?? { role, hop, sessions: new Set<string>() };
      place.sessions.add(trail.session_id);
      places.set(key, place);
    });
  }
  const shared = Array.from(places, ([key, place]): StreamMarker => {
    const [only] = place.sessions;
    return {
      id: key,
      role: place.role,
      lat: place.hop.lat,
      lon: place.hop.lon,
      label: place.hop.label,
      kind: place.hop.kind,
      approximate: place.hop.approximate,
      colour: TRANSPORT_COLOURS.unknown,
      streams: place.sessions.size,
      routeId: place.sessions.size === 1 ? (only ?? null) : null,
    };
  });

  return { routes, markers: [...starts, ...shared] };
}

/**
 * Makes each longitude continue from the previous one the shorter way round,
 * so a flat map draws a Pacific crossing across the Pacific. The result may
 * leave [-180, 180]; `antimeridianCopies` supplies what falls off the edge.
 */
export function unwrapLongitudes<T extends RoutePoint>(points: readonly T[]): T[] {
  const out: T[] = [];
  let previous: number | null = null;
  for (const point of points) {
    const lon: number =
      previous == null ? point.lon : point.lon + 360 * Math.round((previous - point.lon) / 360);
    out.push({ ...point, lon });
    previous = lon;
  }
  return out;
}

/**
 * An unwrapped line plus a copy shifted by -360 when it runs past +180 and by
 * +360 when it runs past -180, so both map edges show the crossing.
 */
export function antimeridianCopies<T extends RoutePoint>(points: readonly T[]): T[][] {
  const lines: T[][] = [[...points]];
  if (points.some((point) => point.lon > 180))
    lines.push(points.map((point) => ({ ...point, lon: point.lon - 360 })));
  if (points.some((point) => point.lon < -180))
    lines.push(points.map((point) => ({ ...point, lon: point.lon + 360 })));
  return lines;
}
