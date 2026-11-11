import { useQuery } from "@tanstack/react-query";

import { adminV2Api, adminV2QueryKey, captureAdminRequestContext } from "@/api/adminV2Client";
import type { BloemSearchMirrorStatus } from "@/api/bloemSearchMirror";
import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";

/**
 * The search mirror's status. Only a Platform administrative context may read
 * it, so the query stays off in an Organization context and outside one.
 * It is bound to the context's authority, so a switch of context drops it.
 */
export function useSearchMirrorStatus() {
  const active = useOptionalAdminContext()?.active ?? null;
  const authority = active ? captureAdminRequestContext(active.key) : null;
  return useQuery({
    queryKey: adminV2QueryKey(active?.key ?? "platform", "search-mirror", authority?.generation),
    enabled: active?.scope === "platform",
    queryFn: ({ signal }) =>
      adminV2Api<BloemSearchMirrorStatus>("/search/mirror", { signal }, "safe", authority),
    staleTime: 15_000,
    refetchInterval: 30_000,
  });
}
