import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Globe, { type GlobeMethods } from "react-globe.gl";
import { useReducedMotion } from "framer-motion";
import { Home, Maximize, Minimize, Pause, Play, Spline, ZoomIn, ZoomOut } from "lucide-react";

import { useBloemT } from "@/bloem/i18n";
import earthImageUrl from "./assets/earth-blue-marble.jpg";
import {
  GLOBE_DASH,
  GlobeDatumCache,
  globeDashAccessors,
  globeLegDatums,
  globeZoomAltitude,
  resizeCameraHeight,
  type GlobeLegDatum,
  type GlobeRouteMotion,
} from "./globeRoutes";
import { MapButton, MapButtons, MapCredit } from "./mapChrome";
import { fullscreenAvailable, markerText, routeText, textTooltip, useFullscreen } from "./mapText";
import type { StreamMapViewProps } from "./StreamMapWidget";

// Bloem: Proxy Manager's web Globe (web/src/pages/TrafficGlobe.tsx) in its
// Calm flow mode, drawing the stream scene: Blue Marble under an atmosphere,
// Earth-hugging lines (or arcs) whose dashes run for live streams, and flat
// dots for each viewer and each place the streams pass. Its own chunk, since
// three.js is a couple of MB (StreamMapWidget.tsx loads it lazily).

const EMPTY: never[] = [];
const NASA_URL = "https://visibleearth.nasa.gov/collection/1484/blue-marble";
const START_VIEW = { lat: 25, lng: -30 };
/** Proxy Manager's Calm mood: line width and the slow turn. */
const CALM = { width: 2, rotation: 0.16 } as const;

interface PointDatum {
  id: string;
  lat: number;
  lng: number;
  color: string;
  radius: number;
  label: string;
}

const legKey = (datum: GlobeLegDatum) => datum.id;
const pointKey = (datum: PointDatum) => datum.id;
// react-globe.gl appends an HTMLElement label as a node and parses a string as
// HTML, so every label is a text node (mapChrome's textTooltip).
const pointLabel = (datum: object) => textTooltip((datum as PointDatum).label) as unknown as string;
const pathStroke = (datum: object) =>
  (datum as GlobeLegDatum).live ? CALM.width * 0.75 : CALM.width * 0.45;
const arcStroke = (datum: object) =>
  (datum as GlobeLegDatum).live ? CALM.width * 0.16 : CALM.width * 0.1;

function useStableDatums<T extends object>(items: T[], key: (datum: T) => string): T[] {
  const [cache] = useState(() => new GlobeDatumCache<T>());
  return useMemo(() => cache.update(items, key), [cache, items, key]);
}

