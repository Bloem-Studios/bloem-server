import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
  type ProfileRequestContextSnapshot,
} from "@/api/client";
import { nativeApiWithProfileRequestContext as apiWithProfileRequestContext } from "@/api/bloemClient";
import type { LiveTVSeriesRule, LiveTVSeriesRulesResponse } from "@/api/bloemTypes";
import { useAuth } from "@/hooks/useAuth";
import { liveTVKeys } from "./bloemKeys";

function useRuleScope() {
  const { profile } = useAuth();
  const captured = captureProfileRequestContext();
  const authority = captured?.profileId === profile?.id ? captured : null;
  return {
    authority,
    queryKey: [
      ...liveTVKeys.liveTVSeriesRules(),
      authority?.serverOrigin,
      authority?.authContextVersion,
      authority?.profileId,
      authority?.profileTokenGeneration,
    ],
  };
}

async function ruleRequest<T>(
  authority: ProfileRequestContextSnapshot | null,
  suffix = "",
  options: RequestInit = {},
): Promise<T> {
  if (!authority || !isCapturedProfileAuthorityActive(authority)) {
    throw new StaleApiRequestContextError();
  }
  const result = await apiWithProfileRequestContext<T>(
    `/livetv/series-rules${suffix}`,
    authority,
    options,
    options.method ? "none" : "safe",
  );
  if (!isCapturedProfileAuthorityActive(authority)) throw new StaleApiRequestContextError();
  return result;
}

export function useLiveTVSeriesRules() {
  const { authority, queryKey } = useRuleScope();
  return useQuery({
    queryKey,
    enabled: Boolean(authority),
    queryFn: ({ signal }) =>
      ruleRequest<LiveTVSeriesRulesResponse>(authority, "", { signal }).then(
        (data) => data.series_rules ?? [],
      ),
    staleTime: 30_000,
    retry: false,
  });
}

export interface CreateLiveTVSeriesRuleRequest {
  title_match: string;
  series_id?: string;
  channel_id?: string;
  new_only: boolean;
}

export function useCreateLiveTVSeriesRule() {
  const { authority, queryKey } = useRuleScope();
  const client = useQueryClient();
  return useMutation({
    networkMode: "always",
    retry: false,
    mutationFn: (body: CreateLiveTVSeriesRuleRequest) =>
      ruleRequest<LiveTVSeriesRule>(authority, "", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSettled: () => client.invalidateQueries({ queryKey }),
  });
}

export function useDeleteLiveTVSeriesRule() {
  const { authority, queryKey } = useRuleScope();
  const client = useQueryClient();
  return useMutation({
    networkMode: "always",
    retry: false,
    mutationFn: (id: string) =>
      ruleRequest<void>(authority, `/${encodeURIComponent(id)}`, { method: "DELETE" }),
    onSettled: () => client.invalidateQueries({ queryKey }),
  });
}
