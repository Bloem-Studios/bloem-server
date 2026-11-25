import { useQuery, useQueryClient } from "@tanstack/react-query";

import { captureProfileRequestContext } from "@/api/client";
import { captureAdminAuthority } from "@/api/v2/adminAuthority";
import { listAdminPlaybackSessions } from "@/api/v2/adminSessions";
import { adminSessionsKey, readAdminSessionsKeepingPauseEvents } from "@/api/v2/adminSessionsCache";

/**
 * Silo's useAdminSessions (hooks/queries/admin/stats.ts) with one addition:
 * it reads only while enabled. Playback sessions are the platform
 * administrator's, so an organization administrator's sidebar leaves them
 * alone. It shares Silo's query key, so both fill the same cache entry.
 * useScopedAdminSessions.test.ts fails when Silo's hook changes; bring this
 * copy in line then.
 */
export function useScopedAdminSessions(enabled: boolean) {
  const queryClient = useQueryClient();
  const context = captureProfileRequestContext();
  return useQuery({
    queryKey: adminSessionsKey(context),
    queryFn: () =>
      readAdminSessionsKeepingPauseEvents(queryClient, context, () =>
        listAdminPlaybackSessions(context ?? captureAdminAuthority()),
      ),
    enabled: enabled && context !== null,
    staleTime: 30_000,
  });
}
