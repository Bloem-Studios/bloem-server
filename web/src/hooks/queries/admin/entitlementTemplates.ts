import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { EntitlementTemplate, EntitlementTemplateInput } from "@/api/types";
import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";

const platformKey = adminV2QueryKey("platform", "entitlement-templates");

export const entitlementTemplateKeys = {
  all: platformKey,
  list: (includeArchived: boolean) => [...platformKey, "list", { includeArchived }] as const,
  history: (key: string) => [...platformKey, "history", key] as const,
  organizationDetail: (organizationID: string) =>
    [...platformKey, "organization", organizationID] as const,
  accountDetail: (userID: string) => [...platformKey, "account", userID] as const,
};

interface TemplateListResponse {
  templates: EntitlementTemplate[];
}

interface TemplateResponse {
  template: EntitlementTemplate;
}

interface TemplateHistoryResponse {
  revisions: EntitlementTemplate[];
}

const templatesPath = "/platform/entitlement-templates";

function templatePath(key: string, suffix = "") {
  return `${templatesPath}/${encodeURIComponent(key)}${suffix}`;
}

export function useEntitlementTemplates(includeArchived = true) {
  const search = includeArchived ? "" : "?status=enabled";
  return useQuery({
    queryKey: entitlementTemplateKeys.list(includeArchived),
    queryFn: () =>
      adminV2Api<TemplateListResponse>(`${templatesPath}${search}`).then(
        (data) => data.templates ?? [],
      ),
  });
}

export function useEntitlementTemplateHistory(key: string | undefined) {
  return useQuery({
    queryKey: entitlementTemplateKeys.history(key ?? ""),
    queryFn: () =>
      adminV2Api<TemplateHistoryResponse>(templatePath(key!, "/history")).then(
        (data) => data.revisions ?? [],
      ),
    enabled: Boolean(key),
  });
}

function useTemplateMutation<TInput>(mutationFn: (input: TInput) => Promise<TemplateResponse>) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: entitlementTemplateKeys.all });
    },
  });
}

export function useCreateEntitlementTemplate() {
  return useTemplateMutation((input: EntitlementTemplateInput) =>
    adminV2Api<TemplateResponse>(templatesPath, { method: "POST", body: JSON.stringify(input) }),
  );
}

export function useReviseEntitlementTemplate() {
  return useTemplateMutation(
    ({
      key,
      expected_revision,
      input,
    }: {
      key: string;
      expected_revision: number;
      input: EntitlementTemplateInput;
    }) =>
      adminV2Api<TemplateResponse>(templatePath(key, "/revisions"), {
        method: "POST",
        body: JSON.stringify({
          expected_revision,
          name: input.name,
          enabled: input.enabled,
          policy: input.policy,
        }),
      }),
  );
}

export function useCloneEntitlementTemplate() {
  return useTemplateMutation(
    ({
      key,
      source_revision,
      newKey,
      name,
    }: {
      key: string;
      source_revision: number;
      newKey: string;
      name: string;
    }) =>
      adminV2Api<TemplateResponse>(templatePath(key, "/clone"), {
        method: "POST",
        body: JSON.stringify({ source_revision, key: newKey, name }),
      }),
  );
}

export function useRollbackEntitlementTemplate() {
  return useTemplateMutation(
    ({
      key,
      expected_revision,
      source_revision,
      name,
      enabled,
    }: {
      key: string;
      expected_revision: number;
      source_revision: number;
      name: string;
      enabled: boolean;
    }) =>
      adminV2Api<TemplateResponse>(templatePath(key, "/revisions"), {
        method: "POST",
        body: JSON.stringify({ expected_revision, source_revision, name, enabled }),
      }),
  );
}

export function useArchiveEntitlementTemplate() {
  return useTemplateMutation(
    ({ key, expected_revision }: { key: string; expected_revision: number }) =>
      adminV2Api<TemplateResponse>(templatePath(key, "/archive"), {
        method: "POST",
        body: JSON.stringify({ expected_revision }),
      }),
  );
}

export interface EntitlementDryRun {
  template_key: string;
  template_revision: number;
  changed: boolean;
  dry_run_token: string;
  expires_at: string;
  changes: Array<{ field: string; before: unknown; after: unknown }>;
  warnings: string[];
}

export interface OrganizationEntitlementDetail {
  template_key: string | null;
  template_revision: number | null;
  managed_default_group: {
    id: string;
    name: string;
    policy: EntitlementTemplate["policy"];
  } | null;
  tenant_limits: { slots: number; transcodes: number };
  library_ids: number[] | null;
  last_reconciled_at: string | null;
  audit_history_href: string | null;
}

export interface AccountEntitlementDetail {
  template_key: string | null;
  template_revision: number | null;
  managed_default_group: {
    id: string;
    name: string;
    policy: EntitlementTemplate["policy"];
  } | null;
  library_ids: number[] | null;
  last_reconciled_at: string | null;
}

function tenantEntitlementPath(organizationID: string, suffix: "" | "/dry-run" | "/apply" = "") {
  return `/platform/organizations/${encodeURIComponent(organizationID)}/entitlement${suffix}`;
}

export function useOrganizationEntitlement(organizationID: string) {
  return useQuery({
    queryKey: entitlementTemplateKeys.organizationDetail(organizationID),
    queryFn: () => adminV2Api<OrganizationEntitlementDetail>(tenantEntitlementPath(organizationID)),
    enabled: Boolean(organizationID),
  });
}

export function useEntitlementDryRun(organizationID: string) {
  return useMutation({
    mutationFn: (input: { template_key: string; template_revision: number }) =>
      adminV2Api<EntitlementDryRun>(tenantEntitlementPath(organizationID, "/dry-run"), {
        method: "POST",
        body: JSON.stringify(input),
      }),
  });
}

export function useApplyTenantEntitlement(organizationID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: {
      template_key: string;
      template_revision: number;
      dry_run_token: string;
      idempotency_key: string;
    }) =>
      adminV2Api<{ template_key: string; template_revision: number; changed: boolean }>(
        tenantEntitlementPath(organizationID, "/apply"),
        { method: "POST", body: JSON.stringify(input) },
      ),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: adminV2QueryKey("platform", "organizations", organizationID),
        }),
        queryClient.invalidateQueries({
          queryKey: entitlementTemplateKeys.organizationDetail(organizationID),
        }),
      ]);
    },
  });
}

function accountEntitlementPath(userID: string, suffix: "" | "/dry-run" | "/apply" = "") {
  return `/platform/users/${encodeURIComponent(userID)}/entitlement${suffix}`;
}

export function useAccountEntitlement(userID: string) {
  return useQuery({
    queryKey: entitlementTemplateKeys.accountDetail(userID),
    queryFn: () => adminV2Api<AccountEntitlementDetail>(accountEntitlementPath(userID)),
    enabled: Boolean(userID),
  });
}

export function useAccountEntitlementDryRun(userID: string) {
  return useMutation({
    mutationFn: (input: { template_key: string; template_revision: number }) =>
      adminV2Api<EntitlementDryRun>(accountEntitlementPath(userID, "/dry-run"), {
        method: "POST",
        body: JSON.stringify(input),
      }),
  });
}

export function useApplyAccountEntitlement(userID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: {
      template_key: string;
      template_revision: number;
      dry_run_token: string;
      idempotency_key: string;
    }) =>
      adminV2Api<{ template_key: string; template_revision: number; changed: boolean }>(
        accountEntitlementPath(userID, "/apply"),
        { method: "POST", body: JSON.stringify(input) },
      ),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: entitlementTemplateKeys.accountDetail(userID),
      });
    },
  });
}
