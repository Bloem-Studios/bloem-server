import { lazy, Suspense, useMemo, type ComponentType, type MouseEvent } from "react";

import { useBloemT, type BloemMessageKey } from "@/bloem/i18n";
import { WidgetHeader } from "@/components/admin/dashboard/WidgetHeader";
import {
  WidgetViewToggle,
  type WidgetViewOption,
} from "@/components/admin/dashboard/WidgetViewToggle";
import { useWidgetRange } from "@/components/admin/dashboard/widgetChrome";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { useStreamInsightsCapabilities, useStreamTrails } from "./queries";
import {
  buildStreamScene,
  streamWindow,
  TRANSPORT_CLASSES,
  TRANSPORT_COLOURS,
  type StreamScene,
  type StreamWindow,
} from "./scene";

/** What every map view draws: the scene, and whether its routes move. */
export interface StreamMapViewProps {
  scene: StreamScene;
  /** Live streams' dashes move; a past window's routes stand still. */
  moving: boolean;
}

// Each view is its own chunk, so three.js, Leaflet and Cesium load only when a
// dashboard holds that widget (see scripts/check-bundle-budget.mjs).
const StreamGlobe = lazy(() => import("./StreamGlobe"));
const StreamFlatMap = lazy(() => import("./StreamFlatMap"));
const StreamSatellite = lazy(() => import("./StreamSatellite"));

const WINDOW_TEXT: Record<
  StreamWindow,
  { label: BloemMessageKey; aria: BloemMessageKey; empty: BloemMessageKey }
> = {
  live: {
    label: "bloem.admin.streamMaps.window.live",
    aria: "bloem.admin.streamMaps.window.liveLabel",
    empty: "bloem.admin.streamMaps.state.emptyLive",
  },
  "24h": {
    label: "bloem.admin.streamMaps.window.day",
    aria: "bloem.admin.streamMaps.window.dayLabel",
    empty: "bloem.admin.streamMaps.state.emptyDay",
  },
  "7d": {
    label: "bloem.admin.streamMaps.window.week",
    aria: "bloem.admin.streamMaps.window.weekLabel",
    empty: "bloem.admin.streamMaps.state.emptyWeek",
  },
};

function MapNote({ children }: { children: string }) {
  return (
    <p className="bg-background/80 text-muted-foreground rounded-md px-2 py-1 text-xs shadow-sm backdrop-blur-sm">
      {children}
    </p>
  );
}

/**
 * The card every stream map widget shares: its title, how many streams it
 * draws, the Live / 24 h / 7 days switch (stored with the placement as the
 * widget's view), the line legend, and the notes the server's answer calls
 * for, around the one map view it was given.
 */
/**
 * Keeps a drag that starts on the map on the map. While the dashboard is
 * being customized the card is draggable, and a mouse press would start
 * moving the whole widget; the browser starts that drag as the press's
 * default action, so cancelling the press is what leaves the drag to the map.
 */
function keepDragOnMap(event: MouseEvent<HTMLElement>) {
  event.preventDefault();
}

function StreamMapCard({
  titleKey,
  View,
}: {
  titleKey: BloemMessageKey;
  View: ComponentType<StreamMapViewProps>;
}) {
  const t = useBloemT();
  const { view, setView } = useWidgetRange();
  const window = streamWindow(view);
  const capabilities = useStreamInsightsCapabilities();
  const trails = useStreamTrails(window);
  const scene = useMemo(() => buildStreamScene(trails.data?.trails ?? []), [trails.data]);

  const options = useMemo<WidgetViewOption<StreamWindow>[]>(
    () =>
      (Object.keys(WINDOW_TEXT) as StreamWindow[]).map((value) => ({
        value,
        label: t(WINDOW_TEXT[value].label),
        ariaLabel: t(WINDOW_TEXT[value].aria),
      })),
    [t],
  );

  const unavailable = capabilities.data?.available === false;
  const notes: string[] = [];
  if (unavailable) notes.push(t("bloem.admin.streamMaps.state.unavailable"));
  // A failed refresh keeps drawing the routes it had; only a map with nothing
  // to draw says the read failed.
  else if (trails.isError && !trails.data) notes.push(t("bloem.admin.streamMaps.state.error"));
  else if (trails.data && scene.routes.length === 0) notes.push(t(WINDOW_TEXT[window].empty));
  if (capabilities.data?.locations === false)
    notes.push(t("bloem.admin.streamMaps.state.noLocations"));
  if (trails.data?.truncated)
    notes.push(t("bloem.admin.streamMaps.state.truncated", { count: scene.routes.length }));

  return (
    <Card className="flex h-full flex-col overflow-hidden">
      <WidgetHeader title={t(titleKey)}>
        {trails.data && (
          <span className="text-muted-foreground text-xs whitespace-nowrap">
            {t("bloem.admin.streamMaps.state.streams", { count: scene.routes.length })}
          </span>
        )}
        <WidgetViewToggle value={window} options={options} onChange={setView} />
      </WidgetHeader>
      {/* A drag on the map turns or pans it, also while the dashboard is
          being customized; the card then moves by its header and its move
          button. */}
      <CardContent className="relative min-h-0 flex-1 p-0" onMouseDown={keepDragOnMap}>
        <Suspense
          fallback={
            <Skeleton
              role="status"
              aria-label={t("bloem.admin.streamMaps.state.loading")}
              className="absolute inset-0 rounded-none"
            />
          }
        >
          <View scene={scene} moving={window === "live"} />
        </Suspense>
        {notes.length > 0 && (
          <div className="pointer-events-none absolute start-2 top-2 flex max-w-[80%] flex-col items-start gap-1">
            {notes.map((note) => (
              <MapNote key={note}>{note}</MapNote>
            ))}
          </div>
        )}
        <ul
          aria-label={t("bloem.admin.streamMaps.legend")}
          className="bg-background/80 pointer-events-none absolute start-2 bottom-2 flex flex-wrap gap-x-2 gap-y-0.5 rounded-md px-2 py-1 text-[0.625rem] shadow-sm backdrop-blur-sm"
        >
          {TRANSPORT_CLASSES.map((transport) => (
            <li key={transport} className="flex items-center gap-1">
              <span
                aria-hidden="true"
                className="inline-block h-0.5 w-3 rounded-full"
                style={{ backgroundColor: TRANSPORT_COLOURS[transport] }}
              />
              {t(`bloem.admin.streamMaps.transport.${transport}`)}
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  );
}

export function StreamGlobeWidget() {
  return <StreamMapCard titleKey="bloem.admin.streamMaps.widgets.globe.title" View={StreamGlobe} />;
}

export function StreamFlatMapWidget() {
  return <StreamMapCard titleKey="bloem.admin.streamMaps.widgets.map.title" View={StreamFlatMap} />;
}

export function StreamSatelliteWidget() {
  return (
    <StreamMapCard
      titleKey="bloem.admin.streamMaps.widgets.satellite.title"
      View={StreamSatellite}
    />
  );
}
