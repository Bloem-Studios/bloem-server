import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import {
  createAccessGroup,
  deleteAccessGroup,
  getAccessGroup,
  listAccessGroups,
  updateAccessGroup,
} from "@/api/v2/accessGroups";
import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";
import type { AccessGroup, AccessGroupInput } from "@/api/types";
import type { components } from "@/api/v2/schema";
import { bloemT } from "@/bloem/i18n";
import type { AdminContextSummary } from "@/api/bloemTypes";
import { adminKeys } from "../keys";

const ADMIN_STALE_TIME = 30_000;

// The organization routes answer with Silo's /api/v2 access group and take its
// write body, so they share the v2 client's reading of both.
type V2Group = components["schemas"]["AdminAccessGroup"];

function groupID(value: string) {
  const id = Number(value);
  if (!Number.isSafeInteger(id) || id <= 0)
    throw new Error("This group contains an unsupported ID.");
  return id;
}

function fromV2(group: V2Group, memberCount = 0): AccessGroup {
  return {
    ...group,
    id: groupID(group.id),
    library_ids: group.library_ids?.map(groupID) ?? null,
    allowed_permissions: group.allowed_permissions ?? null,
    member_count: memberCount,
  };
}

function toV2(body: AccessGroupInput) {
  return {
    ...body,
    library_ids: body.library_ids == null ? body.library_ids : body.library_ids.map(String),
  };
}

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
        ? adminV2Api<{ groups: (V2Group & { member_count: number })[] }>(
            "/organization/groups",
          ).then((data) => (data.groups ?? []).map((group) => fromV2(group, group.member_count)))
        : listAccessGroups(),
    staleTime: ADMIN_STALE_TIME,
  });
}

export function useCreateAccessGroup(context?: AdminContextSummary | null) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: AccessGroupInput) => {
      if (context?.scope === "organization") {
        return adminV2Api<{ group: V2Group }>("/organization/groups", {
          method: "POST",
          body: JSON.stringify({ ...toV2(body), expected_revision: context.policyRevision }),
        }).then((data) => fromV2(data.group));
      }
      return createAccessGroup(body);
    },
    onSuccess: () => {
      toast.success(bloemT("bloem.admin.accessGroups.toasts.created"));
      queryClient.invalidateQueries({ queryKey: groupKey(context) });
    },
    onError: (err) => {
      toast.error(
        err instanceof Error ? err.message : bloemT("bloem.admin.accessGroups.toasts.createFailed"),
      );
    },
  });
}

export function useUpdateAccessGroup(context?: AdminContextSummary | null) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: number; body: AccessGroupInput }) => {
      if (context?.scope === "organization") {
        return adminV2Api<{ group: V2Group }>(`/organization/groups/${id}`, {
          method: "PUT",
          body: JSON.stringify({ ...toV2(body), expected_revision: context.policyRevision }),
        }).then((data) => fromV2(data.group));
      }
      return getAccessGroup(id)
        .then((editor) => updateAccessGroup(editor, body))
        .then((saved) => saved.group);
    },
    onSuccess: (_data, variables) => {
      toast.success(bloemT("bloem.admin.accessGroups.toasts.updated"));
      queryClient.invalidateQueries({ queryKey: groupKey(context) });
      if (context?.scope !== "organization") {
        queryClient.invalidateQueries({ queryKey: adminKeys.accessGroup(variables.id) });
      }
      // User views render group-derived data (effective_policy, inherit
      // hints), so a group change must refresh them too.
      queryClient.invalidateQueries({ queryKey: adminKeys.users() });
    },
    onError: (err) => {
      toast.error(
        err instanceof Error ? err.message : bloemT("bloem.admin.accessGroups.toasts.updateFailed"),
      );
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
    mutationFn: (id: number): Promise<GroupDeletionImpact | void> =>
      context?.scope === "organization"
        ? adminV2Api<GroupDeletionImpact>(`/organization/groups/${id}`, {
            method: "DELETE",
            body: JSON.stringify({ expected_revision: context.policyRevision }),
          })
        : getAccessGroup(id).then(deleteAccessGroup),
    onSuccess: (_data, id) => {
      toast.success(bloemT("bloem.admin.accessGroups.toasts.deleted"));
      queryClient.invalidateQueries({ queryKey: groupKey(context) });
      if (context?.scope !== "organization") {
        queryClient.invalidateQueries({ queryKey: adminKeys.accessGroup(id) });
        queryClient.invalidateQueries({ queryKey: adminKeys.users() });
      }
    },
    onError: (err) => {
      toast.error(
        err instanceof Error ? err.message : bloemT("bloem.admin.accessGroups.toasts.deleteFailed"),
      );
    },
  });
}
