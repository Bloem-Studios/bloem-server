import { memo, useEffect, useRef } from "react";
import L from "leaflet";
import "leaflet/dist/leaflet.css";
import { useReducedMotion } from "framer-motion";
import { Home, Maximize, Minimize, ZoomIn, ZoomOut } from "lucide-react";

import { useBloemT } from "@/bloem/i18n";
import { MapButton, MapButtons, MapCredit } from "./mapChrome";
import { fullscreenAvailable, markerText, routeText, textTooltip, useFullscreen } from "./mapText";
import { antimeridianCopies, unwrapLongitudes } from "./scene";
import type { StreamMapViewProps } from "./StreamMapWidget";
import "./streamMaps.css";

// Bloem: Proxy Manager's web Map (web/src/pages/TrafficMap.tsx) drawing the
// stream scene: Esri's dark grey basemap (the web is dark only), each stream a
// line in its transport's colour, dashing for live streams, and dots for the
// viewers and the places the streams pass. Leaflet is driven directly; its own
// chunk (StreamMapWidget.tsx loads it lazily).

const TILE_URL =
  "https://server.arcgisonline.com/ArcGIS/rest/services/Canvas/World_Dark_Gray_Base/MapServer/tile/{z}/{y}/{x}";
const ESRI_URL = "https://www.esri.com/";
const WORLD: L.LatLngBoundsExpression = [
  [-60, -180],
  [80, 180],
];
/** Proxy Manager's Calm mood: line width and opacity. */
const CALM = { width: 2, opacity: 0.78 } as const;

function lineClass(dotted: boolean, moving: boolean): string {
  return [moving && "bloem-stream-line-moving", dotted && "bloem-stream-line-dotted"]
    .filter(Boolean)
    .join(" ");
}

function StreamFlatMap({ scene, moving }: StreamMapViewProps) {
  const t = useBloemT();
  const reducedMotion = useReducedMotion() ?? false;
  const containerRef = useRef<HTMLDivElement>(null);
  const mapElementRef = useRef<HTMLDivElement>(null);
  const mapRef = useRef<L.Map | null>(null);
  const layerRef = useRef<L.LayerGroup | null>(null);
  const [fullscreen, toggleFullscreen] = useFullscreen(containerRef);

  useEffect(() => {
    const element = mapElementRef.current;
    if (!element) return;
    const map = L.map(element, {
      center: [20, 0],
      zoom: 2,
      minZoom: 1,
      zoomSnap: 0,
      zoomDelta: 0.5,
      wheelPxPerZoomLevel: 30,
      worldCopyJump: true,
      zoomControl: false,
      attributionControl: false,
    });
    L.tileLayer(TILE_URL, { maxNativeZoom: 16, maxZoom: 18 }).addTo(map);
    mapRef.current = map;
    layerRef.current = L.layerGroup().addTo(map);
    // A dashboard card changes size (resize, full screen) without a window resize.
    const observer = new ResizeObserver(() => map.invalidateSize());
    observer.observe(element);
    return () => {
      observer.disconnect();
      map.remove();
      mapRef.current = null;
      layerRef.current = null;
    };
  }, []);

  useEffect(() => {
    const group = layerRef.current;
    if (!group) return;
    group.clearLayers();
    const animate = moving && !reducedMotion;
    for (const route of scene.routes) {
      const tooltip = routeText(t, route);
      // Unwrapped over the whole route, so a Pacific crossing stays one line
      // across the Pacific, with a copy at the other edge of the map.
      const points = unwrapLongitudes([route.start, ...route.legs.map((leg) => leg.to)]);
      route.legs.forEach((leg, index) => {
        const from = points[index];
        const to = points[index + 1];
        if (!from || !to) return;
        for (const copy of antimeridianCopies([from, to])) {
          L.polyline(
            copy.map((point) => [point.lat, point.lon] as L.LatLngTuple),
            {
              color: route.colour,
              weight: route.live ? CALM.width : CALM.width * 0.75,
              opacity: CALM.opacity,
              className: lineClass(leg.dotted, animate),
            },
          )
            .bindTooltip(textTooltip(tooltip), { sticky: true, className: "bloem-stream-tooltip" })
            .addTo(group);
        }
      });
    }
    for (const marker of scene.markers) {
      const start = marker.role === "start";
      L.circleMarker([marker.lat, marker.lon], {
        radius: start ? 3.5 : marker.role === "end" ? 6 : 5,
        color: marker.colour,
        weight: 1,
        fillColor: marker.colour,
        fillOpacity: start ? 0.45 : 0.9,
      })
        .bindTooltip(textTooltip(markerText(t, marker)), {
          direction: "top",
          offset: [0, -4],
          className: "bloem-stream-tooltip",
        })
        .addTo(group);
    }
  }, [scene, moving, reducedMotion, t]);

  return (
    <div ref={containerRef} className="bloem-stream-map bg-background absolute inset-0 isolate">
      <div ref={mapElementRef} className="h-full w-full" />
      <MapButtons>
        <MapButton
          label={t("bloem.admin.streamMaps.controls.zoomIn")}
          onClick={() => mapRef.current?.zoomIn(0.5)}
        >
          <ZoomIn className="size-4" aria-hidden="true" />
        </MapButton>
        <MapButton
          label={t("bloem.admin.streamMaps.controls.zoomOut")}
          onClick={() => mapRef.current?.zoomOut(0.5)}
        >
          <ZoomOut className="size-4" aria-hidden="true" />
        </MapButton>
        <MapButton
          label={t("bloem.admin.streamMaps.controls.reset")}
          onClick={() => mapRef.current?.fitBounds(WORLD, { padding: [8, 8], animate: false })}
        >
          <Home className="size-4" aria-hidden="true" />
        </MapButton>
        {fullscreenAvailable() && (
          <MapButton
            label={t("bloem.admin.streamMaps.controls.fullscreen")}
            pressed={fullscreen}
            onClick={toggleFullscreen}
          >
            {fullscreen ? (
              <Minimize className="size-4" aria-hidden="true" />
            ) : (
              <Maximize className="size-4" aria-hidden="true" />
            )}
          </MapButton>
        )}
      </MapButtons>
      <MapCredit href={ESRI_URL}>{t("bloem.admin.streamMaps.credit.esri")}</MapCredit>
    </div>
  );
}

export default memo(StreamFlatMap);
