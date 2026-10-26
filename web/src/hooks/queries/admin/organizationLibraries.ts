// Bloem: the libraries an organization owns or is entitled to, read in its
// administrative context.
import { useQuery } from "@tanstack/react-query";
import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";
import type { AdminContextKey } from "@/api/bloemTypes";

export interface OrganizationLibraryProjection {
  folder_id: number;
  name: string;
  type: string;
  access_kind: "owned" | "entitled";
  entitlement?: {
    id: string;
    organization_id?: string;
    status: "active" | "suspended" | "revoked";
    security_revision: number;
  };
}

export function useOrganizationLibraries(contextKey: AdminContextKey, enabled = true) {
  return useQuery({
    queryKey: adminV2QueryKey(contextKey, "organization", "libraries"),
    queryFn: () =>
      adminV2Api<{ libraries: OrganizationLibraryProjection[] }>("/organization/libraries").then(
        (data) => data.libraries ?? [],
      ),
    enabled,
  });
}
