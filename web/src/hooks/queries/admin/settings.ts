import { v2, V2ProblemError, type V2Result } from "@/api/v2/request";
import { useRef } from "react";
import {
  adminSettingsKey,
  readAdminSettings,
  captureSettingsBaseline,
  type SettingsValues,
  type SettingsBaseline,
} from "@/api/v2/adminSettingsSnapshot";
import { PASSWORD_RESET_CAPABILITY_KEY } from "@/hooks/queries/passwordReset";
import { refreshAuthProviders } from "../authProviders";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
} from "@/api/client";
import type {
  AdminSettingUpdateResponse,
  AdminServerStatus,
  AdminSettingsUpdateResponse,
  AdminSettingsConnectionCheckRequest,
} from "@/api/types";
import { adminKeys, settingsKeys, themeKeys } from "../keys";
import { toast } from "sonner";

/**
 * server_settings keys surfaced by GET /settings/overlay-config, in the order
 * the admin overlay page presents them. The page edits exactly these, and
 * saving any of them must refresh every profile's cached overlay config.
 */
export const OVERLAY_CONFIG_SERVER_KEYS = [
  "defaults.card_quick_actions_enabled",
  "defaults.card_quick_actions",
  "overlays.enabled",
  "defaults.card_overlays",
] as const;

function affectsOverlayConfig(key: string) {
  return (OVERLAY_CONFIG_SERVER_KEYS as readonly string[]).includes(key);
}

/* eslint-disable react-hooks/refs -- baseline capture must observe authority synchronously. */
function useRetainedSettingsBaseline(
  displayed: SettingsValues | undefined,
  current: SettingsValues | undefined,
) {
  const baseline = useRef<SettingsValues | undefined>(undefined);
  const context = captureProfileRequestContext();
  const authority = JSON.stringify(context ? adminSettingsKey(context) : null);
  const baselineAuthority = useRef(authority);
  if (baselineAuthority.current !== authority) {
    baselineAuthority.current = authority;
    baseline.current = undefined;
  }
  if (!baseline.current && current) baseline.current = current;
  return {
    capture: () => captureSettingsBaseline(displayed ?? baseline.current),
    reset: () => {
      baseline.current = undefined;
    },
  };
}
/* eslint-enable react-hooks/refs */

export type CatalogSearchStatus = V2Result<"GET /api/v2/admin/catalog/search/status">;

export function useAdminServerSettings({ enabled = true }: { enabled?: boolean } = {}) {
  const profileContext = captureProfileRequestContext();
  return useQuery({
    queryKey: profileContext
      ? adminSettingsKey(profileContext)
      : [...adminKeys.serverSettings(), null],
    enabled: enabled && profileContext !== null,
    queryFn: () => {
      if (!profileContext) throw new StaleApiRequestContextError();
      return readAdminSettings(profileContext);
    },
    // The record carries its displayed validator in a WeakMap; do not replace it
    // with an older equal-valued record when only a redacted secret changed.
    structuralSharing: false,
    staleTime: 30_000,
  });
}

export type StorageTransitionPolicy = "start_fresh" | "preserve_uploads" | "migrate_all";
export type StorageTransitionCapabilities =
  V2Result<"GET /api/v2/admin/storage-transitions/capabilities">;
export type StorageTransitionSourceHealth =
  V2Result<"GET /api/v2/admin/storage-transitions/source-health">;

export function useStorageTransitionCapabilities() {
  return useQuery({
    queryKey: [...adminKeys.serverStatus(), "storage-transition-capabilities"] as const,
    queryFn: () => v2("GET /api/v2/admin/storage-transitions/capabilities"),
    retry: false,
    staleTime: 60_000,
  });
}

export function useStorageTransitionSourceHealth(probe: boolean, enabled: boolean) {
  return useQuery({
    queryKey: [...adminKeys.serverStatus(), "storage-transition-source-health", probe] as const,
    enabled,
    queryFn: () => v2("GET /api/v2/admin/storage-transitions/source-health", { query: { probe } }),
    retry: false,
    staleTime: 0,
    refetchInterval: (query) =>
      !probe && query.state.data?.recovery_pending === true ? 5_000 : false,
  });
}

