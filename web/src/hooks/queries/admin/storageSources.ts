import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";
import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";
import type { PluginConfigSchema } from "@/api/types";

/** A storage source an ebook library can read its books from. */
export interface StorageSource {
  source_key: string;
  owner_kind: "platform" | "organization";
  installation_id: number | null;
  plugin_id: string;
  provider_source_id: string;
  root_entry_id: string;
  configuration_revision: number;
  enabled: boolean;
  state: string;
  configured: boolean;
  /** Non-secret, display-safe facts about the connection (bucket, region, endpoint, ...). */
  connection_summary: Record<string, string>;
}

/** A verified storage plugin release available for installation. */
export interface StorageArtifact {
  artifact_key: string;
  plugin_id: string;
  version: string;
  os: string;
  arch: string;
  /** True when the release comes from the verified catalog rather than an upload. */
  catalog: boolean;
  /** The configuration sections the plugin asks an administrator to fill in. */
  config_schema: PluginConfigSchema[];
}

interface StorageSourcePage {
  sources: StorageSource[];
  next_after: string | null;
}

export function useStorageScope() {
  const active = useOptionalAdminContext()?.active ?? null;
  return { active, scope: active?.scope ?? "platform", key: active?.key ?? "platform" };
}

/**
 * The storage sources visible in the active administrative context. Only
 * enabled, configured sources can back a new library. Outside an
 * administrative context (the setup wizard) there are none.
 */
export function useStorageSources(enabled: boolean) {
  const { active, scope, key } = useStorageScope();
  return useQuery({
    queryKey: adminV2QueryKey(key, "native-storage", "sources"),
    queryFn: () =>
      adminV2Api<StorageSourcePage>(`/${scope}/native-storage/sources?limit=100`).then(
        (page) => page.sources,
      ),
    enabled: enabled && active !== null,
    staleTime: 30_000,
  });
}

export function useStorageArtifacts(enabled: boolean) {
  const { active, scope, key } = useStorageScope();
  return useQuery({
    queryKey: adminV2QueryKey(key, "native-storage", "artifacts"),
    queryFn: () =>
      adminV2Api<{ artifacts: StorageArtifact[] }>(`/${scope}/native-storage/artifacts`).then(
        (page) => page.artifacts,
      ),
    enabled: enabled && active !== null,
    staleTime: 60_000,
  });
}

/** The two-part upload every storage plugin installation and upgrade takes. */
function storageUpload(request: object, binary: File): FormData {
  const body = new FormData();
  body.append("request", JSON.stringify(request));
  body.append("binary", binary);
  return body;
}

export interface InstallStorageSourceInput {
  artifactKey: string;
  providerSourceId: string;
  rootEntryId: string;
  config: Record<string, Record<string, unknown>>;
  binary?: File;
}

