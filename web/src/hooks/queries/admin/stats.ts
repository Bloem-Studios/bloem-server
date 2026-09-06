import { useQuery } from "@tanstack/react-query";
import { api, captureProfileRequestContext } from "@/api/client";
import type { AdminStats } from "@/api/types";
import { adminKeys } from "../keys";

import { listAdminPlaybackSessions } from "@/api/v2/adminSessions";
import { adminUserScope, captureAdminUserAuthority } from "@/api/v2/adminUsers";

const ADMIN_STALE_TIME = 30_000;

export function fetchAdminStats(options: { refresh?: boolean } = {}) {
  return api<AdminStats>(`/admin/stats${options.refresh ? "?refresh=1" : ""}`);
}

export function useAdminStats() {
  return useQuery({
    queryKey: adminKeys.stats(),
    queryFn: () => fetchAdminStats(),
    staleTime: ADMIN_STALE_TIME,
  });
}

export function useAdminSessions() {
  const context = captureProfileRequestContext();
  return useQuery({
    queryKey: [...adminKeys.sessions(), adminUserScope(context)],
    queryFn: () => listAdminPlaybackSessions(context ?? captureAdminUserAuthority()),
    enabled: context !== null,
    staleTime: ADMIN_STALE_TIME,
  });
}
