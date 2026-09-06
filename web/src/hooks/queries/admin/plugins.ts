import { v2, V2ProblemError } from "@/api/v2/request";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useState } from "react";
import { toast } from "sonner";

import {
  api,
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
  type ProfileRequestContextSnapshot,
} from "@/api/client";
import type {
  ConnectionCheckResponse,
  CreatePluginRepositoryRequest,
  InstallPluginRequest,
  PluginCatalogEntry,
  PluginCatalogSettings,
  PluginInstallation,
  PluginRepository,
  PluginTaskBindingUpdateResponse,
  SavePluginAuthBindingRequest,
  SavePluginConfigRequest,
  SavePluginTaskBindingRequest,
  UpdatePluginInstallationRequest,
  UpdatePluginCatalogSettingsRequest,
  UpdatePluginRepositoryRequest,
} from "@/api/types";
import {
  DEFAULT_UPLOAD_CHUNK_SIZE,
  type ChunkedUploadProgress,
  uploadFileInChunks,
} from "@/lib/chunkedUpload";
import { adminKeys } from "../keys";

const ADMIN_STALE_TIME = 30_000;
export const CHECK_PLUGIN_UPDATES_TASK_KEY = "check_plugin_updates";

function invalidatePluginQueries(queryClient: ReturnType<typeof useQueryClient>) {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: adminKeys.pluginRepositories() }),
    queryClient.invalidateQueries({ queryKey: adminKeys.pluginCatalog() }),
    queryClient.invalidateQueries({ queryKey: adminKeys.pluginInstallations() }),
    queryClient.invalidateQueries({ queryKey: adminKeys.pluginCatalogSettings() }),
  ]);
}

// useAdminPluginInstallations is a slim hook for callers (e.g. AdminSidebar)
// that only need the installations list. Shares its cache key with
// useAdminPlugins() so triggering a refetch in either keeps both in sync.
export function useAdminPluginInstallations() {
  return useQuery({
    queryKey: adminKeys.pluginInstallations(),
    queryFn: () =>
      api<PluginInstallation[]>("/admin/plugins/installations").then((data) => data ?? []),
    staleTime: ADMIN_STALE_TIME,
  });
}

