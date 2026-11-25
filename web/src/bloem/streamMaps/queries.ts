import { useQuery } from "@tanstack/react-query";

import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";
import type { StreamInsightsCapabilities, StreamTrails, StreamWindow } from "./scene";

// Bloem: the platform administrator's stream routes for the dashboard maps
// (docs/bloem/api/insights.md). The dashboard's minute refresh does not mark
// these stale (AdminDashboard.tsx lists Silo's query keys only), so they pace
// themselves: the live window every 30 seconds, the longer ones every 5 minutes.

export const LIVE_REFRESH_MS = 30_000;
export const HISTORY_REFRESH_MS = 5 * 60_000;

export const streamMapKeys = {
  capabilities: adminV2QueryKey("platform", "stream-insights", "capabilities"),
  trails: (window: StreamWindow) => adminV2QueryKey("platform", "streams", "trails", window),
};

export function useStreamInsightsCapabilities() {
  return useQuery({
    queryKey: streamMapKeys.capabilities,
    queryFn: () => adminV2Api<StreamInsightsCapabilities>("/platform/stream-insights/capabilities"),
    staleTime: HISTORY_REFRESH_MS,
  });
}

export function useStreamTrails(window: StreamWindow) {
  const every = window === "live" ? LIVE_REFRESH_MS : HISTORY_REFRESH_MS;
  return useQuery({
    queryKey: streamMapKeys.trails(window),
    queryFn: () =>
      adminV2Api<StreamTrails>(`/platform/streams/trails?window=${encodeURIComponent(window)}`),
    refetchInterval: every,
    staleTime: every / 2,
  });
}
