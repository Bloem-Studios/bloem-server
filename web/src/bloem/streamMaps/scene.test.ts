import { describe, expect, it } from "vitest";

import {
  antimeridianCopies,
  buildStreamScene,
  greatCircleKm,
  transportClass,
  TRANSPORT_COLOURS,
  unwrapLongitudes,
  type StreamHop,
  type StreamTrail,
} from "./scene";

const hop = (
  kind: string,
  label: string,
  lat: number,
  lon: number,
  approximate = false,
): StreamHop => ({
  kind,
  label,
  lat,
  lon,
  approximate,
});

const AMSTERDAM = hop("viewer", "Amsterdam", 52.37, 4.9);
const CF_AMS = hop("cloudflare", "AMS", 52.31, 4.76);
const EDGE = hop("edge", "ns11", 50.11, 8.68);
const SERVER = hop("server", "Bloem", 40.71, -74.0);

/** The item at `index`, failing the test when there is none. */
function at<T>(items: readonly T[], index: number): T {
  const item = items[index];
  if (item === undefined) throw new Error(`no item at ${index}`);
  return item;
}

const trail = (id: string, transport: string, hops: StreamHop[], live = true): StreamTrail => ({
  session_id: id,
  live,
  transport,
  hops,
});

describe("buildStreamScene", () => {
  it("draws one route per trail, a leg per pair of hops, in the trail's colour", () => {
    const scene = buildStreamScene([trail("a", "udp", [AMSTERDAM, CF_AMS, EDGE, SERVER])]);

    expect(scene.routes).toHaveLength(1);
    const route = at(scene.routes, 0);
    expect(route.id).toBe("a");
    expect(route.colour).toBe(TRANSPORT_COLOURS.udp);
    expect(route.legs.map((leg) => [leg.from, leg.to])).toEqual([
      [
        { lat: 52.37, lon: 4.9 },
        { lat: 52.31, lon: 4.76 },
      ],
      [
        { lat: 52.31, lon: 4.76 },
        { lat: 50.11, lon: 8.68 },
      ],
      [
        { lat: 50.11, lon: 8.68 },
        { lat: 40.71, lon: -74.0 },
      ],
    ]);
  });

  it("splits a route's journey by distance, from 0 to 1 without gaps", () => {
    const route = at(buildStreamScene([trail("a", "tcp", [AMSTERDAM, EDGE, SERVER])]).routes, 0);
    const first = at(route.legs, 0);
    const second = at(route.legs, 1);

    expect(first.start).toBe(0);
    expect(first.end).toBeCloseTo(first.km / route.km, 10);
    expect(second.start).toBe(first.end);
    expect(second.end).toBe(1);
    expect(route.km).toBeCloseTo(first.km + second.km, 10);
  });

  it("dots only the legs that touch an approximate viewer", () => {
    const viewer = hop("viewer", "Netherlands", 52.13, 5.29, true);
    const route = at(buildStreamScene([trail("a", "udp", [viewer, EDGE, SERVER])]).routes, 0);

    expect(route.legs.map((leg) => leg.dotted)).toEqual([true, false]);
  });

  it("stacks lines unknown, Cloudflare, TCP, UDP from the bottom, so UDP is on top", () => {
    const scene = buildStreamScene([
      trail("u", "udp", [AMSTERDAM, SERVER]),
      trail("t", "tcp", [AMSTERDAM, SERVER]),
      trail("c", "cloudflare", [AMSTERDAM, SERVER]),
      trail("x", "quic-draft", [AMSTERDAM, SERVER]),
    ]);

    expect(scene.routes.map((route) => route.id)).toEqual(["x", "c", "t", "u"]);
    expect(at(scene.routes, 0).colour).toBe(TRANSPORT_COLOURS.unknown);
  });

  it("marks each stream's start in its colour and opens that stream", () => {
    const scene = buildStreamScene([trail("a", "tcp", [AMSTERDAM, SERVER])]);
    const start = scene.markers.find((marker) => marker.role === "start");

    expect(start).toMatchObject({
      label: "Amsterdam",
      kind: "viewer",
      colour: TRANSPORT_COLOURS.tcp,
      streams: 1,
      routeId: "a",
    });
  });

  it("draws a place several streams share once, counting them, and opens none", () => {
    const scene = buildStreamScene([
      trail("a", "udp", [AMSTERDAM, EDGE, SERVER]),
      trail("b", "tcp", [hop("viewer", "Berlin", 52.52, 13.4), EDGE, SERVER]),
    ]);
    const shared = scene.markers.filter((marker) => marker.role !== "start");

    expect(
      shared.map((marker) => [marker.role, marker.label, marker.streams, marker.routeId]),
    ).toEqual([
      ["via", "ns11", 2, null],
      ["end", "Bloem", 2, null],
    ]);
    expect(shared.every((marker) => marker.colour === TRANSPORT_COLOURS.unknown)).toBe(true);
  });

  it("lets a place only one stream uses open that stream", () => {
    const scene = buildStreamScene([trail("a", "udp", [AMSTERDAM, EDGE, SERVER])]);

    expect(scene.markers.find((marker) => marker.role === "end")?.routeId).toBe("a");
  });

  it("keeps a one-hop trail as its start and drops a trail with no hops", () => {
    const scene = buildStreamScene([trail("a", "udp", [AMSTERDAM]), trail("b", "udp", [])]);

    expect(scene.routes.map((route) => [route.id, route.legs.length])).toEqual([["a", 0]]);
    expect(scene.markers.map((marker) => marker.routeId)).toEqual(["a"]);
  });
});

describe("transportClass", () => {
  it("knows the four classes and draws anything else as unknown", () => {
    expect(["udp", "tcp", "cloudflare", "unknown", "", "h4"].map(transportClass)).toEqual([
      "udp",
      "tcp",
      "cloudflare",
      "unknown",
      "unknown",
      "unknown",
    ]);
  });
});

describe("geometry", () => {
  it("measures a great circle in km", () => {
    expect(greatCircleKm({ lat: 0, lon: 0 }, { lat: 0, lon: 1 })).toBeCloseTo(111.19, 1);
  });

  it("continues longitudes the short way round and copies a line across the edge", () => {
    const line = unwrapLongitudes([
      { lat: 35, lon: 139 },
      { lat: 37, lon: -122 },
    ]);

    expect(line.map((point) => point.lon)).toEqual([139, 238]);
    expect(antimeridianCopies(line).map((copy) => copy.map((point) => point.lon))).toEqual([
      [139, 238],
      [-221, -122],
    ]);
  });
});
