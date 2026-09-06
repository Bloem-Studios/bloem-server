import { v2, V2ProblemError, type V2Result } from "@/api/v2/request";
import { useRef } from "react";
import {
  adminSettingsKey,
  readAdminSettings,
  captureSettingsBaseline,
  type SettingsValues,
  type SettingsBaseline,
} from "@/api/v2/adminSettingsSnapshot";
import { jellyfinCompatStatusKey } from "@/api/v2/jellyfinStatusCache";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  api,
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
} from "@/api/client";
import type {
  AdminSettingUpdateResponse,
  AdminServerStatus,
  AdminSettingsUpdateResponse,
  AdminSettingsConnectionCheckRequest,
  JellyfinCompatSettingsPatch,
  JellyfinCompatStatus,
  JellyfinCompatWebInstallRequest,
} from "@/api/types";
import { adminKeys, compatKeys, settingsKeys, themeKeys } from "../keys";
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

export type CatalogSearchStatus = V2Result<"GET /api/v2/admin/catalog/search/status">;

export function useAdminServerSettings() {
  const profileContext = captureProfileRequestContext();
  return useQuery({
    queryKey: profileContext
      ? adminSettingsKey(profileContext)
      : [...adminKeys.serverSettings(), null],
    enabled: profileContext !== null,
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

export function useAdminServerStatus() {
  return useQuery({
    queryKey: adminKeys.serverStatus(),
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
  const baseline = useRef<SettingsValues | undefined>(undefined);
  const context = captureProfileRequestContext();
  const authority = JSON.stringify(context ? adminSettingsKey(context) : null);
  const baselineAuthority = useRef(authority);
  if (baselineAuthority.current !== authority) {
    baselineAuthority.current = authority;
    baseline.current = undefined;
  }
  if (!baseline.current && current) baseline.current = current;
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
      if (keys.some((key) => key.startsWith("jellyfin_compat."))) {
        invalidations.push(
          queryClient.invalidateQueries({ queryKey: adminKeys.jellyfinCompatStatus() }),
          // The user-facing Connect Apps card reads the same settings and
          // caches them for minutes, so it has to drop its copy too.
          queryClient.invalidateQueries({ queryKey: compatKeys.all }),
        );
      }
      if (keys.some((key) => key.startsWith("catalog.search."))) {
        invalidations.push(
          queryClient.invalidateQueries({ queryKey: adminKeys.catalogSearchStatus() }),
        );
      }
      if (keys.some((key) => key.startsWith("branding.") || key.startsWith("ui.admin_"))) {
        invalidations.push(
          queryClient.invalidateQueries({ queryKey: themeKeys.adminCss() }),
          queryClient.invalidateQueries({ queryKey: themeKeys.branding() }),
        );
      }
      if (keys.some(affectsOverlayConfig)) {
        invalidations.push(
          queryClient.invalidateQueries({ queryKey: settingsKeys.overlayConfig() }),
        );
      }
      await Promise.all(invalidations);
      if (isCapturedProfileAuthorityActive(profileContext)) baseline.current = undefined;
    },
    onError: (err, intent) => {
      if (!isCapturedProfileAuthorityActive(intent.profileContext)) return;
      toast.error(err instanceof Error ? err.message : "Failed to update settings");
    },
  });
  const capture = (values: SettingsValues) => ({
    ...captureSettingsBaseline(displayed ?? baseline.current),
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
      const intent = capture(values);
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
  const baseline = useRef<SettingsValues | undefined>(undefined);
  const context = captureProfileRequestContext();
  const authority = JSON.stringify(context ? adminSettingsKey(context) : null);
  const baselineAuthority = useRef(authority);
  if (baselineAuthority.current !== authority) {
    baselineAuthority.current = authority;
    baseline.current = undefined;
  }
  if (!baseline.current && current) baseline.current = current;
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
      if (variables.key.startsWith("jellyfin_compat.")) {
        invalidations.push(
          queryClient.invalidateQueries({ queryKey: adminKeys.jellyfinCompatStatus() }),
        );
      }
      if (variables.key.startsWith("catalog.search.")) {
        invalidations.push(
          queryClient.invalidateQueries({ queryKey: adminKeys.catalogSearchStatus() }),
        );
      }
      // Branding and admin theme settings are served live by public endpoints
      // (`/theme/branding`, `/theme/admin-css`) and require no restart. Refresh
      // those caches so saved changes apply immediately instead of waiting out
      // the 60s / 5min stale windows.
      if (variables.key.startsWith("branding.") || variables.key.startsWith("ui.admin_")) {
        invalidations.push(
          queryClient.invalidateQueries({ queryKey: themeKeys.adminCss() }),
          queryClient.invalidateQueries({ queryKey: themeKeys.branding() }),
        );
      }
      if (affectsOverlayConfig(variables.key)) {
        invalidations.push(
          queryClient.invalidateQueries({ queryKey: settingsKeys.overlayConfig() }),
        );
      }
      await Promise.all(invalidations);
      if (isCapturedProfileAuthorityActive(variables.profileContext)) baseline.current = undefined;
    },
    onError: (err, intent) => {
      if (!isCapturedProfileAuthorityActive(intent.profileContext)) return;
      toast.error(err instanceof Error ? err.message : "Failed to update setting");
    },
  });
  const capture = (values: { key: string; value: string }) => ({
    ...captureSettingsBaseline(displayed ?? baseline.current),
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

export function useJellyfinCompatStatus() {
  const profileContext = captureProfileRequestContext();
  return useQuery({
    queryKey: profileContext
      ? jellyfinCompatStatusKey(profileContext)
      : [...adminKeys.jellyfinCompatStatus(), null],
    enabled: profileContext !== null,
    queryFn: async (): Promise<JellyfinCompatStatus> => {
      if (!profileContext || !isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      const status = await v2("GET /api/v2/admin/jellyfin-compat/status", { profileContext });
      if (!isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      return {
        ...status,
        operation: status.operation
          ? { ...status.operation, started_at: status.operation.started_at ?? "" }
          : undefined,
      };
    },
    staleTime: 15_000,
  });
}

export function useUpdateJellyfinCompatSettings() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: JellyfinCompatSettingsPatch) =>
      api<JellyfinCompatStatus>("/admin/jellyfin-compat/settings", {
        method: "PATCH",
        body: JSON.stringify(body),
      }),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: adminKeys.jellyfinCompatStatus() }),
        queryClient.invalidateQueries({ queryKey: adminKeys.serverSettings() }),
        queryClient.invalidateQueries({ queryKey: adminKeys.serverStatus() }),
        // Keeps the user-facing Connect Apps card from serving a stale
        // address after an admin edits it.
        queryClient.invalidateQueries({ queryKey: compatKeys.all }),
      ]);
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to update Jellyfin compatibility");
    },
  });
}

export function useInstallJellyfinCompatWeb() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: JellyfinCompatWebInstallRequest = {}) =>
      api<JellyfinCompatStatus>("/admin/jellyfin-compat/web/install", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: async () => {
      toast.success("Jellyfin Web install started");
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: adminKeys.jellyfinCompatStatus() }),
        queryClient.invalidateQueries({ queryKey: adminKeys.serverSettings() }),
        queryClient.invalidateQueries({ queryKey: adminKeys.serverStatus() }),
      ]);
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to install Jellyfin Web assets");
    },
  });
}

export function useRemoveJellyfinCompatWeb() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      api<JellyfinCompatStatus>("/admin/jellyfin-compat/web/remove", {
        method: "POST",
        body: JSON.stringify({}),
      }),
    onSuccess: async () => {
      toast.success("Jellyfin Web removal started");
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: adminKeys.jellyfinCompatStatus() }),
        queryClient.invalidateQueries({ queryKey: adminKeys.serverSettings() }),
        queryClient.invalidateQueries({ queryKey: adminKeys.serverStatus() }),
      ]);
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to remove Jellyfin Web assets");
    },
  });
}

/** Reads one stored setting for the setup wizard. Protected/empty values stay absent. */
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