export function useAdminPluginRepositories() {
  const profileContext = captureProfileRequestContext();
  return useQuery({
    queryKey: [
      ...adminKeys.pluginRepositories(),
      profileContext?.serverOrigin,
      profileContext?.authContextVersion,
      profileContext?.profileId,
      profileContext?.profileTokenGeneration,
    ],
    queryFn: async (): Promise<PluginRepository[]> => {
      if (!profileContext || !isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      const repositories: PluginRepository[] = [];
      const cursors = new Set<string>();
      const ids = new Set<string>();
      let cursor: string | undefined;
      for (let pageNumber = 0; pageNumber < 100; pageNumber++) {
        const page = await v2("GET /api/v2/admin/plugins/repositories", {
          query: { limit: 100, cursor },
          profileContext,
        });
        if (!isCapturedProfileAuthorityActive(profileContext))
          throw new StaleApiRequestContextError();
        for (const row of page.items) {
          const id = Number(row.id);
          if (!/^[1-9][0-9]*$/.test(row.id) || !Number.isSafeInteger(id) || ids.has(row.id))
            throw new Error("Invalid repository identifier in response.");
          if (
            row.source_kind !== "silo" &&
            row.source_kind !== "approved_community" &&
            row.source_kind !== "external"
          )
            throw new Error("Unrecognized repository source kind.");
          ids.add(row.id);
          repositories.push({ ...row, id, source_kind: row.source_kind });
        }
        if (!page.page) throw new Error("Missing repository pagination metadata.");
        if (!page.page.has_more) return repositories;
        const next = page.page.next_cursor;
        if (!next || cursors.has(next)) throw new Error("Invalid repository continuation.");
        cursors.add(next);
        cursor = next;
      }
      throw new Error("Repository list exceeds this client's page limit.");
    },
    staleTime: ADMIN_STALE_TIME,
    enabled: profileContext !== null,
  });
}

export function useAdminPlugins() {
  const repositoriesQuery = useAdminPluginRepositories();

  const catalogQuery = useQuery({
    queryKey: adminKeys.pluginCatalog(),
    queryFn: () => api<PluginCatalogEntry[]>("/admin/plugins/catalog").then((data) => data ?? []),
    staleTime: ADMIN_STALE_TIME,
  });

  const installationsQuery = useQuery({
    queryKey: adminKeys.pluginInstallations(),
    queryFn: () =>
      api<PluginInstallation[]>("/admin/plugins/installations").then((data) => data ?? []),
    staleTime: ADMIN_STALE_TIME,
  });

  const catalogSettingsQuery = useQuery({
    queryKey: adminKeys.pluginCatalogSettings(),
    queryFn: fetchPluginCatalogSettings,
    staleTime: ADMIN_STALE_TIME,
  });

  return {
    repositories: repositoriesQuery.data ?? [],
    repositoriesError: repositoriesQuery.error,
    catalog: catalogQuery.data ?? [],
    installations: installationsQuery.data ?? [],
    catalogSettings: catalogSettingsQuery.data,
    isLoading:
      repositoriesQuery.isLoading ||
      catalogQuery.isLoading ||
      installationsQuery.isLoading ||
      catalogSettingsQuery.isLoading,
    isFetching:
      repositoriesQuery.isFetching ||
      catalogQuery.isFetching ||
      installationsQuery.isFetching ||
      catalogSettingsQuery.isFetching,
  };
}

export type PluginCatalogSettingsView = PluginCatalogSettings & { etag: string };
type PluginCatalogSettingsUpdate = UpdatePluginCatalogSettingsRequest & { etag: string };
type PluginCatalogSettingsIntent = PluginCatalogSettingsUpdate & {
  profileContext: ProfileRequestContextSnapshot;
};

export async function fetchPluginCatalogSettings(): Promise<PluginCatalogSettingsView> {
  const profileContext = captureProfileRequestContext();
  if (!profileContext) throw new StaleApiRequestContextError();
  let etag = "";
  const [settings, status] = await Promise.all([
    v2("GET /api/v2/admin/plugins/catalog-settings", {
      profileContext,
      onResponse: (response) => {
        etag = response.headers.get("ETag") ?? "";
      },
    }),
    v2("GET /api/v2/admin/plugins/catalog-status", { profileContext }),
  ]);
  if (!isCapturedProfileAuthorityActive(profileContext)) throw new StaleApiRequestContextError();
  if (!etag || etag === "*" || etag.startsWith("W/"))
    throw new Error("Catalog revision unavailable. Reload before editing.");
  return {
    ...status,
    ...settings,
    etag,
    community_updates_paused:
      !settings.include_approved_community_plugins && status.installed_community_plugin_count > 0,
  };
}

export function useUpdatePluginCatalogSettings() {
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: ({ etag, profileContext, ...body }: PluginCatalogSettingsIntent) => {
      if (!etag || etag === "*" || etag.startsWith("W/"))
        throw new Error("Reload plugin catalog settings before editing.");
      return v2("PUT /api/v2/admin/plugins/catalog-settings", {
        body,
        profileContext,
        headers: { "If-Match": etag },
        retryAuthentication: false,
      });
    },
    retry: false,
    onSuccess: (_result, intent) => {
      if (!isCapturedProfileAuthorityActive(intent.profileContext)) return;
      toast.success("Plugin catalog settings updated");
      invalidatePluginQueries(queryClient);
    },
    onError: (error) => {
      toast.error(
        error instanceof V2ProblemError && error.status === 412
          ? "Plugin catalog settings changed. Reload and review them before submitting another edit."
          : error instanceof Error
            ? error.message
            : "Failed to update plugin catalog settings",
      );
    },
  });
  return {
    ...mutation,
    mutate: (values: PluginCatalogSettingsUpdate) => {
      const profileContext = captureProfileRequestContext();
      if (!profileContext) {
        toast.error("Select an administrator profile before editing.");
        return;
      }
      mutation.mutate({ ...values, profileContext });
    },
  };
}

type PluginRepositoryCreationIntent = {
  body: CreatePluginRepositoryRequest;
  profileContext: ProfileRequestContextSnapshot;
};
function captureRepositoryCreation(
  body: CreatePluginRepositoryRequest,
): PluginRepositoryCreationIntent {
  const profileContext = captureProfileRequestContext();
  if (!profileContext) throw new StaleApiRequestContextError();
  return { body: { ...body }, profileContext };
}
export function useCreatePluginRepository() {
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: async ({ body, profileContext }: PluginRepositoryCreationIntent) => {
      if (!isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      const result = await v2("POST /api/v2/admin/plugins/repositories", {
        body,
        profileContext,
        retryAuthentication: false,
      });
      if (!isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      return result;
    },
    retry: false,
    onSuccess: (_result, intent) => {
      if (!isCapturedProfileAuthorityActive(intent.profileContext)) return;
      toast.success("Repository added");
      invalidatePluginQueries(queryClient);
    },
    onError: (error, intent) => {
      if (!isCapturedProfileAuthorityActive(intent.profileContext)) return;
      toast.error(
        error instanceof V2ProblemError && error.status === 422
          ? error.message
          : "Repository creation could not be confirmed. Refresh repositories before submitting again.",
      );
    },
  });
  return {
    ...mutation,
    mutate: (body: CreatePluginRepositoryRequest) => {
      try {
        mutation.mutate(captureRepositoryCreation(body));
      } catch {
        toast.error("Select an administrator profile before adding a repository.");
      }
    },
    mutateAsync: (body: CreatePluginRepositoryRequest) =>
      mutation.mutateAsync(captureRepositoryCreation(body)),
  };
}

