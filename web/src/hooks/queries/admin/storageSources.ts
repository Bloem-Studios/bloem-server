import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";
import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";

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
}

/** A storage plugin release the server operator approved for installation. */
export interface StorageArtifact {
  artifact_key: string;
  plugin_id: string;
  version: string;
  os: string;
  arch: string;
}

interface StorageSourcePage {
  sources: StorageSource[];
  next_after: string | null;
}

function useStorageScope() {
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
  binary: File;
}

export function useInstallStorageSource() {
  const queryClient = useQueryClient();
  const { scope, key } = useStorageScope();
  return useMutation({
    mutationFn: (input: InstallStorageSourceInput) =>
      adminV2Api<{ source: StorageSource }>(`/${scope}/native-storage/installations`, {
        method: "POST",
        body: storageUpload(
          {
            artifact_key: input.artifactKey,
            provider_source_id: input.providerSourceId,
            root_entry_id: input.rootEntryId,
            enabled: true,
            config: input.config,
          },
          input.binary,
        ),
      }),
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
  binary: File;
}

/** Replaces a source's plugin with another approved release, keeping its configuration and library. */
export function useUpgradeStorageSource() {
  const queryClient = useQueryClient();
  const { scope, key } = useStorageScope();
  return useMutation({
    mutationFn: ({ source, artifactKey, binary }: UpgradeStorageSourceInput) =>
      adminV2Api<{ source: StorageSource }>(
        `/${scope}/native-storage/installations/${source.installation_id}/upgrade`,
        {
          method: "POST",
          body: storageUpload(
            {
              artifact_key: artifactKey,
              source_key: source.source_key,
              expected_revision: source.configuration_revision,
            },
            binary,
          ),
        },
      ),
    onSuccess: () => {
      toast.success("Storage plugin upgraded");
      void queryClient.invalidateQueries({ queryKey: adminV2QueryKey(key, "native-storage") });
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Upgrade failed"),
  });
}

export function storageSourceLabel(source: StorageSource): string {
  return `${source.provider_source_id} (${source.plugin_id})`;
}
