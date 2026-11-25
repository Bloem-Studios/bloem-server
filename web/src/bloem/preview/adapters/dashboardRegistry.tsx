import { BLOEM_DASHBOARD_WIDGETS } from "@/bloem/streamMaps/widgets";
import {
  DASHBOARD_WIDGETS as SILO_DASHBOARD_WIDGETS,
  findDashboardWidget as findSiloDashboardWidget,
  getDashboardWidget as getSiloDashboardWidget,
} from "@/components/admin/dashboard/registry";
import type { DashboardWidgetDefinition, WidgetId } from "@/components/admin/dashboard/types";

// Silo's dashboard registry with Bloem's stream map widgets after Silo's own,
// for the dashboard modules that list, place and look up widgets
// (bloem-preview-plugin.ts). Silo's default layout stays Silo's: an admin adds
// the maps from the Add widget sheet.

export { DEFAULT_LAYOUT } from "@/components/admin/dashboard/registry";

/** Silo's widgets in Silo's order, then Bloem's. The definitions are Silo's own objects, so their text getters still follow the language. */
export const DASHBOARD_WIDGETS: DashboardWidgetDefinition[] = [
  ...SILO_DASHBOARD_WIDGETS,
  ...BLOEM_DASHBOARD_WIDGETS,
];

const BLOEM_WIDGETS_BY_ID = new Map<string, DashboardWidgetDefinition>(
  BLOEM_DASHBOARD_WIDGETS.map((widget) => [widget.id, widget]),
);

export function getDashboardWidget(id: WidgetId): DashboardWidgetDefinition {
  return BLOEM_WIDGETS_BY_ID.get(id) ?? getSiloDashboardWidget(id);
}

/** A saved layout's id, Silo's (with its legacy names) or Bloem's; undefined for one this build does not know. */
export function findDashboardWidget(id: string): DashboardWidgetDefinition | undefined {
  return BLOEM_WIDGETS_BY_ID.get(id) ?? findSiloDashboardWidget(id);
}