type PluginRepositoryUpdateIntent = {
  id: number;
  body: UpdatePluginRepositoryRequest;
  profileContext: ProfileRequestContextSnapshot;
};
function captureRepositoryUpdate(input: {
  id: number;
  body: UpdatePluginRepositoryRequest;
}): PluginRepositoryUpdateIntent {
  if (!Number.isSafeInteger(input.id) || input.id <= 0) throw new Error("Invalid repository ID");
  const profileContext = captureProfileRequestContext();
  if (!profileContext) throw new StaleApiRequestContextError();
  return { id: input.id, body: { ...input.body }, profileContext };
}
export function useUpdatePluginRepository() {
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: async ({ id, body, profileContext }: PluginRepositoryUpdateIntent) => {
      if (!isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      const result = await v2("PUT /api/v2/admin/plugins/repositories/{id}", {
        path: { id: String(id) },
        body,
        profileContext,
        retryAuthentication: false,
      });
      if (!isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      return result;
    },
    retry: false,
    onSuccess: (_result, intent) => {
      if (!isCapturedProfileAuthorityActive(intent.profileContext)) return;
      toast.success("Repository updated");
      invalidatePluginQueries(queryClient);
    },
    onError: (error, intent) => {
      if (!isCapturedProfileAuthorityActive(intent.profileContext)) return;
      toast.error(
        error instanceof V2ProblemError && error.status === 422
          ? error.message
          : "Repository update could not be confirmed. Refresh repositories before submitting again.",
      );
    },
  });
  return {
    ...mutation,
    mutate: (input: { id: number; body: UpdatePluginRepositoryRequest }) => {
      try {
        mutation.mutate(captureRepositoryUpdate(input));
      } catch {
        toast.error("Select an administrator profile before updating a repository.");
      }
    },
    mutateAsync: (input: { id: number; body: UpdatePluginRepositoryRequest }) =>
      mutation.mutateAsync(captureRepositoryUpdate(input)),
  };
}

type PluginRepositoryDeletionIntent = { id: number; profileContext: ProfileRequestContextSnapshot };
function captureRepositoryDeletion(id: number): PluginRepositoryDeletionIntent {
  if (!Number.isSafeInteger(id) || id <= 0) throw new Error("Invalid repository ID");
  const profileContext = captureProfileRequestContext();
  if (!profileContext) throw new StaleApiRequestContextError();
  return { id, profileContext };
}
export function useDeletePluginRepository() {
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: async ({ id, profileContext }: PluginRepositoryDeletionIntent) => {
      if (!isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      await v2("DELETE /api/v2/admin/plugins/repositories/{id}", {
        path: { id: String(id) },
        profileContext,
        retryAuthentication: false,
      });
      if (!isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
    },
    retry: false,
    onSuccess: (_result, intent) => {
      if (!isCapturedProfileAuthorityActive(intent.profileContext)) return;
      toast.success("Repository removed");
      invalidatePluginQueries(queryClient);
    },
    onError: (error, intent) => {
      if (!isCapturedProfileAuthorityActive(intent.profileContext)) return;
      toast.error(
        error instanceof V2ProblemError && error.status === 422
          ? error.message
          : "Repository deletion could not be confirmed. Refresh repositories before submitting again.",
      );
    },
  });
  return {
    ...mutation,
    mutate: (id: number) => {
      try {
        mutation.mutate(captureRepositoryDeletion(id));
      } catch {
        toast.error("Select an administrator profile before deleting a repository.");
      }
    },
    mutateAsync: (id: number) => mutation.mutateAsync(captureRepositoryDeletion(id)),
  };
}

export function useInstallPlugin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: InstallPluginRequest) =>
      api<PluginInstallation>("/admin/plugins/installations", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      toast.success("Plugin installed");
      invalidatePluginQueries(queryClient);
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to install plugin");
    },
  });
}

export interface UploadPluginRequest {
  file: File;
  onProgress?: (progress: ChunkedUploadProgress) => void;
}