export function useInstallStorageSource() {
  const queryClient = useQueryClient();
  const { scope, key } = useStorageScope();
  return useMutation({
    // Variables can hold credentials; keep them out of the mutation cache.
    gcTime: 0,
    mutationFn: (input: InstallStorageSourceInput) =>
      adminV2Api<{ source: StorageSource }>(
        `/${scope}/native-storage/${input.binary ? "installations" : "catalog/installations"}`,
        {
          method: "POST",
          body: input.binary
            ? storageUpload(
                {
                  artifact_key: input.artifactKey,
                  provider_source_id: input.providerSourceId,
                  root_entry_id: input.rootEntryId,
                  enabled: true,
                  config: input.config,
                },
                input.binary,
              )
            : JSON.stringify({
                artifact_key: input.artifactKey,
                provider_source_id: input.providerSourceId,
                root_entry_id: input.rootEntryId,
                enabled: true,
                config: input.config,
              }),
        },
      ),
    onSuccess: () => {
      toast.success("Storage source added");
      void queryClient.invalidateQueries({ queryKey: adminV2QueryKey(key, "native-storage") });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Install failed"),
  });
}

export interface UpgradeStorageSourceInput {
  source: StorageSource;
  artifactKey: string;
  binary?: File;
}

/** Replaces a source's plugin with another approved release, keeping its configuration and library. */
export function useUpgradeStorageSource() {
  const queryClient = useQueryClient();
  const { scope, key } = useStorageScope();
  return useMutation({
    mutationFn: ({ source, artifactKey, binary }: UpgradeStorageSourceInput) =>
      adminV2Api<{ source: StorageSource }>(
        `/${scope}/native-storage/${binary ? "installations" : "catalog/installations"}/${source.installation_id}/upgrade`,
        {
          method: "POST",
          body: binary
            ? storageUpload(
                {
                  artifact_key: artifactKey,
                  source_key: source.source_key,
                  expected_revision: source.configuration_revision,
                },
                binary,
              )
            : JSON.stringify({
                artifact_key: artifactKey,
                source_key: source.source_key,
                expected_revision: source.configuration_revision,
              }),
        },
      ),
    onSuccess: () => {
      toast.success("Storage plugin upgraded");
      void queryClient.invalidateQueries({ queryKey: adminV2QueryKey(key, "native-storage") });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Upgrade failed"),
  });
}

type StorageConfig = Record<string, Record<string, unknown>>;

/** The catalog discovery request: no state path and no owner, the server decides both. */
export function discoverBody(input: {
  artifactKey: string;
  providerSourceId: string;
  config: StorageConfig;
}) {
  return {
    artifact_key: input.artifactKey,
    provider_source_id: input.providerSourceId,
    enabled: true,
    config: input.config,
  };
}

/** A credential rotation request: the revision guard and the new keys, nothing else. */
export function rotateBody(input: {
  revision: number;
  accessKey: string;
  secretKey: string;
  sessionToken: string;
}) {
  return {
    expected_revision: input.revision,
    access_key: input.accessKey,
    secret_key: input.secretKey,
    ...(input.sessionToken.trim() ? { session_token: input.sessionToken } : {}),
  };
}

function sourcePath(source: StorageSource): string {
  return `native-storage/sources/${encodeURIComponent(source.source_key)}`;
}

function installationPath(source: StorageSource): string {
  if (source.installation_id === null) {
    throw new Error("This storage source has no plugin installation to change.");
  }
  return `native-storage/installations/${source.installation_id}`;
}

/** The revision-guarded body disable and remove share. */
function installationChangeBody(source: StorageSource): string {
  return JSON.stringify({
    source_key: source.source_key,
    expected_revision: source.configuration_revision,
  });
}

/*
 * Error handling for every mutation below: a failed request rejects with the
 * `AdminV2ClientError` adminV2Api throws, so `mutation.error.code` carries the
 * server's machine-readable error code (for example
 * `configuration_namespace_unverified`) alongside `.status` and `.message`.
 * Request bodies hold secrets for install, discover, replace, rotate and
 * configure, so none of them are put in query keys, toasts or logs; toasts show
 * only the server's message. Those mutations also set `gcTime: 0`: React Query
 * keeps a mutation's `variables` in the MutationCache for `gcTime` after its last
 * observer unmounts (5 minutes by default), which would retain the secrets.
 */

export interface DiscoverStorageSourceInput {
  artifactKey: string;
  providerSourceId: string;
  config: StorageConfig;
}

/** Adds a source from the verified catalog and discovers its root; the server picks the root entry. */
export function useDiscoverStorageSource() {
  const queryClient = useQueryClient();
  const { scope, key } = useStorageScope();
  return useMutation({
    // Variables can hold credentials; keep them out of the mutation cache.
    gcTime: 0,
    mutationFn: (input: DiscoverStorageSourceInput) =>
      adminV2Api<{ source: StorageSource }>(
        `/${scope}/native-storage/catalog/installations/discover`,
        {
          method: "POST",
          body: JSON.stringify(discoverBody(input)),
        },
      ),
    onSuccess: () => {
      toast.success("Storage source added");
      void queryClient.invalidateQueries({
        queryKey: adminV2QueryKey(key, "native-storage", "sources"),
      });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Install failed"),
  });
}

export interface ReplaceStorageConfigurationInput {
  source: StorageSource;
  config: StorageConfig;
}

/** Replaces a source's whole configuration, guarded by the revision the editor loaded. */
export function useReplaceStorageConfiguration() {
  const queryClient = useQueryClient();
  const { scope, key } = useStorageScope();
  return useMutation({
    // Variables can hold credentials; keep them out of the mutation cache.
    gcTime: 0,
    mutationFn: ({ source, config }: ReplaceStorageConfigurationInput) =>
      adminV2Api<{ source: StorageSource }>(`/${scope}/${sourcePath(source)}/configuration`, {
        method: "PUT",
        body: JSON.stringify({ expected_revision: source.configuration_revision, config }),
      }),
    onSuccess: () => {
      toast.success("Storage source updated");
      void queryClient.invalidateQueries({
        queryKey: adminV2QueryKey(key, "native-storage", "sources"),
      });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Update failed"),
  });
}

export interface RotateStorageCredentialsInput {
  source: StorageSource;
  accessKey: string;
  secretKey: string;
  sessionToken?: string;
}

/** Replaces only the access keys of an in-use source without touching the rest of its configuration. */
export function useRotateStorageCredentials() {
  const queryClient = useQueryClient();
  const { scope, key } = useStorageScope();
  return useMutation({
    // Variables can hold credentials; keep them out of the mutation cache.
    gcTime: 0,
    mutationFn: ({ source, accessKey, secretKey, sessionToken }: RotateStorageCredentialsInput) =>
      adminV2Api<{ source: StorageSource }>(`/${scope}/${sourcePath(source)}/credentials`, {
        method: "POST",
        body: JSON.stringify(
          rotateBody({
            revision: source.configuration_revision,
            accessKey,
            secretKey,
            sessionToken: sessionToken ?? "",
          }),
        ),
      }),
    onSuccess: () => {
      toast.success("Credentials rotated");
      void queryClient.invalidateQueries({
        queryKey: adminV2QueryKey(key, "native-storage", "sources"),
      });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Rotation failed"),
  });
}

/** Stops a source's plugin installation without removing its configuration. */
export function useDisableStorageSource() {
  const queryClient = useQueryClient();
  const { scope, key } = useStorageScope();
  return useMutation({
    mutationFn: ({ source }: { source: StorageSource }) =>
      adminV2Api<unknown>(`/${scope}/${installationPath(source)}/disable`, {
        method: "POST",
        body: installationChangeBody(source),
      }),
    onSuccess: () => {
      toast.success("Storage source disabled");
      void queryClient.invalidateQueries({
        queryKey: adminV2QueryKey(key, "native-storage", "sources"),
      });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Disable failed"),
  });
}

/** Removes a source's plugin installation, guarded by the revision the caller saw. */
export function useRemoveStorageSource() {
  const queryClient = useQueryClient();
  const { scope, key } = useStorageScope();
  return useMutation({
    mutationFn: ({ source }: { source: StorageSource }) =>
      adminV2Api<unknown>(`/${scope}/${installationPath(source)}`, {
        method: "DELETE",
        body: installationChangeBody(source),
      }),
    onSuccess: () => {
      toast.success("Storage source removed");
      void queryClient.invalidateQueries({
        queryKey: adminV2QueryKey(key, "native-storage", "sources"),
      });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Remove failed"),
  });
}

export interface StorageConnectionTest {
  ok: boolean;
  message: string;
}

/**
 * Checks a source by listing one entry of its root. Never rejects: a failure
 * resolves `{ok: false, message}` so the caller renders it inline, and no toast
 * is raised for a check the administrator asked for and can read.
 */
export function useTestStorageSource() {
  const { scope } = useStorageScope();
  return useMutation({
    mutationFn: async ({ source }: { source: StorageSource }): Promise<StorageConnectionTest> => {
      try {
        await adminV2Api<unknown>(`/${scope}/${sourcePath(source)}/browse?limit=1`);
        return { ok: true, message: "Connection works" };
      } catch (error) {
        return {
          ok: false,
          message: error instanceof Error && error.message ? error.message : "Connection failed",
        };
      }
    },
  });
}

export function storageSourceLabel(source: StorageSource): string {
  return source.plugin_id === "bloem.storage.bookwarehouse"
    ? "Bookwarehouse"
    : `${source.provider_source_id} (${source.plugin_id})`;
}

export function useConfigureStorageSource() {
  const queryClient = useQueryClient();
  const { scope, key } = useStorageScope();
  return useMutation({
    // Variables can hold credentials; keep them out of the mutation cache.
    gcTime: 0,
    mutationFn: ({ source, url, apiKey }: { source: StorageSource; url: string; apiKey: string }) =>
      adminV2Api(`/${scope}/native-storage/sources/${source.source_key}/configuration`, {
        method: "PUT",
        body: JSON.stringify({
          expected_revision: source.configuration_revision,
          config: {
            connection: {
              base_url: url,
              api_key: apiKey,
              source_id: source.provider_source_id,
              source_name: "Bookwarehouse",
            },
          },
        }),
      }),
    onSuccess: () => {
      toast.success("Bookwarehouse connected");
      void queryClient.invalidateQueries({ queryKey: adminV2QueryKey(key, "native-storage") });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Connection failed"),
  });
}
