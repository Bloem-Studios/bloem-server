import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";
import type { AdminContextKey } from "@/api/types";

export type MembershipStatus = "active" | "suspended" | "invited";
export type PeopleSort = "name" | "email" | "recent_activity";

export interface PeopleFilters {
  query: string;
  status: MembershipStatus[];
  groupIds: number[];
  activeSince?: string;
  sort: PeopleSort;
  cursor?: string;
  limit: number;
}

export interface OrganizationProfile {
  id: string;
  name: string;
  group_id: number;
  group_name: string;
  updated_at: string;
}

export interface OrganizationPerson {
  organization_id: string;
  account_id: number;
  email: string;
  display_name: string;
  membership_id: string;
  membership_status: MembershipStatus;
  legacy_role: "admin" | "user";
  security_revision: number;
  last_activity: string;
  profiles: OrganizationProfile[];
}

export interface PeoplePageData {
  items: OrganizationPerson[];
  next_cursor?: string;
  approximate_total: number;
}

export interface PeopleSelection {
  token: string;
  matched: number;
  excluded: number;
  expires_at: string;
}

export interface BulkRecordResult {
  account_id: number;
  reason: string;
}

export interface PeopleBulkJob {
  job_id: string;
  status: "queued" | "running" | "completed" | "failed" | "cancelled";
  progress_current: number;
  progress_total: number;
  succeeded: number;
  skipped: BulkRecordResult[];
  failed: BulkRecordResult[];
  target_cohort_id?: string;
  target_cohort_revision?: number;
  target_group_id?: number;
}

export interface OrganizationOverview {
  id: string;
  slug: string;
  name: string;
  status: "initializing" | "active" | "suspended";
  owner_account_id?: number;
  policy_revision: number;
  membership_count: number;
  profile_count: number;
  library_count: number;
  entitlement_count: number;
}

export interface OrganizationGroup {
  id: number;
  name: string;
}

export const defaultPeopleFilters: PeopleFilters = {
  query: "",
  status: [],
  groupIds: [],
  sort: "name",
  limit: 50,
};

export function peopleFiltersFromSearch(params: URLSearchParams): PeopleFilters {
  const statuses = params
    .getAll("status")
    .flatMap((value) => value.split(","))
    .filter((value): value is MembershipStatus =>
      ["active", "suspended", "invited"].includes(value),
    );
  const groupIds = params
    .getAll("group_id")
    .flatMap((value) => value.split(","))
    .map(Number)
    .filter((value) => Number.isInteger(value) && value > 0);
  const requestedSort = params.get("sort");
  const sort: PeopleSort = ["email", "recent_activity"].includes(requestedSort ?? "")
    ? (requestedSort as PeopleSort)
    : "name";
  return {
    query: params.get("query")?.trim() ?? "",
    status: statuses,
    groupIds,
    activeSince: params.get("active_since") || undefined,
    sort,
    cursor: params.get("cursor") || undefined,
    limit: 50,
  };
}

export function peopleFiltersToSearch(filters: PeopleFilters): URLSearchParams {
  const params = new URLSearchParams();
  if (filters.query) params.set("query", filters.query);
  filters.status.forEach((status) => params.append("status", status));
  filters.groupIds.forEach((id) => params.append("group_id", String(id)));
  if (filters.activeSince) params.set("active_since", filters.activeSince);
  if (filters.sort !== "name") params.set("sort", filters.sort);
  if (filters.cursor) params.set("cursor", filters.cursor);
  return params;
}

export function canonicalSelectionFilter(filters: PeopleFilters) {
  return {
    query: filters.query,
    status: filters.status,
    group_ids: filters.groupIds,
    ...(filters.activeSince ? { active_since: filters.activeSince } : {}),
    sort: filters.sort,
  };
}

function peopleQueryString(filters: PeopleFilters): string {
  const params = peopleFiltersToSearch(filters);
  params.set("limit", String(filters.limit));
  return params.toString();
}

export const organizationPeopleKeys = {
  root: (contextKey: AdminContextKey) => adminV2QueryKey(contextKey, "organization"),
  overview: (contextKey: AdminContextKey) =>
    adminV2QueryKey(contextKey, "organization", "overview"),
  peopleRoot: (contextKey: AdminContextKey) =>
    adminV2QueryKey(contextKey, "organization", "people", "list"),
  people: (contextKey: AdminContextKey, filters: PeopleFilters) =>
    [...adminV2QueryKey(contextKey, "organization", "people", "list"), filters] as const,
  groups: (contextKey: AdminContextKey) => adminV2QueryKey(contextKey, "organization", "groups"),
  job: (contextKey: AdminContextKey, jobId: string) =>
    adminV2QueryKey(contextKey, "organization", "people", "bulk-job", jobId),
};