export function useUploadPlugin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ file, onProgress }: UploadPluginRequest) => {
      if (file.size > DEFAULT_UPLOAD_CHUNK_SIZE) {
        return uploadFileInChunks<PluginInstallation>({
          file,
          createPath: "/admin/plugins/uploads/chunked",
          chunkPath: (uploadId, chunkIndex) =>
            `/admin/plugins/uploads/chunked/${encodeURIComponent(uploadId)}/chunks/${chunkIndex}`,
          completePath: (uploadId) =>
            `/admin/plugins/uploads/chunked/${encodeURIComponent(uploadId)}/complete`,
          cancelPath: (uploadId) =>
            `/admin/plugins/uploads/chunked/${encodeURIComponent(uploadId)}`,
          onProgress,
        });
      }

      const formData = new FormData();
      formData.append("archive", file);
      return api<PluginInstallation>("/admin/plugins/uploads", {
        method: "POST",
        body: formData,
      });
    },
    onSuccess: () => {
      toast.success("Plugin uploaded");
      invalidatePluginQueries(queryClient);
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to upload plugin");
    },
  });
}

/**
 * Wraps {@link useUploadPlugin} with the upload-progress state and reset wiring
 * shared by the admin plugin upload forms.
 */
export function usePluginUpload() {
  const uploadPlugin = useUploadPlugin();
  const [progress, setProgress] = useState<number | null>(null);

  const upload = useCallback(
    (file: File, options?: { onSuccess?: () => void }) => {
      setProgress(0);
      uploadPlugin.mutate(
        { file, onProgress: (next) => setProgress(next.percent) },
        {
          onSuccess: () => {
            setProgress(null);
            options?.onSuccess?.();
          },
          onError: () => setProgress(null),
        },
      );
    },
    [uploadPlugin],
  );

  return { upload, progress, isPending: uploadPlugin.isPending };
}

export function useUpdatePluginInstallation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: number; body: UpdatePluginInstallationRequest }) =>
      api<PluginInstallation>(`/admin/plugins/installations/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      toast.success("Plugin updated");
      invalidatePluginQueries(queryClient);
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to update plugin");
    },
  });
}

export function useApplyPluginUpdate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) =>
      api<PluginInstallation>(`/admin/plugins/installations/${id}/update`, {
        method: "POST",
      }),
    onSuccess: () => {
      toast.success("Plugin updated");
      invalidatePluginQueries(queryClient);
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to update plugin");
    },
  });
}

export function useDeletePluginInstallation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api(`/admin/plugins/installations/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      toast.success("Plugin removed");
      invalidatePluginQueries(queryClient);
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to remove plugin");
    },
  });
}

export function useCheckPluginUpdates() {
  const queryClient = useQueryClient();
  return useMutation({
    retry: false,
    mutationFn: () =>
      v2("POST /api/v2/admin/tasks/{key}/run", {
        path: { key: CHECK_PLUGIN_UPDATES_TASK_KEY },
        retryAuthentication: false,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: adminKeys.tasks() });
      queryClient.invalidateQueries({ queryKey: adminKeys.task(CHECK_PLUGIN_UPDATES_TASK_KEY) });
      invalidatePluginQueries(queryClient);
      toast.success("Plugin update check started");
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to start plugin update check");
    },
  });
}

export function useSavePluginConfig() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: number; body: SavePluginConfigRequest }) =>
      api(`/admin/plugins/installations/${id}/config`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: async () => {
      toast.success("Plugin config saved");
      await invalidatePluginQueries(queryClient);
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to save plugin config");
    },
  });
}

export function useTestPluginConfig() {
  return useMutation({
    mutationFn: ({ id, body }: { id: number; body: SavePluginConfigRequest }) =>
      api<ConnectionCheckResponse>(`/admin/plugins/installations/${id}/config/test`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
  });
}

export function useSavePluginAuthBinding() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: number; body: SavePluginAuthBindingRequest }) =>
      api(`/admin/plugins/installations/${id}/auth-binding`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      toast.success("Auth binding saved — restart the server to apply it");
      invalidatePluginQueries(queryClient);
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to save auth binding");
    },
  });
}

export function useSavePluginTaskBinding() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      capabilityId,
      body,
    }: {
      id: number;
      capabilityId: string;
      body: SavePluginTaskBindingRequest;
    }) =>
      api<PluginTaskBindingUpdateResponse>(
        `/admin/plugins/installations/${id}/task-bindings/${capabilityId}`,
        {
          method: "PUT",
          body: JSON.stringify(body),
        },
      ),
    onSuccess: (data) => {
      toast.success(
        data.restart_required
          ? "Task binding saved — restart the server to apply it"
          : "Task binding saved",
      );
      invalidatePluginQueries(queryClient);
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to save task binding");
    },
  });
}
