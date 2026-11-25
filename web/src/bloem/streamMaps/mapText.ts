import { useEffect, useState, type RefObject } from "react";

import type { BloemMessageKey, BloemTranslate } from "@/bloem/i18n";
import type { StreamMarker, StreamRoute } from "./scene";

// Bloem: what the three stream maps share about their drawing: the words for
// places and lines, and full screen (the buttons and credit are mapChrome.tsx).

const HOP_KINDS: Record<string, BloemMessageKey> = {
  viewer: "bloem.admin.streamMaps.hop.viewer",
  cloudflare: "bloem.admin.streamMaps.hop.cloudflare",
  edge: "bloem.admin.streamMaps.hop.edge",
  server: "bloem.admin.streamMaps.hop.server",
  node: "bloem.admin.streamMaps.hop.node",
};

function hopKindText(t: BloemTranslate, kind: string): string {
  return t(HOP_KINDS[kind] ?? "bloem.admin.streamMaps.hop.other");
}

/** "Edge: ns11", "Viewer: Netherlands (approximate)", "Server: Bloem · 12 streams". */
export function markerText(t: BloemTranslate, marker: StreamMarker): string {
  const values = { kind: hopKindText(t, marker.kind), label: marker.label };
  const place = marker.approximate
    ? t("bloem.admin.streamMaps.placeApproximate", values)
    : t("bloem.admin.streamMaps.place", values);
  return marker.streams > 1
    ? t("bloem.admin.streamMaps.placeShared", { place, count: marker.streams })
    : place;
}

/** "UDP · Amsterdam → AMS → ns11 → Bloem". */
export function routeText(t: BloemTranslate, route: StreamRoute): string {
  return t("bloem.admin.streamMaps.route", {
    transport: t(`bloem.admin.streamMaps.transport.${route.transport}`),
    route: route.hops.map((hop) => hop.label).join(" → "),
  });
}

/**
 * A tooltip node for a map layer that would otherwise parse its label as HTML
 * (react-globe.gl's labels, Leaflet's tooltips): the server's place names are
 * set as text, never markup.
 */
export function textTooltip(text: string): HTMLElement {
  const element = document.createElement("div");
  element.textContent = text;
  element.className =
    "bg-popover text-popover-foreground rounded-md border px-2 py-1 text-xs shadow-md whitespace-nowrap";
  return element;
}

export function fullscreenAvailable(): boolean {
  return typeof document !== "undefined" && document.fullscreenEnabled === true;
}

/** Whether `target` is the page's full-screen element, following the browser's changes. */
export function useFullscreen(target: RefObject<HTMLElement | null>): [boolean, () => void] {
  const [fullscreen, setFullscreen] = useState(false);
  useEffect(() => {
    const update = () => setFullscreen(document.fullscreenElement === target.current);
    document.addEventListener("fullscreenchange", update);
    return () => document.removeEventListener("fullscreenchange", update);
  }, [target]);
  const toggle = () => {
    if (document.fullscreenElement) void document.exitFullscreen();
    else void target.current?.requestFullscreen();
  };
  return [fullscreen, toggle];
}
