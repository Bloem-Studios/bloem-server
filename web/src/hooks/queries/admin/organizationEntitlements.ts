import { useMutation, useQueryClient } from "@tanstack/react-query";
import { adminV2Api, adminV2QueryKey, captureAdminRequestContext } from "@/api/adminV2Client";
import type { AdminContextKey } from "@/api/bloemTypes";

export type OrganizationEntitlementAction = "active" | "suspended" | "withdraw";

export function useChangeOrganizationEntitlement(contextKey: AdminContextKey) {
  const client = useQueryClient();
  const authority = captureAdminRequestContext(contextKey);
  return useMutation({
    // Never queue a destructive intent for replay after a context/network change.
    networkMode: "always",
    retry: false,
    mutationFn: ({
      folderId,
      revision,
      action,
    }: {
      folderId: number;
      revision: number;
      action: OrganizationEntitlementAction;
    }) =>
      adminV2Api(
        `/organization/entitlements/${folderId}`,
        {
          method: action === "withdraw" ? "DELETE" : "PUT",
          body: JSON.stringify({
            expected_revision: revision,
            ...(action === "withdraw" ? {} : { status: action }),
          }),
        },
        "none",
        authority,
      ),
    // A lost response can still mean the server committed the operation.
    onSettled: () =>
      client.invalidateQueries({
        queryKey: adminV2QueryKey(contextKey, "organization", "libraries"),
      }),
  });
}
