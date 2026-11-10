import { useQuery } from "@tanstack/react-query";

import { v2 } from "@/api/v2/request";
import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";
import { useBuildInfo as useSiloBuildInfo, type BuildInfo } from "@/hooks/queries/admin/system";

/**
 * The admin sidebar's build box. Silo's build route is the platform
 * administrator's; an organization administrator sees the server version that
 * the public system info route reports to anyone.
 */
export function useBuildInfo() {
  const platformScope = useOptionalAdminContext()?.active?.scope === "platform";
  const platform = useSiloBuildInfo(platformScope);
  const organization = useQuery({
    queryKey: ["bloem", "system-info", "build"],
    queryFn: async (): Promise<BuildInfo> => {
      const info = await v2("GET /api/v2/system/info");
      const available = info.server_version !== "" && info.server_version !== "unavailable";
      return {
        display: info.server_version,
        revision: info.server_version,
        dirty: false,
        vcs_time: "",
        available,
      };
    },
    staleTime: Number.POSITIVE_INFINITY,
    retry: false,
    enabled: !platformScope,
  });
  return platformScope ? platform : organization;
}
