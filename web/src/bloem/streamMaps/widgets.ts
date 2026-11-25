import { bloemT, type BloemMessageKey } from "@/bloem/i18n";
import type {
  DashboardWidgetDefinition,
  DashboardWidgetSpec,
  WidgetId,
} from "@/components/admin/dashboard/types";
import type { MessageKey } from "@/i18n";
import { STREAM_WINDOWS } from "./scene";
import { StreamFlatMapWidget, StreamGlobeWidget, StreamSatelliteWidget } from "./StreamMapWidget";

// Bloem: the stream map widgets Silo's admin dashboard offers beside its own
// (adapters/dashboardRegistry.tsx adds them to the registry). Silo's dashboard
// is the platform administrator's; an organization administrator's admin home
// is the organization overview, so nobody else is offered them.

/** Ids in the saved layout; Bloem's carry its prefix so they never meet one of Silo's. */
export const STREAM_GLOBE_WIDGET_ID = "bloem-stream-globe";
export const STREAM_MAP_WIDGET_ID = "bloem-stream-map";
export const STREAM_SATELLITE_WIDGET_ID = "bloem-stream-satellite";

type BloemWidgetSpec = Omit<DashboardWidgetSpec, "id" | "titleKey" | "descriptionKey"> & {
  id: string;
  titleKey: BloemMessageKey;
  descriptionKey: BloemMessageKey;
};

/**
 * Silo's `defineDashboardWidget` for a widget named in Bloem's catalog. The
 * grid formats `titleKey` with Silo's `t`, which the plugin swaps for one that
 * reads `bloem.*` ids from Bloem's catalog (adapters/i18nWithBloem.ts); the
 * getters follow the interface language, as Silo's do.
 */
function defineBloemDashboardWidget(spec: BloemWidgetSpec): DashboardWidgetDefinition {
  const { id, titleKey, descriptionKey, ...rest } = spec;
  return {
    ...rest,
    id: id as WidgetId,
    titleKey: titleKey as unknown as MessageKey,
    descriptionKey: descriptionKey as unknown as MessageKey,
    get title() {
      return bloemT(titleKey);
    },
    get description() {
      return bloemT(descriptionKey);
    },
  };
}

/** A map wants room: half the grid's width and four rows to start, up to the full width. */
const MAP_BOX = {
  minSpan: 4,
  maxSpan: 12,
  defaultSpan: 6,
  minRows: 3,
  maxRows: 8,
  defaultRows: 4,
} as const;

/** The window is the widget's view: Live, 24 h or 7 days, stored with the placement. */
const WINDOW_VIEWS = { allowed: STREAM_WINDOWS, default: "live" } as const;

export const BLOEM_DASHBOARD_WIDGETS: readonly DashboardWidgetDefinition[] = [
  defineBloemDashboardWidget({
    id: STREAM_GLOBE_WIDGET_ID,
    titleKey: "bloem.admin.streamMaps.widgets.globe.title",
    descriptionKey: "bloem.admin.streamMaps.widgets.globe.description",
    ...MAP_BOX,
    views: WINDOW_VIEWS,
    Component: StreamGlobeWidget,
  }),
  defineBloemDashboardWidget({
    id: STREAM_MAP_WIDGET_ID,
    titleKey: "bloem.admin.streamMaps.widgets.map.title",
    descriptionKey: "bloem.admin.streamMaps.widgets.map.description",
    ...MAP_BOX,
    views: WINDOW_VIEWS,
    Component: StreamFlatMapWidget,
  }),
  defineBloemDashboardWidget({
    id: STREAM_SATELLITE_WIDGET_ID,
    titleKey: "bloem.admin.streamMaps.widgets.satellite.title",
    descriptionKey: "bloem.admin.streamMaps.widgets.satellite.description",
    ...MAP_BOX,
    views: WINDOW_VIEWS,
    Component: StreamSatelliteWidget,
  }),
];
