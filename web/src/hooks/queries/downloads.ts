import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/client";
import { fetchDownloadCapability, deleteDownloadEntry } from "@/api/v2/downloadRegistry";
import { downloadKeys } from "./keys";
import { toast } from "sonner";

export type DownloadQuality = "original" | "20mbps" | "10mbps" | "5mbps" | "2mbps" | "1mbps";
export type DownloadDeliveryFormat = "original" | "remux" | "transcode";

interface DownloadResponse {
  id: string;
  content_id: string;
  episode_id?: string;
  batch_id?: string;
  device_id?: string;
  media_file_id: number;
  file_size: number;
  bytes_sent: number;
  kind: string;
  status: string;
  quality: DownloadQuality;
  effective_quality: DownloadQuality;
  delivery_format: DownloadDeliveryFormat;
  target_bitrate_kbps: number;
  revision: number;
  created_at: string;
  completed_at?: string;
}

interface CreateDownloadRequest {
  content_id: string;
  episode_id?: string;
  file_id?: number;
  quality?: DownloadQuality;
  series?: boolean;
  /** With series: true, restrict to one season; 0 is the Specials season. */
  season_number?: number;
}

export interface DownloadCapability {
  enabled: boolean;
  download_allowed: boolean;
  quality_presets: DownloadQuality[];
  transcode_enabled: boolean;
  transcode_user_allowed: boolean;
  season_download: boolean;
  series_monitoring: boolean;
  monitoring_modes?: string[];
  proxy_delivery: boolean;
}

export function useDownloadCapability(enabled = true) {
  return useQuery({
    queryKey: downloadKeys.capability(),
    queryFn: async () => (await fetchDownloadCapability()) as DownloadCapability,
    enabled,
  });
}

export function useCreateDownload() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateDownloadRequest) =>
      api<DownloadResponse | { downloads: DownloadResponse[] }>("/downloads", {
        method: "POST",
        body: JSON.stringify(req),
      }),
    onSuccess: (_data, req) => {
      toast.success(req.series ? "Series download queued" : "Download queued");
      qc.invalidateQueries({ queryKey: downloadKeys.all });
    },
    onError: () => {
      toast.error("Failed to start download");
    },
  });
}

export function useDeleteDownload() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: deleteDownloadEntry,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: downloadKeys.all });
    },
  });
}
