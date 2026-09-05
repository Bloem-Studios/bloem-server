import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  CreateHistoryImportRunRequest,
  EmbyConnectLoginRequest,
  EmbyConnectLoginResponse,
  HistoryImportRun,
  HistoryImportSource,
  PlexCheckResponse,
  PlexPinResponse,
} from "@/api/types";
import type { components } from "@/api/v2/schema";
import { v2, type V2Body } from "@/api/v2/request";
import { historyImportKeys } from "./keys";
import { toast } from "sonner";

const STALE_TIME = 15_000;

// The UI keeps the pre-v2 run and source shapes (numeric keys); v2 carries
// every identifier as an opaque string, so the adapters below convert at the
// boundary, as profiles.ts does.
export function historyImportSourceFromV2(
  source: components["schemas"]["HistoryImportSource"],
): HistoryImportSource {
  return { ...source, id: Number(source.id) };
}

export function historyImportRunFromV2(
  run: components["schemas"]["HistoryImportRun"],
): HistoryImportRun {
  return {
    ...run,
    status: run.status as HistoryImportRun["status"],
    user_id: Number(run.user_id),
    mapping_id: run.mapping_id === undefined ? undefined : Number(run.mapping_id),
  };
}

function createRunBodyToV2(
  body: CreateHistoryImportRunRequest,
): V2Body<"POST /api/v2/history-imports/runs"> {
  const { source_id, server_url: _serverUrl, ...rest } = body;
  return {
    ...rest,
    source: body.source as "emby" | "jellyfin" | "plex",
    ...(source_id !== undefined ? { source_id: String(source_id) } : {}),
  };
}

export function useHistoryImportSources() {
  return useQuery({
    queryKey: historyImportKeys.sources(),
    queryFn: () =>
      v2("GET /api/v2/history-imports/sources").then((d) => d.items.map(historyImportSourceFromV2)),
    staleTime: STALE_TIME,
  });
}

export function useHistoryImportRuns(limit = 10) {
  return useQuery({
    queryKey: historyImportKeys.runs(limit),
    queryFn: () =>
      v2("GET /api/v2/history-imports/runs", { query: { limit } }).then((d) =>
        d.items.map(historyImportRunFromV2),
      ),
    staleTime: 5_000,
  });
}

export function useHistoryImportRun(id?: string) {
  return useQuery({
    queryKey: historyImportKeys.run(id),
    queryFn: () =>
      v2("GET /api/v2/history-imports/runs/{id}", { path: { id: id! } }).then(
        historyImportRunFromV2,
      ),
    enabled: !!id,
  });
}

export function useLoginEmbyConnect() {
  return useMutation({
    mutationFn: (body: EmbyConnectLoginRequest): Promise<EmbyConnectLoginResponse> =>
      v2("POST /api/v2/history-imports/emby-connect/login", { body }),
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to sign in with Emby Connect");
    },
  });
}

export function useCreatePlexPin() {
  return useMutation({
    mutationFn: (): Promise<PlexPinResponse> => v2("POST /api/v2/history-imports/plex/auth/pin"),
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to start Plex sign-in");
    },
  });
}

export function useCheckPlexPin(sessionId?: string) {
  return useQuery({
    queryKey: historyImportKeys.plexCheck(sessionId),
    queryFn: (): Promise<PlexCheckResponse> =>
      v2("POST /api/v2/history-imports/plex/auth/check", { body: { session_id: sessionId! } }),
    enabled: !!sessionId,
    retry: false,
    refetchInterval: (query) => {
      const data = query.state.data;
      if (query.state.error) return false;
      if (!data) return 2_000;
      return data.authenticated ? false : 2_000;
    },
  });
}

export function useCreateHistoryImportRun() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateHistoryImportRunRequest) =>
      v2("POST /api/v2/history-imports/runs", { body: createRunBodyToV2(body) }).then(
        historyImportRunFromV2,
      ),
    onSuccess: (run) => {
      toast.success("Import started");
      queryClient.invalidateQueries({ queryKey: historyImportKeys.runs() });
      queryClient.invalidateQueries({ queryKey: historyImportKeys.run(run.id) });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to start import");
    },
  });
}
