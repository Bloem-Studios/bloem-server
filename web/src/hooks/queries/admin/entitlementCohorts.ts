import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";
import type { AdminContextKey } from "@/api/types";
import { organizationPeopleKeys, type PeopleBulkJob } from "./organizationPeople";

export interface EffectivePolicy {
  library_ids: number[] | null;
  playback_allowed: boolean;
  max_streams: number;
  max_profiles: number;
  transcode_allowed: boolean;
  max_transcodes: number;
  download_allowed: boolean;
  download_transcode_allowed: boolean;
  max_playback_quality: string;
  allowed_permissions: string[] | null;
  requests_allowed: boolean;
}

export interface EntitlementCohort {
  cohort_id: string;
  organization_id: string;
  name: string;
  revision: number;
  access_group_id: number;
  source_template_key?: string;
  source_template_revision?: number;
  parent_cohort_id?: string;
  derivation_kind: "exact_template" | "policy_patch" | "managed_default";
  policy: EffectivePolicy;
  policy_digest: string;
  member_count: number;
  archived: boolean;
  created_by_account_id?: number;
  created_at: string;
}

export type SetMode = "add" | "remove" | "replace";
export type LibrarySetMode = SetMode | "all" | "none";
export type PermissionSetMode = SetMode | "unrestricted";

export interface PolicyPatch {
  libraries?: { mode: LibrarySetMode; values?: number[] };
  permissions?: { mode: PermissionSetMode; values?: string[] };
  playback_allowed?: boolean;
  transcode_allowed?: boolean;
  download_allowed?: boolean;
  download_transcode_allowed?: boolean;
  requests_allowed?: boolean;
  max_streams?: number;
  max_profiles?: number;
  max_transcodes?: number;
  max_playback_quality?: string;
}

export type PolicyCommand =
  | {
      kind: "assign_entitlement_cohort";
      cohort_id: string;
      include_custom_profiles: boolean;
    }
  | {
      kind: "apply_entitlement_template";
      template_key: string;
      template_revision: number;
      include_custom_profiles: boolean;
    }
  | {
      kind: "derive_entitlement_cohort";
      cohort_id: string;
      name: string;
      patch: PolicyPatch;
      include_custom_profiles: boolean;
    }
  | {
      kind: "restore_default_entitlement";
      include_custom_profiles: boolean;
    };

export interface CohortDistribution {
  group_id?: number;
  group_name?: string;
  cohort_id?: string;
  cohort_revision?: number;
  source_template_key?: string;
  source_template_revision?: number;
  state: "managed" | "custom" | "legacy_unmanaged";
  count: number;
}

export interface PolicyTarget {
  kind: PolicyCommand["kind"];
  cohort_id?: string;
  cohort_revision?: number;
  parent_cohort_id?: string;
  group_id?: number;
  template_key?: string;
  template_revision?: number;
  name?: string;
  policy_digest: string;
  policy: EffectivePolicy;
}

export interface PolicyPreview {
  matched: number;
  excluded: number;
  already_compliant: number;
  inherited_profiles_will_move: number;
  custom_profiles_will_remain: number;
  custom_profiles_will_move: number;
  ineligible_or_stale: number;
  current_cohorts: CohortDistribution[];
  target: PolicyTarget;
  diff: Array<{ field: string; changed_accounts: number }>;
  selection_expires_at: string;
  confirmation_expires_at: string;
  confirmation_token: string;
}

export const entitlementCohortKeys = {
  root: (contextKey: AdminContextKey) =>
    adminV2QueryKey(contextKey, "organization", "entitlement-cohorts"),
  list: (contextKey: AdminContextKey, includeArchived: boolean) =>
    [
      ...adminV2QueryKey(contextKey, "organization", "entitlement-cohorts"),
      "list",
      { includeArchived },
    ] as const,
  detail: (contextKey: AdminContextKey, cohortID: string) =>
    [...adminV2QueryKey(contextKey, "organization", "entitlement-cohorts"), cohortID] as const,
  preview: (contextKey: AdminContextKey) =>
    adminV2QueryKey(contextKey, "organization", "people", "policy-preview"),
  job: (contextKey: AdminContextKey, jobID: string) =>
    adminV2QueryKey(contextKey, "organization", "people", "policy-job", jobID),
};