export function useOrganizationOverview(contextKey: AdminContextKey) {
  return useQuery({
    queryKey: organizationPeopleKeys.overview(contextKey),
    queryFn: () =>
      adminV2Api<{ organization: OrganizationOverview }>("/organization/overview").then(
        (result) => result.organization,
      ),
  });
}

export function useOrganizationPeople(contextKey: AdminContextKey, filters: PeopleFilters) {
  return useQuery({
    queryKey: organizationPeopleKeys.people(contextKey, filters),
    queryFn: () => adminV2Api<PeoplePageData>(`/organization/people?${peopleQueryString(filters)}`),
  });
}

export function useOrganizationGroups(contextKey: AdminContextKey) {
  return useQuery({
    queryKey: organizationPeopleKeys.groups(contextKey),
    queryFn: () =>
      adminV2Api<{ groups: OrganizationGroup[] }>("/organization/groups").then(
        (result) => result.groups,
      ),
  });
}

export function useCreatePeopleSelection(contextKey: AdminContextKey) {
  return useMutation({
    mutationKey: adminV2QueryKey(contextKey, "organization", "people", "selection"),
    mutationFn: (filters: PeopleFilters) =>
      adminV2Api<{ selection: PeopleSelection }>("/organization/people/selections", {
        method: "POST",
        body: JSON.stringify(canonicalSelectionFilter(filters)),
      }).then((result) => result.selection),
  });
}

export function useUpdateProfileGroup(contextKey: AdminContextKey) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: adminV2QueryKey(contextKey, "organization", "people", "profile-group"),
    mutationFn: ({
      accountId,
      profileId,
      expectedRevision,
      groupId,
    }: {
      accountId: number;
      profileId: string;
      expectedRevision: number;
      groupId: number;
    }) =>
      adminV2Api<{ person: OrganizationPerson }>(
        `/organization/people/${accountId}/profiles/${encodeURIComponent(profileId)}`,
        {
          method: "PATCH",
          body: JSON.stringify({
            expected_revision: expectedRevision,
            group_id: groupId,
          }),
        },
      ),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: organizationPeopleKeys.peopleRoot(contextKey),
      });
    },
  });
}

export function useRefreshOrganizationPerson(contextKey: AdminContextKey) {
  const queryClient = useQueryClient();
  return async (accountId: number): Promise<OrganizationPerson> => {
    const person = await adminV2Api<{ person: OrganizationPerson }>(
      `/organization/people/${accountId}`,
    ).then((result) => result.person);
    queryClient.setQueriesData<PeoplePageData>(
      { queryKey: organizationPeopleKeys.peopleRoot(contextKey) },
      (page) =>
        page
          ? {
              ...page,
              items: page.items.map((item) => (item.account_id === accountId ? person : item)),
            }
          : page,
    );
    return person;
  };
}

export function useCreatePeopleBulkJob(contextKey: AdminContextKey) {
  return useMutation({
    mutationKey: adminV2QueryKey(contextKey, "organization", "people", "bulk-job"),
    mutationFn: (input: {
      selectionToken: string;
      kind: "assign_group" | "suspend_memberships" | "reactivate_memberships";
      groupId?: number;
    }) =>
      adminV2Api<{ job: PeopleBulkJob }>("/organization/people/bulk-jobs", {
        method: "POST",
        body: JSON.stringify({
          selection_token: input.selectionToken,
          kind: input.kind,
          ...(input.groupId ? { group_id: input.groupId } : {}),
        }),
      }).then((result) => result.job),
  });
}

export function usePeopleBulkJob(contextKey: AdminContextKey, jobId?: string) {
  return useQuery({
    queryKey: organizationPeopleKeys.job(contextKey, jobId ?? "none"),
    queryFn: () =>
      adminV2Api<{ job: PeopleBulkJob }>(
        `/organization/people/bulk-jobs/${encodeURIComponent(jobId ?? "")}`,
      ).then((result) => result.job),
    enabled: Boolean(jobId),
    refetchInterval: (query) => {
      const status = (query.state.data as PeopleBulkJob | undefined)?.status;
      return status === "queued" || status === "running" ? 1000 : false;
    },
  });
}
