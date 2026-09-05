import { useQuery } from "@tanstack/react-query";
import {
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  nativeApiWithProfileRequestContext,
  StaleApiRequestContextError,
} from "@/api/client";
import { useAuth } from "@/hooks/useAuth";

export interface LiveTVCapability {
  supported: boolean;
  allowed: boolean;
  available: boolean;
  heartbeat_interval_seconds: number;
}

export function useLiveTVAccess() {
  const { profile } = useAuth();
  const authority = captureProfileRequestContext();
  return useQuery({
    // Never put access or PIN tokens in query keys or query data.
    queryKey: [
      "live-tv-access",
      authority?.serverOrigin,
      authority?.authContextVersion,
      profile?.id,
    ],
    enabled: Boolean(authority && profile?.id === authority.profileId),
    queryFn: async ({ signal }) => {
      if (!authority || !isCapturedProfileAuthorityActive(authority)) {
        throw new StaleApiRequestContextError();
      }
      const result = await nativeApiWithProfileRequestContext<LiveTVCapability>(
        "/livetv/capability",
        authority,
        { signal },
      );
      if (!isCapturedProfileAuthorityActive(authority)) {
        throw new StaleApiRequestContextError();
      }
      return result;
    },
    staleTime: 0,
    refetchInterval: 30_000,
    retry: false,
  });
}
