import { describe, expect, it } from "vitest";

import { loadBloemAdminEnglishForTests } from "@/bloem/i18n";
import {
  DASHBOARD_WIDGETS as SILO_DASHBOARD_WIDGETS,
  DEFAULT_LAYOUT as SILO_DEFAULT_LAYOUT,
} from "@/components/admin/dashboard/registry";
import type { WidgetId } from "@/components/admin/dashboard/types";
import {
  DASHBOARD_WIDGETS,
  DEFAULT_LAYOUT,
  findDashboardWidget,
  getDashboardWidget,
} from "./dashboardRegistry";

const BLOEM_IDS = ["bloem-stream-globe", "bloem-stream-map", "bloem-stream-satellite"];

describe("Bloem's dashboard registry", () => {
  it("lists Silo's widgets in Silo's order, then the three stream maps", () => {
    expect(DASHBOARD_WIDGETS.slice(0, SILO_DASHBOARD_WIDGETS.length)).toEqual(SILO_DASHBOARD_WIDGETS);
    expect(DASHBOARD_WIDGETS.slice(SILO_DASHBOARD_WIDGETS.length).map((widget) => widget.id)).toEqual(
      BLOEM_IDS,
    );
  });

  it("keeps Silo's own definition objects", () => {
    SILO_DASHBOARD_WIDGETS.forEach((widget, index) => expect(DASHBOARD_WIDGETS[index]).toBe(widget));
  });

  it("keeps Silo's default layout, without the maps", () => {
    expect(DEFAULT_LAYOUT).toBe(SILO_DEFAULT_LAYOUT);
    expect(DEFAULT_LAYOUT.some((entry) => BLOEM_IDS.includes(entry.id))).toBe(false);
  });

  it("finds the maps and Silo's widgets by id, and nothing for an unknown id", () => {
    for (const id of BLOEM_IDS) expect(findDashboardWidget(id)?.id).toBe(id);
    expect(findDashboardWidget("now-playing")).toBe(
      SILO_DASHBOARD_WIDGETS.find((widget) => widget.id === "now-playing"),
    );
    expect(findDashboardWidget("bloem-stream-nowhere")).toBeUndefined();
    expect(getDashboardWidget("bloem-stream-map" as WidgetId).id).toBe("bloem-stream-map");
  });

  it("gives each map the Live, 24 h and 7 days views and no range", () => {
    for (const id of BLOEM_IDS) {
      const widget = findDashboardWidget(id);
      expect(widget?.views).toEqual({ allowed: ["live", "24h", "7d"], default: "live" });
      expect(widget?.ranges).toBeUndefined();
    }
  });

  it("names the maps from Bloem's catalog", async () => {
    await loadBloemAdminEnglishForTests();
    expect(BLOEM_IDS.map((id) => findDashboardWidget(id)?.title)).toEqual([
      "Stream globe",
      "Stream map",
      "Stream satellite",
    ]);
    expect(findDashboardWidget("bloem-stream-globe")?.description).toBe(
      "Where streams come from and how they reach the server, on a globe.",
    );
  });
});
