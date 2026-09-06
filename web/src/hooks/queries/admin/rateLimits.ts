import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  api,
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
} from "@/api/client";
import type { RateLimitConfig, RateLimitUpdateResponse } from "@/api/types";
import { adminKeys } from "../keys";
import { toast } from "sonner";

import { v2 } from "@/api/v2/request";

const ADMIN_STALE_TIME = 30_000;

export function useRateLimitConfig() {
  const context = captureProfileRequestContext();
  return useQuery({
    queryKey: [
      ...adminKeys.rateLimitConfig(),
      context?.serverOrigin,
      context?.authContextVersion,
      context?.profileId,
    ],
    enabled: context !== null,
    queryFn: async (): Promise<RateLimitConfig> => {
      if (!context || !isCapturedProfileAuthorityActive(context))
        throw new StaleApiRequestContextError();
      const [config, status] = await Promise.all([
        v2("GET /api/v2/admin/rate-limits/config", { profileContext: context }),
        v2("GET /api/v2/admin/rate-limits/status", { profileContext: context }),
      ]);
      if (!isCapturedProfileAuthorityActive(context)) throw new StaleApiRequestContextError();
      return { ...config, ...status };
    },
    staleTime: ADMIN_STALE_TIME,
  });
}

export function useUpdateRateLimitConfig() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (config: RateLimitConfig) =>
      api<RateLimitUpdateResponse>("/admin/rate-limits/config", {
        method: "PUT",
        body: JSON.stringify(config),
      }),
    onSuccess: async (data) => {
      if (data.restart_required) {
        toast.success("Rate limit settings saved — restart the server to apply them");
      } else {
        toast.success("Rate limit settings saved");
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: adminKeys.rateLimitConfig() }),
        queryClient.invalidateQueries({ queryKey: adminKeys.serverStatus() }),
      ]);
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to save rate limit settings");
    },
  });
}
