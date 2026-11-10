import { useEffect, useSyncExternalStore } from "react";

import { v2 } from "@/api/v2/request";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { useBuildInfo as useSiloBuildInfo, type BuildInfo } from "@/hooks/queries/admin/system";

// The public server version, read once per page load. A small store rather
// than a query: Silo's sidebar renders, in its tests too, without a query
// client of its own for this.
let publicBuild: BuildInfo | undefined;
let publicBuildFailed = false;
let publicBuildLoad: Promise<void> | undefined;
const listeners = new Set<() => void>();

function loadPublicBuild(): void {
  publicBuildLoad ??= v2("GET /api/v2/system/info").then(
    (info) => {
      publicBuild = {
        display: info.server_version,
        revision: info.server_version,
        dirty: false,
        vcs_time: "",
        available: info.server_version !== "" && info.server_version !== "unavailable",
      };
      for (const listener of listeners) listener();
    },
    () => {
      publicBuildFailed = true;
      for (const listener of listeners) listener();
    },
  );
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

const snapshot = () => publicBuild;

/**
 * The admin sidebar's build box. Silo's build route is the platform
 * administrator's; an organization administrator sees the server version that
 * the public system info route reports to anyone.
 */
export function useBuildInfo() {
  // The sidebar reads the same context (AdminSidebar.tsx).
  const platformScope = useAdminContext().active?.scope === "platform";
  const platform = useSiloBuildInfo(platformScope);
  const data = useSyncExternalStore(subscribe, snapshot, snapshot);
  useEffect(() => {
    if (!platformScope) loadPublicBuild();
  }, [platformScope]);
  if (platformScope) return platform;
  return {
    data,
    isPending: data === undefined && !publicBuildFailed,
    isError: data === undefined && publicBuildFailed,
  };
}
