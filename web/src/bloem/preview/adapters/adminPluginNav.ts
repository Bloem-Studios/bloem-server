import { platformAdminScope } from "@/bloem/adminScope";
import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";
import { useAdminPluginInstallations as useSiloAdminPluginInstallations } from "@/hooks/queries/admin/plugins";

export * from "@/hooks/queries/admin/plugins";

/**
 * The admin sidebar's plugin pages. Plugins are the platform administrator's;
 * an organization administrator's sidebar lists none of their pages.
 */
export function useAdminPluginInstallations(): ReturnType<typeof useSiloAdminPluginInstallations> {
  const installations = useSiloAdminPluginInstallations();
  // The sidebar reads the same context (AdminSidebar.tsx).
  if (platformAdminScope(useOptionalAdminContext())) return installations;
  return { ...installations, data: undefined } as ReturnType<
    typeof useSiloAdminPluginInstallations
  >;
}
