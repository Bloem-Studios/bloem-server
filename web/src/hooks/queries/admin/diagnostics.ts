import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import {
  api,
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
} from "@/api/client";
import { v2, type V2Result } from "@/api/v2/request";
import { fetchAdminDiagnosticReportBundle } from "@/api/v2/adminDiagnosticDownload";
import type {
  AdminSettingUpdateResponse,
  ClientDiagnosticManifest,
  DiagnosticReport,
  DiagnosticReportListResponse,
  DiagnosticReportSummary,
  DiagnosticStatus,
} from "@/api/types";
import { adminKeys } from "@/hooks/queries/keys";

export interface AdminDiagnosticsQuery {
  user_id?: number | string;
  platform?: string;
  report_type?: string;
  from?: string;
  to?: string;
  short_id?: string;
  limit?: number;
  cursor?: string;
}

export function useDiagnosticsStatus() {
  return useQuery({
    queryKey: adminKeys.diagnosticStatus(),
    queryFn: () => api<DiagnosticStatus>("/diagnostics/status"),
    staleTime: 30_000,
  });
}

export function useUpdateDiagnosticsUploadsEnabled() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (enabled: boolean) =>
      api<AdminSettingUpdateResponse>("/admin/settings/diagnostics.uploads_enabled", {
        method: "PUT",
        body: JSON.stringify({ value: enabled ? "true" : "false" }),
      }),
    onSuccess: async (_result, enabled) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: adminKeys.diagnosticStatus() }),
        queryClient.invalidateQueries({ queryKey: adminKeys.serverSettings() }),
      ]);
      toast.success(
        enabled ? "Client diagnostic uploads enabled" : "Client diagnostic uploads disabled",
      );
    },
    onError: (error) => {
      toast.error(
        error instanceof Error ? error.message : "Failed to update client diagnostic uploads",
      );
    },
  });
}

function diagnosticSummary(
  row: V2Result<"GET /api/v2/admin/diagnostics/reports/{id}">,
): DiagnosticReport {
  return { ...row, manifest: row.manifest as unknown as ClientDiagnosticManifest };
}

export function useDiagnosticReports(params: AdminDiagnosticsQuery) {
  return useQuery({
    queryKey: adminKeys.diagnosticReports({ ...params }),
    queryFn: async (): Promise<DiagnosticReportListResponse> => {
      const profileContext = captureProfileRequestContext();
      if (!profileContext) throw new StaleApiRequestContextError();
      const page = await v2("GET /api/v2/admin/diagnostics/reports", {
        profileContext,
        query: {
          ...params,
          user_id:
            params.user_id === undefined || params.user_id === ""
              ? undefined
              : String(params.user_id),
        },
      });
      if (!isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      return { reports: page.items, next_cursor: page.page?.next_cursor };
    },
    staleTime: 5_000,
  });
}

export function useDiagnosticReport(id?: string) {
  return useQuery({
    queryKey: adminKeys.diagnosticReport(id),
    queryFn: async (): Promise<DiagnosticReport> => {
      const profileContext = captureProfileRequestContext();
      if (!profileContext) throw new StaleApiRequestContextError();
      const report = await v2("GET /api/v2/admin/diagnostics/reports/{id}", {
        path: { id: id! },
        profileContext,
      });
      if (!isCapturedProfileAuthorityActive(profileContext))
        throw new StaleApiRequestContextError();
      return diagnosticSummary(report);
    },
    enabled: Boolean(id),
  });
}

export function useDeleteDiagnosticReport() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      api<void>(`/admin/diagnostics/reports/${encodeURIComponent(id)}`, {
        method: "DELETE",
      }),
    onSuccess: (_result, id) => {
      queryClient.removeQueries({ queryKey: adminKeys.diagnosticReport(id) });
      void queryClient.invalidateQueries({
        queryKey: ["admin", "diagnostics", "reports"],
      });
      toast.success("Diagnostic report deleted");
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to delete diagnostic report");
    },
  });
}

export async function downloadDiagnosticReport(report: DiagnosticReportSummary) {
  // Always stream the bundle through the server (proxy mode) instead of
  // following a presigned URL. When S3Private points at an endpoint only the
  // server can reach (an internal MinIO/R2 gateway), a presigned URL sends the
  // browser to an unreachable host, and because that navigation happens in a
  // separate window the UI can neither detect the failure nor fall back. Admin
  // downloads are bounded and rare, so proxying through the server is reliable
  // and lets errors surface here for the caller to report.
  const blob = await fetchAdminDiagnosticReportBundle(report.id);
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `silo-diagnostics-${report.short_id || report.id}.tar.gz`;
  anchor.style.display = "none";
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}
