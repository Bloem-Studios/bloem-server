import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { api } from "@/api/client";
import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";
import type { AccessGroup, AccessGroupInput, AdminContextSummary } from "@/api/types";
import { adminKeys } from "../keys";

const ADMIN_STALE_TIME = 30_000;

function groupKey(context?: AdminContextSummary | null) {
  return context?.scope === "organization"
    ? adminV2QueryKey(context.key, "organization", "groups")
    : adminKeys.accessGroups();
}

export function useAccessGroups(context?: AdminContextSummary | null) {
  return useQuery({
    queryKey: groupKey(context),
    queryFn: () =>
      context?.scope === "organization"
        ? adminV2Api<{ groups: AccessGroup[] }>("/organization/groups").then(
            (data) => data.groups ?? [],
          )
        : api<AccessGroup[]>("/admin/access-groups").then((data) => data ?? []),
    staleTime: ADMIN_STALE_TIME,
  });
}

export function useCreateAccessGroup(context?: AdminContextSummary | null) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: AccessGroupInput) => {
      if (context?.scope === "organization") {
        return adminV2Api<{ group: AccessGroup }>("/organization/groups", {
          method: "POST",
          body: JSON.stringify({ ...body, expected_revision: context.policyRevision }),
        }).then((data) => data.group);
      }
      return api<AccessGroup>("/admin/access-groups", {
        method: "POST",
        body: JSON.stringify(body),
      });
    },
    onSuccess: () => {
      toast.success("Access group created");
      queryClient.invalidateQueries({ queryKey: groupKey(context) });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to create access group");
    },
  });
}

export function useUpdateAccessGroup(context?: AdminContextSummary | null) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: number; body: AccessGroupInput }) => {
      if (context?.scope === "organization") {
        return adminV2Api<{ group: AccessGroup }>(`/organization/groups/${id}`, {
          method: "PUT",
          body: JSON.stringify({ ...body, expected_revision: context.policyRevision }),
        }).then((data) => data.group);
      }
      return api<AccessGroup>(`/admin/access-groups/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      });
    },
    onSuccess: (_data, variables) => {
      toast.success("Access group updated");
      queryClient.invalidateQueries({ queryKey: groupKey(context) });
      if (context?.scope !== "organization") {
        queryClient.invalidateQueries({ queryKey: adminKeys.accessGroup(variables.id) });
      }
      // User views render group-derived data (effective_policy, inherit
      // hints), so a group change must refresh them too.
      queryClient.invalidateQueries({ queryKey: adminKeys.users() });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to update access group");
    },
  });
}

export interface GroupDeletionImpact {
  profiles_reassigned: number;
  default_group_id: number;
}

export function useDeleteAccessGroup(context?: AdminContextSummary | null) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) =>
      context?.scope === "organization"
        ? adminV2Api<GroupDeletionImpact>(`/organization/groups/${id}`, {
            method: "DELETE",
            body: JSON.stringify({ expected_revision: context.policyRevision }),
          })
        : api(`/admin/access-groups/${id}`, { method: "DELETE" }),
    onSuccess: (_data, id) => {
      toast.success("Access group deleted");
      queryClient.invalidateQueries({ queryKey: groupKey(context) });
      if (context?.scope !== "organization") {
        queryClient.invalidateQueries({ queryKey: adminKeys.accessGroup(id) });
        queryClient.invalidateQueries({ queryKey: adminKeys.users() });
      }
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to delete access group");
    },
  });
}
