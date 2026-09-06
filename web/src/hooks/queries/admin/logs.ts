import { useQuery } from "@tanstack/react-query";
import {
  api,
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
} from "@/api/client";
import { v2 } from "@/api/v2/request";
import type { AuditLogListResponse, OperationalLogListResponse } from "@/api/types";
import { adminKeys } from "../keys";

export interface AdminLogQuery {
  from?: string;
  to?: string;
  cursor?: string;
  limit?: number;
  /** One level, or a comma-separated list of them (e.g. `"error,warn"`). */
  level?: string;
  component?: string;
  node_id?: string;
  request_id?: string;
  user_id?: number;
  session_id?: string;
  playback_session_id?: string;
  q?: string;
  method?: string;
  status_code?: number;
  path_prefix?: string;
  client_ip?: string;
}

function toQueryString(params: AdminLogQuery) {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === "") continue;
    search.set(key, String(value));
  }
  return search.toString();
}

function numericLogID(value: string): number {
  const parsed = Number(value);
  if (!/^[1-9][0-9]*$/.test(value) || !Number.isSafeInteger(parsed))
    throw new Error("Log identifier cannot be represented by this client.");
  return parsed;
}

export function useOperationalLogs(params: AdminLogQuery, enabled = true) {
  const profileContext = captureProfileRequestContext();
  const query = {
    cursor: params.cursor,
    limit: params.limit,
    from: params.from,
    to: params.to,
    level: params.level,
    component: params.component,
    node_id: params.node_id,
    request_id: params.request_id,
    user_id: params.user_id === undefined ? undefined : String(params.user_id),
    session_id: params.session_id,
    playback_session_id: params.playback_session_id,
    q: params.q,
  };
  return useQuery({
    queryKey: [
      ...adminKeys.operationalLogs(query),
      profileContext?.serverOrigin,
      profileContext?.authContextVersion,
      profileContext?.profileId,
    ],
    queryFn: async (): Promise<OperationalLogListResponse> => {
      if (!profileContext || !isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      const page = await v2("GET /api/v2/admin/logs/app", { query, profileContext });
      if (!isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      if (!page.page || (page.page.has_more && !page.page.next_cursor))
        throw new Error("Log response is missing pagination metadata.");
      return {
        entries: page.items.map((entry) => ({
          ...entry,
          id: numericLogID(entry.id),
          user_id: entry.user_id == null ? undefined : numericLogID(entry.user_id),
        })),
        next_cursor: page.page.next_cursor ?? undefined,
      };
    },
    staleTime: 5_000,
    enabled: enabled && profileContext !== null,
  });
}

export function useAuditLogs(params: AdminLogQuery, enabled = true) {
  const qs = toQueryString(params);
  return useQuery({
    queryKey: adminKeys.auditLogs({ ...params }),
    queryFn: () => api<AuditLogListResponse>(`/admin/logs/audit${qs ? `?${qs}` : ""}`),
    staleTime: 5_000,
    enabled,
  });
}