function StreamGlobe({ scene, moving }: StreamMapViewProps) {
  const t = useBloemT();
  const reducedMotion = useReducedMotion() ?? false;
  const globeRef = useRef<GlobeMethods | undefined>(undefined);
  const containerRef = useRef<HTMLDivElement>(null);
  const [fullscreen, toggleFullscreen] = useFullscreen(containerRef);
  const [rotationChoice, setRotating] = useState<boolean | null>(null);
  const rotating = rotationChoice ?? !reducedMotion;
  const [arched, setArched] = useState(false);

  // react-globe.gl needs a real width and height on every render: undefined
  // works on the first mount and throws on a later one.
  const [size, setSize] = useState({ width: 600, height: 400 });
  const previousAspect = useRef(1);
  const fitAltitude = Math.max(1.65, (2.65 * size.height) / Math.max(size.width, 1) - 1);
  useEffect(() => {
    const aspect = Math.max(1, size.height / Math.max(size.width, 1));
    const globe = globeRef.current;
    if (globe && aspect !== previousAspect.current) {
      const pov = globe.pointOfView();
      globe.pointOfView(
        { altitude: resizeCameraHeight(pov.altitude, previousAspect.current, aspect, 1, 0.4) },
        0,
      );
    }
    previousAspect.current = aspect;
  }, [size]);
  useEffect(() => {
    const element = containerRef.current;
    if (!element) return;
    const update = () => setSize({ width: element.clientWidth, height: element.clientHeight });
    update();
    const observer = new ResizeObserver(update);
    observer.observe(element);
    return () => observer.disconnect();
  }, [fullscreen]);

  const motion: GlobeRouteMotion = moving && !reducedMotion ? "moving" : "still";
  const pathData = useStableDatums(
    useMemo(() => globeLegDatums(scene.routes, "path"), [scene.routes]),
    legKey,
  );
  const arcData = useStableDatums(
    useMemo(() => globeLegDatums(scene.routes, "arc"), [scene.routes]),
    legKey,
  );
  const pathDash = globeDashAccessors("path", motion);
  const arcDash = globeDashAccessors("arc", motion);
  // A refresh hands the Paths layer new data, which restarts its dashes from
  // their initial gap; this clock adds how far they had travelled, so the
  // dashes keep running through a refresh (Proxy Manager's motion clock).
  // The clock is read only when react-globe.gl asks for a gap, never during
  // render; a change of motion banks the distance run so far.
  const clockRef = useRef({ elapsed: 0, at: 0, moving: false });
  useEffect(() => {
    const clock = clockRef.current;
    const now = Date.now();
    if (clock.moving) clock.elapsed += (now - clock.at) / GLOBE_DASH.msPerDeg;
    clock.at = now;
    clock.moving = motion === "moving";
  }, [motion]);
  const pathInitialGap = useCallback(
    (datum: object) => {
      const leg = datum as GlobeLegDatum;
      const clock = clockRef.current;
      const travelled =
        clock.elapsed + (clock.moving ? (Date.now() - clock.at) / GLOBE_DASH.msPerDeg : 0);
      return pathDash.dashInitialGap(datum) + (leg.legDeg > 0 ? travelled / leg.legDeg : 0);
    },
    [pathDash],
  );

  const routeLabels = useMemo(
    () => new Map(scene.routes.map((route) => [route.id, routeText(t, route)])),
    [scene.routes, t],
  );
  const legLabel = useCallback(
    (datum: object) =>
      textTooltip(routeLabels.get((datum as GlobeLegDatum).routeId) ?? "") as unknown as string,
    [routeLabels],
  );
  const points = useStableDatums(
    useMemo(
      () =>
        scene.markers.map<PointDatum>((marker) => ({
          id: marker.id,
          lat: marker.lat,
          lng: marker.lon,
          color: marker.colour,
          radius: marker.role === "start" ? 0.35 : marker.role === "end" ? 0.45 : 0.38,
          label: markerText(t, marker),
        })),
      [scene.markers, t],
    ),
    pointKey,
  );

  useEffect(() => {
    globeRef.current?.pointOfView({ ...START_VIEW, altitude: 1.65 }, 0);
  }, []);

  useEffect(() => {
    const controls = globeRef.current?.controls();
    if (!controls) return;
    controls.zoomSpeed = Math.sqrt(globeRef.current?.pointOfView().altitude ?? 1.65);
    const orbit = controls as unknown as { autoRotate: boolean; autoRotateSpeed: number };
    orbit.autoRotate = rotating;
    orbit.autoRotateSpeed = CALM.rotation;
    const stopOnInteraction = () => setRotating(false);
    controls.addEventListener("start", stopOnInteraction);
    return () => controls.removeEventListener("start", stopOnInteraction);
  }, [rotating]);

  const zoom = (factor: number) => {
    const globe = globeRef.current;
    if (!globe) return;
    globe.pointOfView(
      { altitude: globeZoomAltitude(globe.pointOfView().altitude, factor) },
      reducedMotion ? 0 : 300,
    );
  };
  const resetView = () => {
    globeRef.current?.pointOfView(
      { ...START_VIEW, altitude: fitAltitude },
      reducedMotion ? 0 : 500,
    );
  };

  return (
    <div ref={containerRef} className="bg-background absolute inset-0 isolate overflow-hidden">
      <Globe
        ref={globeRef}
        width={size.width}
        height={size.height}
        animateIn={!reducedMotion}
        arcsTransitionDuration={reducedMotion ? 0 : 1000}
        pathTransitionDuration={reducedMotion ? 0 : 1000}
        pointsTransitionDuration={reducedMotion ? 0 : 1000}
        onZoom={({ altitude }) => {
          // globe.gl resets sensitivity on each camera change; keep its
          // altitude scaling, with twice the default wheel response.
          const controls = globeRef.current?.controls();
          if (controls) controls.zoomSpeed = Math.sqrt(altitude);
        }}
        globeImageUrl={earthImageUrl}
        backgroundColor="rgba(0,0,0,0)"
        showAtmosphere
        atmosphereColor="#06bce0"
        atmosphereAltitude={0.12}
        arcsData={arched ? arcData : EMPTY}
        arcColor="color"
        arcDashLength={arcDash.dashLength}
        arcDashGap={arcDash.dashGap}
        arcDashInitialGap={arcDash.dashInitialGap}
        arcDashAnimateTime={arcDash.dashAnimateTime}
        arcStroke={arcStroke}
        arcLabel={legLabel}
        pathsData={arched ? EMPTY : pathData}
        pathPoints="points"
        pathColor="color"
        pathStroke={pathStroke}
        pathDashLength={pathDash.dashLength}
        pathDashGap={pathDash.dashGap}
        pathDashInitialGap={pathInitialGap}
        pathDashAnimateTime={pathDash.dashAnimateTime}
        pathLabel={legLabel}
        pathPointAlt={0.002}
        pointsData={points}
        pointLat="lat"
        pointLng="lng"
        pointColor="color"
        pointAltitude={0.01}
        pointRadius="radius"
        pointLabel={pointLabel}
      />

      <MapButtons>
        <MapButton label={t("bloem.admin.streamMaps.controls.zoomIn")} onClick={() => zoom(0.7)}>
          <ZoomIn className="size-4" aria-hidden="true" />
        </MapButton>
        <MapButton label={t("bloem.admin.streamMaps.controls.zoomOut")} onClick={() => zoom(1.3)}>
          <ZoomOut className="size-4" aria-hidden="true" />
        </MapButton>
        <MapButton label={t("bloem.admin.streamMaps.controls.reset")} onClick={resetView}>
          <Home className="size-4" aria-hidden="true" />
        </MapButton>
        <MapButton
          label={t("bloem.admin.streamMaps.controls.rotate")}
          pressed={rotating}
          onClick={() => setRotating(!rotating)}
        >
          {rotating ? (
            <Pause className="size-4" aria-hidden="true" />
          ) : (
            <Play className="size-4" aria-hidden="true" />
          )}
        </MapButton>
        <MapButton
          label={t("bloem.admin.streamMaps.controls.arched")}
          pressed={arched}
          onClick={() => setArched((value) => !value)}
        >
          <Spline className="size-4" aria-hidden="true" />
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
      <MapCredit href={NASA_URL}>{t("bloem.admin.streamMaps.credit.nasa")}</MapCredit>
    </div>
  );
}

export default memo(StreamGlobe);
