import { v2, type V2Result } from "@/api/v2/request";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/client";
import type {
  AdminSettingUpdateResponse,
  AdminServerStatus,
  AdminSettingsUpdateResponse,
  AdminSettingsConnectionCheckRequest,
  ConnectionCheckResponse,
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
  return useQuery({
    queryKey: adminKeys.serverSettings(),
    queryFn: () => v2("GET /api/v2/admin/settings/effective"),
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
    queryFn: () => api<AdminServerStatus>("/admin/server/status"),
    staleTime: 15_000,
  });
}

export function useUpdateServerSettings() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (values: Record<string, string>) =>
      api<AdminSettingsUpdateResponse>("/admin/settings", {
        method: "PUT",
        body: JSON.stringify({ values }),
      }),
    onSuccess: async (_data, values) => {
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
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to update settings");
    },
  });
}

export function useUpdateServerSetting() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ key, value }: { key: string; value: string }) =>
      api<AdminSettingUpdateResponse>(`/admin/settings/${key}`, {
        method: "PUT",
        body: JSON.stringify({ value }),
      }),
    onSuccess: async (_data, variables) => {
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
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to update setting");
    },
  });
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
  return useQuery({
    queryKey: adminKeys.jellyfinCompatStatus(),
    queryFn: () => api<JellyfinCompatStatus>("/admin/jellyfin-compat/status"),
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
