import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import {
  api,
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
} from "@/api/client";
import { v2 } from "@/api/v2/request";
import type { AdminDashboardLayoutDocument, AdminDashboardLayoutResponse } from "@/api/types";
import { adminKeys } from "../keys";

const DASHBOARD_LAYOUT_PATH = "/admin/dashboard/layout";

// A single toast id per concern: a burst of failed saves (offline, server
// down) collapses into one message instead of stacking one per attempt.
const SAVE_TOAST_ID = "admin-dashboard-layout-save";
const RESET_TOAST_ID = "admin-dashboard-layout-reset";

/**
 * Writes to the layout row run one at a time.
 *
 * Same-scope mutations queue and execute in the order they were started, which
 * is what keeps the last write the admin made the one that wins: without it two
 * saves can overlap and the older document can land last, and a reset can
 * land before an in-flight save that then
 * resurrects the arrangement it just discarded.
 */
const LAYOUT_MUTATION_SCOPE = { id: "admin-dashboard-layout" } as const;

/** Reads the canonical account layout under the current request authority. */
export function useAdminDashboardLayout() {
  const profileContext = captureProfileRequestContext();
  return useQuery({
    queryKey: [
      ...adminKeys.dashboardLayout(),
      profileContext?.serverOrigin,
      profileContext?.authContextVersion,
      profileContext?.profileId,
      profileContext?.profileTokenGeneration,
    ],
    queryFn: async (): Promise<AdminDashboardLayoutResponse> => {
      if (!profileContext || !isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      const result = await v2("GET /api/v2/admin/dashboard/layout", { profileContext });
      if (!isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      return { layout: result.layout, updated_at: result.updated_at };
    },
    enabled: profileContext !== null,
    staleTime: Infinity,
    gcTime: Infinity,
  });
}

export function useSaveAdminDashboardLayout() {
  const queryClient = useQueryClient();
  return useMutation({
    scope: LAYOUT_MUTATION_SCOPE,
    mutationFn: (layout: AdminDashboardLayoutDocument) =>
      api<void>(DASHBOARD_LAYOUT_PATH, {
        method: "PUT",
        body: JSON.stringify({ layout }),
      }),
    // Legacy writes acknowledge no canonical document or revision. Invalidate
    // every authority variant rather than fabricating a timestamp or publishing
    // a late write into the currently active account/profile's cache.
    onSuccess: () => queryClient.invalidateQueries({ queryKey: adminKeys.dashboardLayout() }),
    onError: () => {
      // The layout still works from local state, so this is informational.
      toast.error("Failed to save the dashboard layout on the server", { id: SAVE_TOAST_ID });
    },
  });
}

export function useResetAdminDashboardLayout() {
  const queryClient = useQueryClient();
  return useMutation({
    scope: LAYOUT_MUTATION_SCOPE,
    mutationFn: () => api<void>(DASHBOARD_LAYOUT_PATH, { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: adminKeys.dashboardLayout() }),
    onError: () => {
      toast.error("Failed to reset the dashboard layout on the server", { id: RESET_TOAST_ID });
    },
  });
}
