import { useQuery } from "@tanstack/react-query";
import {
  api,
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
} from "@/api/client";
import { v2 } from "@/api/v2/request";
import type { AdminPlaybackHistoryItem, AdminUserProfile } from "@/api/types";
import { adminKeys } from "../keys";

const ADMIN_HISTORY_STALE_TIME = 15_000;

export interface AdminPlaybackHistoryParams {
  userId?: number;
  profileId?: string;
  mediaItemId?: string;
  completed?: "all" | "true" | "false";
  limit?: number;
}

export function buildAdminPlaybackHistorySearchParams(params: AdminPlaybackHistoryParams) {
  const search = new URLSearchParams();
  if (params.userId) search.set("user_id", String(params.userId));
  if (params.profileId) search.set("profile_id", params.profileId);
  if (params.mediaItemId) search.set("media_item_id", params.mediaItemId);
  if (params.completed && params.completed !== "all") {
    search.set("completed", params.completed);
  } else {
    search.set("completed", "all");
  }
  search.set("limit", String(params.limit ?? 100));
  return search;
}

export function useAdminPlaybackHistory(params: AdminPlaybackHistoryParams) {
  return useQuery({
    queryKey: adminKeys.playbackHistory(params),
    queryFn: () =>
      api<AdminPlaybackHistoryItem[]>(
        `/admin/playback-history?${buildAdminPlaybackHistorySearchParams(params).toString()}`,
      ).then((rows) => rows ?? []),
    staleTime: ADMIN_HISTORY_STALE_TIME,
    refetchInterval: ADMIN_HISTORY_STALE_TIME,
    refetchIntervalInBackground: true,
  });
}

export function useAdminUserProfiles(userId?: number) {
  const c = captureProfileRequestContext();
  const scope = c ? `${c.serverOrigin}:${c.authContextVersion}:${c.profileId}` : "unavailable";
  return useQuery({
    queryKey: [...adminKeys.userProfiles(userId), scope],
    queryFn: async () => {
      if (!c || !isCapturedProfileAuthorityActive(c)) throw new StaleApiRequestContextError();
      const result = await v2("GET /api/v2/admin/users/{id}/profiles", {
        path: { id: String(userId) },
        profileContext: c,
      });
      if (!isCapturedProfileAuthorityActive(c)) throw new StaleApiRequestContextError();
      if (!Array.isArray(result.items) || !result.page || result.page.has_more)
        throw new Error("Incomplete profile listing");
      return result.items as AdminUserProfile[];
    },
    enabled: Boolean(userId),
    retry: false,
    staleTime: ADMIN_HISTORY_STALE_TIME,
  });
}