export function useCreateStorageTransition() {
  const queryClient = useQueryClient();
  return useMutation({
    retry: false,
    mutationFn: (request: { policy: StorageTransitionPolicy; values: Record<string, string> }) =>
      v2("POST /api/v2/admin/storage-transitions", { body: request }),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: adminKeys.serverSettings() }),
        queryClient.invalidateQueries({ queryKey: adminKeys.serverStatus() }),
        queryClient.invalidateQueries({ queryKey: ["admin", "jobs"] }),
        queryClient.invalidateQueries({ queryKey: adminKeys.jobs("storage_transition") }),
      ]);
    },
  });
}

export function useCancelStorageTransition() {
  const queryClient = useQueryClient();
  return useMutation({
    retry: false,
    mutationFn: (id: string) => v2("POST /api/v2/admin/jobs/{id}/cancel", { path: { id } }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: adminKeys.jobs("storage_transition") });
    },
  });
}

/** Shape of `GET /admin/settings/restart-keys`. */
export interface RestartKeysResponse {
  keys: string[];
  prefixes: string[];
}

/**
 * The compiled restart-required registry (`internal/config/restart_keys.go`).
 * It only changes across deploys, so it is cached aggressively and never
 * retried: an older server without the endpoint degrades to "nothing needs a
 * restart" rather than to a broken settings page.
 */
export function useAdminRestartKeys() {
  return useQuery({
    queryKey: adminKeys.restartKeys(),
    queryFn: () => v2("GET /api/v2/admin/settings/restart-keys"),
    staleTime: 5 * 60_000,
    retry: false,
  });
}

