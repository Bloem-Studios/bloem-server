import { platformAdminScope } from "@/bloem/adminScope";
import { useScopedAdminSessions } from "@/bloem/admin/useScopedAdminSessions";
import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";

export * from "@/hooks/queries/admin/stats";

/**
 * The admin sidebar's live session count. Playback sessions are the platform
 * administrator's; an organization administrator's sidebar does not read them.
 */
export function useAdminSessions() {
  // The sidebar reads the same context (AdminSidebar.tsx).
  return useScopedAdminSessions(platformAdminScope(useOptionalAdminContext()));
}
