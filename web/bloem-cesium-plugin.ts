import type { Plugin } from "vite";
import cesium from "vite-plugin-cesium";

// Bloem: CesiumJS for the dashboard's stream satellite widget
// (src/bloem/streamMaps/StreamSatellite.tsx), set up as Proxy Manager's web
// sets up its Satellite view.
//
// Cesium is bundled into the widget's lazily loaded chunk (rebuildCesium)
// instead of being loaded as a global. In its default mode the plugin puts a
// render-blocking <script src="/cesium/Cesium.js"> (about 6 MB) into
// index.html, so every page would download and parse it before the app runs;
// its transformIndexHtml hook would also link /cesium/Widgets/widgets.css on
// every page. Both are dropped: the widget imports what it needs itself. The
// plugin still copies Assets, Workers and ThirdParty to dist/cesium, and its
// output intro sets window.CESIUM_BASE_URL so the bundled library finds them.
//
// Not under Vitest: tests never draw the satellite view, and the plugin's
// rebuild would only slow every run.
export function bloemCesium(): Plugin[] {
  if (process.env.VITEST) return [];
  const plugin = cesium({ rebuildCesium: true });
  delete plugin.transformIndexHtml;
  return [plugin];
}
