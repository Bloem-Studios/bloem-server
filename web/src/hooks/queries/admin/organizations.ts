import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";

export type OrganizationStatus = "initializing" | "active" | "suspended";
export type MembershipStatus = "invited" | "active" | "suspended";

export interface PlatformOrganization {
  id: string;
  slug: string;
  name: string;
  status: OrganizationStatus;
  owner_account_id?: number;
  policy_revision: number;
  is_default?: boolean;
  membership_count?: number;
  active_membership_count?: number;
  profile_count?: number;
  library_count?: number;
  entitlement_count?: number;
}

export interface OrganizationMembership {
  id: string;
  organization_id: string;
  account_id: number;
  email: string;
  username: string;
  status: MembershipStatus;
  legacy_role: "admin" | "user";
  security_revision: number;
}

export interface OrganizationFilter {
  query?: string;
  status?: OrganizationStatus | "all";
  cursor?: string;
  limit?: number;
}

export interface OrganizationPage {
  organizations: PlatformOrganization[];
  next_cursor?: string;
}

export interface MembershipPage {
  memberships: OrganizationMembership[];
  next_cursor?: string;
}

const platformKey = adminV2QueryKey("platform");

export const organizationKeys = {
  all: platformKey,
  lists: () => [...platformKey, "organizations"] as const,
  list: (filter: OrganizationFilter) => [...platformKey, "organizations", filter] as const,
  detail: (id: string) => [...platformKey, "organizations", id] as const,
  memberships: (id: string, cursor = "") =>
    [...platformKey, "organizations", id, "memberships", cursor] as const,
  membershipPages: (id: string) => [...platformKey, "organizations", id, "memberships"] as const,
};

function organizationPath(id: string, suffix = ""): string {
  return `/platform/organizations/${encodeURIComponent(id)}${suffix}`;
}

function queryString(filter: OrganizationFilter): string {
  const params = new URLSearchParams();
  if (filter.query) params.set("query", filter.query);
  if (filter.status && filter.status !== "all") params.set("status", filter.status);
  if (filter.cursor) params.set("cursor", filter.cursor);
  params.set("limit", String(filter.limit ?? 50));
  return params.toString();
}

export function usePlatformOrganizations(filter: OrganizationFilter) {
  return useQuery({
    queryKey: organizationKeys.list(filter),
    queryFn: () => adminV2Api<OrganizationPage>(`/platform/organizations?${queryString(filter)}`),
  });
}

export function usePlatformOrganization(id: string) {
  return useQuery({
    queryKey: organizationKeys.detail(id),
    queryFn: () =>
      adminV2Api<{ organization: PlatformOrganization }>(organizationPath(id)).then(
        (result) => result.organization,
      ),
    enabled: Boolean(id),
  });
}

export function useOrganizationMemberships(id: string, cursor = "") {
  const params = new URLSearchParams();
  if (cursor) params.set("cursor", cursor);
  const search = params.size > 0 ? `?${params}` : "";
  return useQuery({
    queryKey: organizationKeys.memberships(id, cursor),
    queryFn: () => adminV2Api<MembershipPage>(`${organizationPath(id, "/memberships")}${search}`),
    enabled: Boolean(id),
  });
}

function usePlatformMutation<TInput, TResult>(
  mutationFn: (input: TInput) => Promise<TResult>,
  invalidation: (input: TInput) => readonly unknown[],
  additionalInvalidation?: (input: TInput) => readonly unknown[],
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    onSuccess: async (_result, input) => {
      await queryClient.invalidateQueries({ queryKey: invalidation(input) });
      if (additionalInvalidation) {
        await queryClient.invalidateQueries({ queryKey: additionalInvalidation(input) });
      }
      await queryClient.invalidateQueries({ queryKey: organizationKeys.lists() });
    },
  });
}

export function useCreateOrganization() {
  return usePlatformMutation(
    (input: { name: string; slug: string; owner_account_id: number }) =>
      adminV2Api<{ organization: PlatformOrganization }>("/platform/organizations", {
        method: "POST",
        body: JSON.stringify(input),
      }),
    () => organizationKeys.lists(),
  );
}

export function useUpdateOrganization(id: string) {
  return usePlatformMutation(
    (input: { expected_revision: number; name?: string; slug?: string }) =>
      adminV2Api<{ organization: PlatformOrganization }>(organizationPath(id), {
        method: "PATCH",
        body: JSON.stringify(input),
      }),
    () => organizationKeys.detail(id),
  );
}

export function useSetOrganizationStatus(id: string) {
  return usePlatformMutation(
    (input: { expected_revision: number; status: "active" | "suspended" }) =>
      adminV2Api<{ organization: PlatformOrganization }>(
        organizationPath(id, input.status === "suspended" ? "/suspend" : "/reactivate"),
        { method: "POST", body: JSON.stringify({ expected_revision: input.expected_revision }) },
      ),
    () => organizationKeys.detail(id),
  );
}

export function useTransferOrganizationOwnership(id: string) {
  return usePlatformMutation(
    (input: { expected_revision: number; owner_account_id: number; password: string }) =>
      adminV2Api<{ organization: PlatformOrganization }>(
        organizationPath(id, "/transfer-ownership"),
        {
          method: "POST",
          body: JSON.stringify({ ...input, confirmed: true }),
        },
      ),
    () => organizationKeys.detail(id),
  );
}

export function useCreateOrganizationMembership(id: string) {
  return usePlatformMutation(
    (input: {
      expected_revision: number;
      account_id: number;
      legacy_role: "admin" | "user";
      status: MembershipStatus;
    }) =>
      adminV2Api<{ membership: OrganizationMembership; organization: PlatformOrganization }>(
        organizationPath(id, "/memberships"),
        { method: "POST", body: JSON.stringify(input) },
      ),
    () => organizationKeys.detail(id),
    () => organizationKeys.membershipPages(id),
  );
}

export function useUpdateOrganizationMembership(id: string) {
  return usePlatformMutation(
    (input: {
      membership_id: string;
      expected_revision: number;
      legacy_role?: "admin" | "user";
      status?: MembershipStatus;
    }) => {
      const { membership_id, ...body } = input;
      return adminV2Api<{
        membership: OrganizationMembership;
        organization: PlatformOrganization;
      }>(organizationPath(id, `/memberships/${encodeURIComponent(membership_id)}`), {
        method: "PATCH",
        body: JSON.stringify(body),
      });
    },
    () => organizationKeys.detail(id),
    () => organizationKeys.membershipPages(id),
  );
}