export function useAdminServerStatus(enabled = true) {
  return useQuery({
    queryKey: adminKeys.serverStatus(),
    enabled,
    queryFn: async (): Promise<AdminServerStatus> => {
      const profileContext = captureProfileRequestContext();
      if (!profileContext) throw new StaleApiRequestContextError();
      const status = await v2("GET /api/v2/admin/server/status", { profileContext });
      if (!isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      return status;
    },
    staleTime: 15_000,
  });
}

export function useUpdateServerSettings(displayed?: SettingsValues) {
  const { data: current } = useAdminServerSettings();
  const retained = useRetainedSettingsBaseline(displayed, current);
  const queryClient = useQueryClient();
  const mutation = useMutation({
    retry: false,
    mutationFn: async (intent: SettingsBaseline & { values: SettingsValues }) => {
      let acknowledgedETag = "";
      const result = await v2("PUT /api/v2/admin/settings", {
        body: { values: intent.values },
        headers: { "If-Match": intent.etag },
        profileContext: intent.profileContext,
        retryAuthentication: false,
        onResponse: (response) => {
          acknowledgedETag = response.headers.get("ETag") ?? "";
        },
      });
      return { ...result, acknowledgedETag };
    },
    onSuccess: async (_data, { values, profileContext }) => {
      if (!isCapturedProfileAuthorityActive(profileContext)) return;
      const keys = Object.keys(values);
      const invalidations = [
        queryClient.invalidateQueries({ queryKey: adminKeys.serverSettings() }),
        queryClient.invalidateQueries({ queryKey: adminKeys.serverStatus() }),
        queryClient.invalidateQueries({
          queryKey: [...adminKeys.serverSettings(), "sensitive-status"] as const,
        }),
      ];
      if (keys.includes("auth.local_password_login") || keys.includes("server.public_url")) {
        invalidations.push(refreshAuthProviders(queryClient));
      }

      if (keys.includes("server.public_url")) {
        // Invitation and reset links are built on the public URL, so the
        // capability answers that gate them change with it.
        invalidations.push(
          queryClient.invalidateQueries({
            queryKey: adminKeys.users(),
            predicate: (query) => query.queryKey.at(-1) === "capabilities",
          }),
          // Self-service reset also needs the public URL.
          queryClient.invalidateQueries({ queryKey: PASSWORD_RESET_CAPABILITY_KEY }),
        );
      }
      if (keys.some((key) => key.startsWith("catalog.search."))) {
        invalidations.push(
          queryClient.invalidateQueries({ queryKey: adminKeys.catalogSearchStatus() }),
        );
      }
      if (keys.some((key) => key.startsWith("branding."))) {
        invalidations.push(queryClient.invalidateQueries({ queryKey: themeKeys.branding() }));
      }
      if (keys.some(affectsOverlayConfig)) {
        invalidations.push(
          queryClient.invalidateQueries({ queryKey: settingsKeys.overlayConfig() }),
        );
      }
      await Promise.all(invalidations);
      if (isCapturedProfileAuthorityActive(profileContext)) retained.reset();
    },
    onError: (err, intent) => {
      if (!isCapturedProfileAuthorityActive(intent.profileContext)) return;
      toast.error(err instanceof Error ? err.message : "Failed to update settings");
    },
  });
  const capture = (values: SettingsValues) => ({
    ...retained.capture(),
    values: { ...values },
  });
  return {
    ...mutation,
    variables: mutation.variables?.values,
    mutate: (
      values: SettingsValues,
      options?: { onSuccess?: (result: AdminSettingsUpdateResponse) => void },
    ) => {
      try {
        const intent = capture(values);
        mutation.mutate(intent, {
          onSuccess: (result) => {
            if (isCapturedProfileAuthorityActive(intent.profileContext))
              options?.onSuccess?.(result);
          },
        });
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "Reload settings before saving.");
      }
    },
    mutateAsync: async (values: SettingsValues) => {
      let intent;
      try {
        intent = capture(values);
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "Reload settings before saving.");
        throw error;
      }
      const result = await mutation.mutateAsync(intent);
      if (!isCapturedProfileAuthorityActive(intent.profileContext))
        throw new StaleApiRequestContextError();
      // The refetch may already include a competing write. Advance retained
      // edits only when it matches the state captured by our acknowledged PUT.
      const refreshed = queryClient.getQueryState<SettingsValues>(
        adminSettingsKey(intent.profileContext),
      );
      const settingsSnapshot =
        refreshed?.status === "success" &&
        !refreshed.isInvalidated &&
        refreshed.data &&
        result.acknowledgedETag &&
        captureSettingsBaseline(refreshed.data).etag === result.acknowledgedETag
          ? refreshed.data
          : undefined;
      return { ...result, settingsSnapshot };
    },
  };
}