export function useOrganizationEntitlementCohorts(
  contextKey: AdminContextKey,
  includeArchived = false,
) {
  return useQuery({
    queryKey: entitlementCohortKeys.list(contextKey, includeArchived),
    queryFn: () =>
      adminV2Api<{ cohorts: EntitlementCohort[] }>(
        `/organization/entitlement-cohorts?include_archived=${String(includeArchived)}`,
      ).then((result) => result.cohorts ?? []),
  });
}

export function useOrganizationEntitlementCohort(contextKey: AdminContextKey, cohortID?: string) {
  return useQuery({
    queryKey: entitlementCohortKeys.detail(contextKey, cohortID ?? "none"),
    queryFn: () =>
      adminV2Api<{ cohort: EntitlementCohort }>(
        `/organization/entitlement-cohorts/${encodeURIComponent(cohortID ?? "")}`,
      ).then((result) => result.cohort),
    enabled: Boolean(cohortID),
  });
}

export function useCreatePolicyPreview(contextKey: AdminContextKey) {
  return useMutation({
    mutationKey: entitlementCohortKeys.preview(contextKey),
    mutationFn: (input: { selectionToken: string; command: PolicyCommand }) =>
      adminV2Api<{ preview: PolicyPreview }>("/organization/people/policy-previews", {
        method: "POST",
        body: JSON.stringify({
          selection_token: input.selectionToken,
          command: input.command,
        }),
      }).then((result) => result.preview),
  });
}

export function useCreatePolicyJob(contextKey: AdminContextKey) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: adminV2QueryKey(contextKey, "organization", "people", "policy-job"),
    mutationFn: (input: {
      selectionToken: string;
      confirmationToken: string;
      idempotencyKey: string;
      command: PolicyCommand;
    }) =>
      adminV2Api<{ job: PeopleBulkJob }>("/organization/people/policy-jobs", {
        method: "POST",
        body: JSON.stringify({
          selection_token: input.selectionToken,
          confirmation_token: input.confirmationToken,
          idempotency_key: input.idempotencyKey,
          command: input.command,
        }),
      }).then((result) => result.job),
    onSuccess: (job) => {
      queryClient.setQueryData(entitlementCohortKeys.job(contextKey, job.job_id), job);
    },
  });
}

export function usePolicyJob(contextKey: AdminContextKey, jobID?: string) {
  return useQuery({
    queryKey: entitlementCohortKeys.job(contextKey, jobID ?? "none"),
    queryFn: () =>
      adminV2Api<{ job: PeopleBulkJob }>(
        `/organization/people/policy-jobs/${encodeURIComponent(jobID ?? "")}`,
      ).then((result) => result.job),
    enabled: Boolean(jobID),
    refetchInterval: (query) => {
      const status = (query.state.data as PeopleBulkJob | undefined)?.status;
      return status === "queued" || status === "running" ? 1000 : false;
    },
  });
}

export function useCancelPolicyJob(contextKey: AdminContextKey) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: adminV2QueryKey(contextKey, "organization", "people", "policy-job", "cancel"),
    mutationFn: (jobID: string) =>
      adminV2Api<{ job: PeopleBulkJob }>(
        `/organization/people/policy-jobs/${encodeURIComponent(jobID)}/cancel`,
        { method: "POST", body: "{}" },
      ).then((result) => result.job),
    onSuccess: async (job) => {
      queryClient.setQueryData(entitlementCohortKeys.job(contextKey, job.job_id), job);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: entitlementCohortKeys.root(contextKey) }),
        queryClient.invalidateQueries({ queryKey: organizationPeopleKeys.peopleRoot(contextKey) }),
      ]);
    },
  });
}
