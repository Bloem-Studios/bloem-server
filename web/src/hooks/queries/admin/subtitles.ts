import {
  updateAdminSubtitleMetadata,
  type AdminSubtitleEditor,
  type AdminSubtitlePatch,
} from "@/api/v2/adminSubtitleMetadata";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/client";
import {
  adminSubtitleListScope,
  listAdminSubtitles,
  type AdminSubtitleListQuery,
} from "@/api/v2/adminSubtitles";
import { v2 } from "@/api/v2/request";
import type { SubtitleProviderUpdateRequest, SubtitleProviderTestRequest } from "@/api/types";
import { adminKeys } from "../keys";
import { toast } from "sonner";

const ADMIN_STALE_TIME = 30_000;

export function useAdminDownloadedSubtitles(filters: AdminSubtitleListQuery) {
  const scope = adminSubtitleListScope();
  return useQuery({
    queryKey: ["admin", "downloadedSubtitles", scope, filters],
    queryFn: ({ signal }) => listAdminSubtitles(filters, scope, signal),
    retry: false,
    staleTime: ADMIN_STALE_TIME,
  });
}

export function useAdminUpdateDownloadedSubtitle() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ editor, patch }: { editor: AdminSubtitleEditor; patch: AdminSubtitlePatch }) =>
      updateAdminSubtitleMetadata(editor, patch),
    retry: false,
    gcTime: 0,
    onSuccess: (_updated, { editor }) => {
      if (adminSubtitleListScope() !== editor.intent.scope) return;
      toast.success("Subtitle updated");
      void queryClient.invalidateQueries({
        queryKey: ["admin", "downloadedSubtitles", editor.intent.scope],
      });
    },
  });
}

export function useSubtitleProviders() {
  return useQuery({
    queryKey: adminKeys.subtitleProviders(),
    queryFn: () => v2("GET /api/v2/admin/subtitle-providers"),
    staleTime: ADMIN_STALE_TIME,
  });
}

export function useUpdateSubtitleProvider() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      provider,
      config,
    }: {
      provider: string;
      config: SubtitleProviderUpdateRequest;
    }) =>
      api<{ status: string }>(`/admin/subtitle-providers/${provider}`, {
        method: "PUT",
        body: JSON.stringify(config),
      }),
    onSuccess: async () => {
      toast.success("Provider settings saved");
      await queryClient.invalidateQueries({
        queryKey: adminKeys.subtitleProviders(),
      });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to save provider settings");
    },
  });
}

export function useTestSubtitleProvider() {
  return useMutation({
    mutationFn: ({ provider, config }: { provider: string; config: SubtitleProviderTestRequest }) =>
      testSubtitleProvider(provider, config),
    retry: false,
  });
}

export function testSubtitleProvider(provider: string, config: SubtitleProviderTestRequest) {
  return v2("POST /api/v2/admin/subtitle-providers/{provider}/test", {
    path: { provider },
    body: config,
    retryAuthentication: false,
  });
}
