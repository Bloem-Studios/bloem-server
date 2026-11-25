import { memo, useCallback, useEffect, useRef, useState } from "react";
import * as Cesium from "cesium";
import "cesium/Build/Cesium/Widgets/CesiumWidget/CesiumWidget.css";
import { useReducedMotion } from "framer-motion";
import { Home, Maximize, Minimize, Pause, Play, Spline, ZoomIn, ZoomOut } from "lucide-react";

import { useBloemT } from "@/bloem/i18n";
import { MapButton, MapButtons, MapCredit } from "./mapChrome";
import { fullscreenAvailable, markerText, useFullscreen } from "./mapText";
import { routeEntityOptions } from "./satelliteRoutes";
import type { StreamMapViewProps } from "./StreamMapWidget";
import "./streamMaps.css";

// Bloem: Proxy Manager's web Satellite (web/src/pages/TrafficCesium.tsx) in its
// Calm mood, drawing the stream scene over Esri's World Imagery with sunlit
// terrain and night shading: a glowing line per leg, a comet per live stream,
// and a dot per viewer and per place the streams pass.
//
// It runs on Cesium's bare CesiumWidget, not its Viewer: the Viewer's controls
// bind through Knockout, which compiles its bindings with `new Function`, and
// the web's Content-Security-Policy allows no eval (internal/server/frontend.go).
// Its own chunk, since Cesium is several MB (StreamMapWidget.tsx loads it lazily).

const IMAGERY_URL =
  "https://services.arcgisonline.com/ArcGis/rest/services/World_Imagery/MapServer";
const ESRI_URL = "https://www.esri.com/";
const EARTH_RADIUS_M = 6_371_000;
/** Proxy Manager's Calm turn. */
const CALM_ROTATION = 0.16;
const PLACE_PREFIX = "place|";

function homeDestination(container: HTMLElement): Cesium.Cartesian3 {
  const width = Math.max(container.clientWidth, 1);
  const height = Math.max(container.clientHeight, 1);
  const aspect = Math.max(width / height, height / width);
  return Cesium.Cartesian3.fromDegrees(-40, 30, 14_871_000 * aspect - EARTH_RADIUS_M);
}

interface Hover {
  text: string;
  x: number;
  y: number;
}

