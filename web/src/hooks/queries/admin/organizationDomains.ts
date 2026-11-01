import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";

// Bloem: an organization's domains and branding, on the platform routes under
// /api/bloem/v1/admin/platform/tenants/{id}.

export interface OrganizationDomain {
  host: string;
  organization_id: string;
  primary: boolean;
  source: "platform" | "garden";
  created_at: string;
}

export interface OrganizationBranding {
  server_name: string;
  login_subtitle: string;
  accent_color: string;
  default_theme: string;
  /** Uploaded asset kind to its reference. */
  assets: Record<string, string>;
}

export type OrganizationBrandingUpdate = Partial<
  Pick<OrganizationBranding, "server_name" | "login_subtitle" | "accent_color" | "default_theme">
>;

export const ORGANIZATION_BRANDING_ASSETS = [
  { kind: "wordmark", label: "Wordmark" },
  { kind: "mark", label: "Mark" },
  { kind: "favicon", label: "Favicon" },
  { kind: "login_bg", label: "Sign-in background" },
] as const;

const platformKey = adminV2QueryKey("platform");

export const organizationDomainKeys = {
  domains: (id: string) => [...platformKey, "organizations", id, "domains"] as const,
  branding: (id: string) => [...platformKey, "organizations", id, "branding"] as const,
};

function tenantPath(id: string, suffix: string): string {
  return `/platform/tenants/${encodeURIComponent(id)}${suffix}`;
}

export function useOrganizationDomains(id: string) {
  return useQuery({
    queryKey: organizationDomainKeys.domains(id),
    queryFn: () =>
      adminV2Api<{ domains?: OrganizationDomain[] }>(tenantPath(id, "/domains")).then(
        (response) => response?.domains ?? [],
      ),
    enabled: Boolean(id),
  });
}

function useDomainsMutation<TInput, TResult>(
  id: string,
  mutationFn: (input: TInput) => Promise<TResult>,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: organizationDomainKeys.domains(id) });
    },
  });
}

export function useAddOrganizationDomain(id: string) {
  return useDomainsMutation(id, (input: { host: string; primary: boolean }) =>
    adminV2Api<{ domain: OrganizationDomain }>(tenantPath(id, "/domains"), {
      method: "POST",
      body: JSON.stringify(input),
    }),
  );
}

export function useSetPrimaryOrganizationDomain(id: string) {
  return useDomainsMutation(id, (host: string) =>
    adminV2Api<void>(tenantPath(id, `/domains/${encodeURIComponent(host)}/primary`), {
      method: "PUT",
    }),
  );
}

export function useRemoveOrganizationDomain(id: string) {
  return useDomainsMutation(id, (host: string) =>
    adminV2Api<void>(tenantPath(id, `/domains/${encodeURIComponent(host)}`), { method: "DELETE" }),
  );
}

export function useOrganizationBranding(id: string) {
  return useQuery({
    queryKey: organizationDomainKeys.branding(id),
    queryFn: () => adminV2Api<OrganizationBranding>(tenantPath(id, "/branding")),
    enabled: Boolean(id),
    retry: false,
  });
}

function useBrandingMutation<TInput, TResult>(
  id: string,
  mutationFn: (input: TInput) => Promise<TResult>,
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: organizationDomainKeys.branding(id) });
    },
  });
}

export function useUpdateOrganizationBranding(id: string) {
  return useBrandingMutation(id, (input: OrganizationBrandingUpdate) =>
    adminV2Api<OrganizationBranding>(tenantPath(id, "/branding"), {
      method: "PUT",
      body: JSON.stringify(input),
    }),
  );
}

export function useUploadOrganizationBrandingAsset(id: string) {
  return useBrandingMutation(id, ({ kind, file }: { kind: string; file: File }) => {
    const body = new FormData();
    body.append("file", file);
    return adminV2Api<{ kind: string; ref: string }>(
      tenantPath(id, `/branding/assets/${encodeURIComponent(kind)}`),
      { method: "PUT", body },
    );
  });
}

export function useDeleteOrganizationBrandingAsset(id: string) {
  return useBrandingMutation(id, (kind: string) =>
    adminV2Api<void>(tenantPath(id, `/branding/assets/${encodeURIComponent(kind)}`), {
      method: "DELETE",
    }),
  );
}