export function useUpdateServerSetting(displayed?: SettingsValues) {
  const { data: current } = useAdminServerSettings();
  const retained = useRetainedSettingsBaseline(displayed, current);
  const queryClient = useQueryClient();
  const mutation = useMutation({
    retry: false,
    mutationFn: (intent: SettingsBaseline & { key: string; value: string }) =>
      v2("PUT /api/v2/admin/settings/{key}", {
        path: { key: intent.key },
        body: { value: intent.value },
        headers: { "If-Match": intent.etag },
        profileContext: intent.profileContext,
        retryAuthentication: false,
      }),
    onSuccess: async (_data, variables) => {
      if (!isCapturedProfileAuthorityActive(variables.profileContext)) return;
      const invalidations = [
        queryClient.invalidateQueries({ queryKey: adminKeys.serverSettings() }),
        queryClient.invalidateQueries({ queryKey: adminKeys.serverStatus() }),
        queryClient.invalidateQueries({
          queryKey: [...adminKeys.serverSettings(), "sensitive-status"] as const,
        }),
      ];
      if (variables.key === "auth.local_password_login" || variables.key === "server.public_url") {
        invalidations.push(refreshAuthProviders(queryClient));
      }

      if (variables.key.startsWith("catalog.search.")) {
        invalidations.push(
          queryClient.invalidateQueries({ queryKey: adminKeys.catalogSearchStatus() }),
        );
      }
      // Branding settings are served live by the public `/theme/branding`
      // endpoint and require no restart. Refresh that cache so saved changes
      // apply immediately instead of waiting out its stale window.
      if (variables.key.startsWith("branding.")) {
        invalidations.push(queryClient.invalidateQueries({ queryKey: themeKeys.branding() }));
      }
      if (affectsOverlayConfig(variables.key)) {
        invalidations.push(
          queryClient.invalidateQueries({ queryKey: settingsKeys.overlayConfig() }),
        );
      }
      await Promise.all(invalidations);
      if (isCapturedProfileAuthorityActive(variables.profileContext)) retained.reset();
    },
    onError: (err, intent) => {
      if (!isCapturedProfileAuthorityActive(intent.profileContext)) return;
      toast.error(err instanceof Error ? err.message : "Failed to update setting");
    },
  });
  const capture = (values: { key: string; value: string }) => ({
    ...retained.capture(),
    ...values,
  });
  return {
    ...mutation,
    variables: mutation.variables
      ? { key: mutation.variables.key, value: mutation.variables.value }
      : undefined,
    mutate: (
      values: { key: string; value: string },
      options?: { onSuccess?: (result: AdminSettingUpdateResponse) => void },
    ) => {
      try {
        const intent = capture(values);
        mutation.mutate(intent, {
          onSuccess: (result) => {
            if (isCapturedProfileAuthorityActive(intent.profileContext))
              options?.onSuccess?.(result);
          },
        });
      } catch (error) {
        toast.error(error instanceof Error ? error.message : "Reload settings before saving.");
      }
    },
    mutateAsync: async (values: { key: string; value: string }) => {
      const intent = capture(values);
      const result = await mutation.mutateAsync(intent);
      if (!isCapturedProfileAuthorityActive(intent.profileContext))
        throw new StaleApiRequestContextError();
      return result;
    },
  };
}

export function useAdminSensitiveStatus() {
  return useQuery({
    queryKey: [...adminKeys.serverSettings(), "sensitive-status"] as const,
    queryFn: () => v2("GET /api/v2/admin/settings/sensitive-status"),
    staleTime: 30_000,
  });
}

export function useCheckAdminSettingsConnection() {
  return useMutation({
    mutationFn: ({ kind, body }: { kind: string; body: AdminSettingsConnectionCheckRequest }) =>
      v2("POST /api/v2/admin/settings/check/{kind}", {
        path: { kind },
        body,
        retryAuthentication: false,
      }),
    retry: false,
  });
}

export function useCatalogSearchStatus(enabled = true) {
  return useQuery({
    queryKey: adminKeys.catalogSearchStatus(),
    queryFn: ({ signal }) => v2("GET /api/v2/admin/catalog/search/status", { signal }),
    enabled,
    staleTime: 15_000,
    refetchInterval: (query) => (query.state.data?.index.rebuild_required ? 2_000 : false),
  });
}

export function useAdminSettingValue(key: string) {
  const profileContext = captureProfileRequestContext();
  return useQuery({
    queryKey: [
      ...adminKeys.serverSettings(),
      "key",
      key,
      profileContext?.serverOrigin,
      profileContext?.authContextVersion,
      profileContext?.profileId,
    ],
    enabled: profileContext !== null,
    queryFn: async (): Promise<string | null> => {
      if (!profileContext || !isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      try {
        const result = await v2("GET /api/v2/admin/settings/{key}", {
          path: { key },
          profileContext,
        });
        if (!isCapturedProfileAuthorityActive(profileContext))
          throw new StaleApiRequestContextError();
        return result.value;
      } catch (error) {
        if (!isCapturedProfileAuthorityActive(profileContext))
          throw new StaleApiRequestContextError();
        if (error instanceof V2ProblemError && error.status === 404) return null;
        throw error;
      }
    },
  });
}