function StreamSatellite({ scene, moving }: StreamMapViewProps) {
  const t = useBloemT();
  const reducedMotion = useReducedMotion() ?? false;
  const containerRef = useRef<HTMLDivElement>(null);
  const globeElementRef = useRef<HTMLDivElement>(null);
  const widgetRef = useRef<Cesium.CesiumWidget | null>(null);
  const epochRef = useRef<Cesium.JulianDate | null>(null);
  /** Each place marker's entity id and its tooltip text. */
  const placesRef = useRef(new Map<string, string>());
  const reducedRef = useRef(reducedMotion);
  const rotatingRef = useRef(!reducedMotion);
  const [rotating, setRotatingState] = useState(!reducedMotion);
  const [arched, setArched] = useState(false);
  const [failed, setFailed] = useState(false);
  const [hover, setHover] = useState<Hover | null>(null);
  const [fullscreen, toggleFullscreen] = useFullscreen(containerRef);

  const setRotating = useCallback((value: boolean) => {
    rotatingRef.current = value;
    setRotatingState(value);
  }, []);
  useEffect(() => {
    reducedRef.current = reducedMotion;
  }, [reducedMotion]);

  useEffect(() => {
    const element = globeElementRef.current;
    if (!element) return;
    let cancelled = false;
    let hoverFrame = 0;
    const cleanups: (() => void)[] = [];
    const fail = () => setFailed(true);
    let widget: Cesium.CesiumWidget;
    try {
      widget = new Cesium.CesiumWidget(element, {
        baseLayer: false,
        showRenderLoopErrors: false,
        shouldAnimate: true,
      });
    } catch {
      // No WebGL: say so once this effect is done, not while React commits.
      queueMicrotask(fail);
      return;
    }
    widgetRef.current = widget;
    epochRef.current = Cesium.JulianDate.clone(widget.clock.currentTime);
    const removeRenderError = widget.scene.renderError.addEventListener(fail);
    cleanups.push(removeRenderError);
    widget.canvas.addEventListener("webglcontextlost", fail);
    cleanups.push(() => widget.canvas.removeEventListener("webglcontextlost", fail));

    Cesium.ArcGisMapServerImageryProvider.fromUrl(IMAGERY_URL)
      .then((provider) => {
        if (cancelled || widget.isDestroyed()) return;
        widget.imageryLayers.add(new Cesium.ImageryLayer(provider));
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });

    // Sunlit terrain, night shading and the atmosphere, as Proxy Manager's view.
    const view = widget.scene;
    view.screenSpaceCameraController.zoomFactor = 10;
    view.globe.enableLighting = true;
    view.globe.atmosphereLightIntensity = 12;
    if (view.skyAtmosphere) view.skyAtmosphere.show = true;
    view.fog.enabled = true;
    view.highDynamicRange = true;
    view.backgroundColor = Cesium.Color.fromCssColorString("#101416");
    view.globe.showGroundAtmosphere = true;
    view.postProcessStages.fxaa.enabled = true;

    const handler = widget.screenSpaceEventHandler;
    for (const type of [
      Cesium.ScreenSpaceEventType.LEFT_DOWN,
      Cesium.ScreenSpaceEventType.RIGHT_DOWN,
      Cesium.ScreenSpaceEventType.MIDDLE_DOWN,
      Cesium.ScreenSpaceEventType.WHEEL,
      Cesium.ScreenSpaceEventType.PINCH_START,
    ]) {
      handler.setInputAction(() => setRotating(false), type);
    }
    handler.removeInputAction(Cesium.ScreenSpaceEventType.LEFT_DOUBLE_CLICK);
    // A place's name follows the pointer; a pick renders the scene, so moves
    // are coalesced to at most one pick per frame.
    let hoverAt: Cesium.Cartesian2 | undefined;
    handler.setInputAction((move: { endPosition: Cesium.Cartesian2 }) => {
      hoverAt = Cesium.Cartesian2.clone(move.endPosition, hoverAt);
      if (hoverFrame) return;
      hoverFrame = requestAnimationFrame(() => {
        hoverFrame = 0;
        if (widget.isDestroyed() || !hoverAt) return;
        const picked: unknown = widget.scene.pick(hoverAt);
        const entity = (picked as { id?: unknown } | undefined)?.id;
        const id = entity instanceof Cesium.Entity ? entity.id : "";
        const text = id.startsWith(PLACE_PREFIX) ? placesRef.current.get(id) : undefined;
        widget.canvas.style.cursor = text ? "pointer" : "";
        setHover(text ? { text, x: hoverAt.x, y: hoverAt.y } : null);
      });
    }, Cesium.ScreenSpaceEventType.MOUSE_MOVE);

    let lastRotation = performance.now();
    const removePostRender = view.postRender.addEventListener(() => {
      const now = performance.now();
      const elapsed = Math.min(0.1, Math.max(0, (now - lastRotation) / 1000));
      lastRotation = now;
      if (rotatingRef.current) view.camera.rotateRight(-0.06 * elapsed * CALM_ROTATION);
    });
    cleanups.push(removePostRender);

    view.camera.setView({ destination: Cesium.Cartesian3.fromDegrees(-30, 25, 45_000_000) });
    view.camera.flyTo({
      destination: homeDestination(element),
      duration: reducedRef.current ? 0 : 3.5,
      easingFunction: Cesium.EasingFunction.QUADRATIC_OUT,
    });
    const observer = new ResizeObserver(() => {
      if (!widget.isDestroyed()) widget.resize();
    });
    observer.observe(element);

    return () => {
      cancelled = true;
      if (hoverFrame) cancelAnimationFrame(hoverFrame);
      observer.disconnect();
      cleanups.forEach((cleanup) => cleanup());
      if (!widget.isDestroyed()) widget.destroy();
      widgetRef.current = null;
    };
  }, [setRotating]);

  useEffect(() => {
    const widget = widgetRef.current;
    const epoch = epochRef.current;
    if (!widget || widget.isDestroyed() || !epoch) return;
    const entities = widget.entities;
    entities.suspendEvents();
    entities.removeAll();
    const comet = moving && !reducedMotion;
    for (const route of scene.routes) {
      for (const options of routeEntityOptions(Cesium, { route, arched, comet, epoch })) {
        entities.add(options);
      }
    }
    const places = new Map<string, string>();
    for (const marker of scene.markers) {
      const id = `${PLACE_PREFIX}${marker.id}`;
      places.set(id, markerText(t, marker));
      const color = Cesium.Color.fromCssColorString(marker.colour);
      entities.add({
        id,
        position: Cesium.Cartesian3.fromDegrees(marker.lon, marker.lat),
        point: {
          pixelSize:
            marker.role === "start" ? 5 : Math.min(13, 5 + Math.sqrt(marker.streams) * 1.6),
          color,
          outlineColor: color.withAlpha(0.35),
          outlineWidth: 3,
          scaleByDistance: new Cesium.NearFarScalar(1e6, 1.3, 4e7, 0.65),
        },
      });
    }
    placesRef.current = places;
    entities.resumeEvents();
  }, [scene, moving, reducedMotion, arched, t]);

  const zoom = (factor: number) => {
    const camera = widgetRef.current?.scene.camera;
    if (!camera) return;
    const height = camera.positionCartographic.height;
    if (factor < 1) camera.zoomIn(height * (1 - factor));
    else camera.zoomOut(height * (factor - 1));
  };
  const resetView = () => {
    const widget = widgetRef.current;
    const element = globeElementRef.current;
    if (!widget || !element) return;
    widget.scene.camera.flyTo({
      destination: homeDestination(element),
      duration: reducedMotion ? 0 : 1,
    });
  };

  return (
    <div ref={containerRef} className="bloem-stream-map absolute inset-0 isolate bg-[#101416]">
      <div ref={globeElementRef} className="h-full w-full" />
      {failed && (
        <p className="text-muted-foreground absolute inset-0 grid place-items-center p-4 text-center text-sm">
          {t("bloem.admin.streamMaps.state.renderFailed")}
        </p>
      )}
      {hover && (
        <div
          className="bg-popover text-popover-foreground pointer-events-none absolute z-[1000] -translate-x-1/2 -translate-y-full rounded-md border px-2 py-1 text-xs whitespace-nowrap shadow-md"
          style={{ left: hover.x, top: hover.y - 8 }}
        >
          {hover.text}
        </div>
      )}
      <MapButtons>
        <MapButton label={t("bloem.admin.streamMaps.controls.zoomIn")} onClick={() => zoom(0.6)}>
          <ZoomIn className="size-4" aria-hidden="true" />
        </MapButton>
        <MapButton label={t("bloem.admin.streamMaps.controls.zoomOut")} onClick={() => zoom(1.6)}>
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
      <MapCredit href={ESRI_URL}>{t("bloem.admin.streamMaps.credit.esriImagery")}</MapCredit>
    </div>
  );
}

export default memo(StreamSatellite);
