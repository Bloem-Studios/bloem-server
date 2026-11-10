import { useSyncExternalStore } from "react";
import {
  keepPreviousData,
  useMutation as useBaseMutation,
  type QueryClient,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { toast } from "sonner";
import { bloemT, type BloemMessageKey } from "@/bloem/i18n";
import {
  captureSessionIdentity,
  captureProfileRequestContext,
  isSessionIdentityCurrent,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
  type ProfileRequestContextSnapshot,
} from "@/api/client";
import { nativeApi, nativeApiWithProfileRequestContext } from "@/api/bloemClient";
import type {
  LiveTVChannel,
  LiveTVChannelsResponse,
  LiveTVGuideResponse,
  LiveTVGuideSource,
  LiveTVGuideSourcesResponse,
  LiveTVRecording,
  LiveTVRecordingsResponse,
  LiveTVDiscoverTunersResponse,
  LiveTVTuner,
  LiveTVTunersResponse,
  SchedulesDirectLineupsResponse,
  XMLSyncLineupsResponse,
} from "@/api/bloemTypes";
import { liveTVKeys } from "./bloemKeys";

// Operational writes must not queue for a later profile/session or replay an
// uncertain creation. The native client also disables transport/auth replay.
const useMutation: typeof useBaseMutation = (options, client) => {
  const identity = captureSessionIdentity();
  const profile = captureProfileRequestContext();
  const mutationFn = options.mutationFn;
  return useBaseMutation(
    {
      ...options,
      networkMode: "always",
      retry: false,
      mutationFn: mutationFn
        ? (variables, context) => {
            if (
              !isSessionIdentityCurrent(identity) ||
              (profile && !isCapturedProfileAuthorityActive(profile))
            )
              throw new StaleApiRequestContextError();
            return mutationFn(variables, context);
          }
        : undefined,
    },
    client,
  );
};

const LIVETV_STALE_TIME = 30_000;

/** The server's message for a failed request, else `fallback` from the catalog. */
function errorText(error: unknown, fallback: BloemMessageKey): string {
  return error instanceof Error ? error.message : bloemT(fallback);
}

/** Well inside the server's stale-session TTL so a paused player keeps its tuner. */

export function useLiveTVTuners() {
  return useQuery({
    queryKey: liveTVKeys.liveTVTuners(),
    queryFn: () =>
      nativeApi<LiveTVTunersResponse>("/livetv/tuners").then((data) => data.tuners ?? []),
    staleTime: LIVETV_STALE_TIME,
  });
}

export function useDiscoverLiveTVTuners() {
  return useMutation({
    mutationFn: (body: { timeout_ms?: number; include_udp?: boolean; probe_urls?: string[] }) =>
      nativeApi<LiveTVDiscoverTunersResponse>("/livetv/tuners/discover", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onError: (err) => {
      toast.error(errorText(err, "bloem.admin.liveTv.toasts.discoveryFailed"));
    },
  });
}

export function useAddLiveTVTuner() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { url?: string; discover_url?: string; device_id?: string }) =>
      nativeApi<LiveTVTuner>("/livetv/tuners", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      toast.success(bloemT("bloem.admin.liveTv.toasts.tunerAdded"));
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVTuners() });
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVChannels() });
    },
    onError: (err) => {
      toast.error(errorText(err, "bloem.admin.liveTv.toasts.addTunerFailed"));
    },
  });
}

export function useScanLiveTVTuner() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (tunerId: string) =>
      nativeApi<LiveTVTuner>(`/livetv/tuners/${encodeURIComponent(tunerId)}/scan`, {
        method: "POST",
      }),
    onSuccess: () => {
      toast.success(bloemT("bloem.admin.liveTv.toasts.lineupRescanned"));
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVTuners() });
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVChannels() });
    },
    onError: (err) => {
      toast.error(errorText(err, "bloem.admin.liveTv.toasts.scanTunerFailed"));
    },
  });
}

export function useDeleteLiveTVTuner() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (tunerId: string) =>
      nativeApi(`/livetv/tuners/${encodeURIComponent(tunerId)}`, { method: "DELETE" }),
    onSuccess: () => {
      toast.success(bloemT("bloem.admin.liveTv.toasts.tunerRemoved"));
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVTuners() });
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVChannels() });
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVGuideSources() });
      void queryClient.invalidateQueries({ queryKey: ["livetv"] });
    },
    onError: (err) => {
      toast.error(errorText(err, "bloem.admin.liveTv.toasts.deleteTunerFailed"));
    },
  });
}

export function useLiveTVChannels(tunerId?: string) {
  const params = new URLSearchParams();
  if (tunerId) params.set("tuner_id", tunerId);
  const qs = params.toString();
  return useQuery({
    queryKey: liveTVKeys.liveTVChannels(tunerId),
    queryFn: () =>
      nativeApi<LiveTVChannelsResponse>(`/livetv/channels${qs ? `?${qs}` : ""}`).then(
        (data) => data.channels ?? [],
      ),
    staleTime: LIVETV_STALE_TIME,
    placeholderData: keepPreviousData,
  });
}

export function usePatchLiveTVChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      channelId,
      body,
    }: {
      channelId: string;
      body: { enabled?: boolean; number_override?: string | null; guide_station_id?: string };
    }) =>
      nativeApi<LiveTVChannel>(`/livetv/channels/${encodeURIComponent(channelId)}`, {
        method: "PATCH",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      toast.success(bloemT("bloem.admin.liveTv.toasts.channelUpdated"));
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVChannels() });
    },
    onError: (err) => {
      toast.error(errorText(err, "bloem.admin.liveTv.toasts.updateChannelFailed"));
    },
  });
}

export function useLiveTVGuideSources() {
  return useQuery({
    queryKey: liveTVKeys.liveTVGuideSources(),
    queryFn: () =>
      nativeApi<LiveTVGuideSourcesResponse>("/livetv/guide-sources").then(
        (data) => data.guide_sources ?? [],
      ),
    staleTime: LIVETV_STALE_TIME,
  });
}

export function useCreateLiveTVGuideSource() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<LiveTVGuideSource>) =>
      nativeApi<LiveTVGuideSource>("/livetv/guide-sources", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      toast.success(bloemT("bloem.admin.liveTv.toasts.guideSourceAdded"));
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVGuideSources() });
    },
    onError: (err) => {
      toast.error(errorText(err, "bloem.admin.liveTv.toasts.addGuideSourceFailed"));
    },
  });
}

export function useUpdateLiveTVGuideSource() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: Partial<LiveTVGuideSource> }) =>
      nativeApi<LiveTVGuideSource>(`/livetv/guide-sources/${encodeURIComponent(id)}`, {
        method: "PATCH",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      toast.success(bloemT("bloem.admin.liveTv.toasts.guideSourceUpdated"));
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVGuideSources() });
    },
    onError: (err) => {
      toast.error(errorText(err, "bloem.admin.liveTv.toasts.updateGuideSourceFailed"));
    },
  });
}

export function useDeleteLiveTVGuideSource() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      nativeApi(`/livetv/guide-sources/${encodeURIComponent(id)}`, { method: "DELETE" }),
    onSuccess: () => {
      toast.success(bloemT("bloem.admin.liveTv.toasts.guideSourceRemoved"));
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVGuideSources() });
    },
    onError: (err) => {
      toast.error(errorText(err, "bloem.admin.liveTv.toasts.deleteGuideSourceFailed"));
    },
  });
}

export function useSyncLiveTVGuideSource() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      nativeApi<LiveTVGuideSource>(`/livetv/guide-sources/${encodeURIComponent(id)}/sync`, {
        method: "POST",
      }),
    onSuccess: () => {
      toast.success(bloemT("bloem.admin.liveTv.toasts.guideSyncFinished"));
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVGuideSources() });
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVChannels() });
      void queryClient.invalidateQueries({ queryKey: ["livetv", "guide"] });
    },
    onError: (err) => {
      void queryClient.invalidateQueries({ queryKey: liveTVKeys.liveTVGuideSources() });
      toast.error(errorText(err, "bloem.admin.liveTv.toasts.syncGuideSourceFailed"));
    },
  });
}

export function useLookupSchedulesDirectLineups() {
  return useMutation({
    mutationFn: (body: {
      username: string;
      password: string;
      country?: string;
      postalcode: string;
    }) =>
      nativeApi<SchedulesDirectLineupsResponse>("/livetv/guide-sources/schedules-direct/lineups", {
        method: "POST",
        body: JSON.stringify(body),
      }).then((data) => data.lineups ?? []),
    onError: (err) => {
      toast.error(errorText(err, "bloem.admin.liveTv.toasts.lineupLookupFailed"));
    },
  });
}

export function useLookupXMLSyncLineups() {
  return useMutation({
    mutationFn: (body: { country?: string; postalcode: string; lang?: string }) =>
      nativeApi<XMLSyncLineupsResponse>("/livetv/guide-sources/xml-sync/lineups", {
        method: "POST",
        body: JSON.stringify(body),
      }).then((data) => data.lineups ?? []),
    onError: (err) => {
      toast.error(errorText(err, "bloem.admin.liveTv.toasts.xmlSyncLookupFailed"));
    },
  });
}

export type LiveTVGuideParams = {
  channelIds?: string[];
  start?: string;
  end?: string;
};

export function useLiveTVGuide(params: LiveTVGuideParams = {}, enabled = true) {
  const search = new URLSearchParams();
  if (params.channelIds?.length) search.set("channels", params.channelIds.join(","));
  if (params.start) search.set("start", params.start);
  if (params.end) search.set("end", params.end);
  const qs = search.toString();
  return useQuery({
    queryKey: liveTVKeys.liveTVGuide({
      channels: params.channelIds?.join(",") ?? "",
      start: params.start ?? "",
      end: params.end ?? "",
    }),
    queryFn: () =>
      nativeApi<LiveTVGuideResponse>(`/livetv/guide${qs ? `?${qs}` : ""}`).then((data) => ({
        programs: data.programs ?? [],
        start: data.start,
        end: data.end,
      })),
    staleTime: LIVETV_STALE_TIME,
    enabled,
    placeholderData: keepPreviousData,
  });
}

/**
 * Codecs this browser can decode, so the server knows whether it can hand us
 * the broadcast streams untouched. OTA channels are MPEG-2 video with AC-3
 * audio, neither of which Media Source Extensions decode — without this the
 * bridge copies them and playback is a black screen with no sound.
 */
export type LiveTVClientCapabilities = {
  codecs_video: string[];
  codecs_audio: string[];
  max_resolution?: string;
};

function captureRecordingScope(profile = captureProfileRequestContext()) {
  // A manual draft supplies its original authority. Never capture a newer
  // session identity for that intent, even if React renders it again.
  const identity = profile
    ? { serverOrigin: profile.serverOrigin, authContextVersion: profile.authContextVersion }
    : captureSessionIdentity();
  return {
    identity,
    profile,
    key: [
      "livetv",
      "recordings",
      identity.serverOrigin,
      identity.authContextVersion,
      profile?.profileId ?? null,
      profile?.profileTokenGeneration ?? null,
    ] as const,
  };
}

type RecordingScope = ReturnType<typeof captureRecordingScope>;

function recordingScopeActive(scope: RecordingScope) {
  const current = captureProfileRequestContext();
  return (
    isSessionIdentityCurrent(scope.identity) &&
    (scope.profile
      ? isCapturedProfileAuthorityActive(scope.profile) &&
        current?.profileTokenGeneration === scope.profile.profileTokenGeneration
      : current === null)
  );
}

async function recordingRequest<T>(scope: RecordingScope, path: string, options: RequestInit = {}) {
  if (!recordingScopeActive(scope)) throw new StaleApiRequestContextError();
  const policy = options.method ? "none" : "safe";
  const data = scope.profile
    ? await nativeApiWithProfileRequestContext<T>(path, scope.profile, options, policy)
    : await nativeApi<T>(path, options, policy);
  if (!recordingScopeActive(scope)) throw new StaleApiRequestContextError();
  return data;
}

function recordingQueryOptions(scope: RecordingScope, status?: string) {
  const params = new URLSearchParams();
  if (status) params.set("status", status);
  const qs = params.toString();
  return {
    queryKey: [...scope.key, status ?? "all"],
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      recordingRequest<LiveTVRecordingsResponse>(scope, `/livetv/recordings${qs ? `?${qs}` : ""}`, {
        signal,
      }).then((data) => data.recordings ?? []),
    staleTime: LIVETV_STALE_TIME,
    networkMode: "always" as const,
    retry: false,
  };
}

export function useLiveTVRecordings(status?: string) {
  return useQuery(recordingQueryOptions(captureRecordingScope(), status));
}

// Keep the guard outside component lifetimes. A failed readback stays blocked
// across hooks/remounts; background query refreshes cannot acknowledge it.
type RecordingWritePhase = "idle" | "pending" | "reload-required" | "reloading";
function createRecordingWriteState() {
  let phase: RecordingWritePhase = "idle";
  const listeners = new Set<() => void>();
  return {
    getSnapshot: () => phase,
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    set: (next: RecordingWritePhase) => {
      phase = next;
      listeners.forEach((listener) => listener());
    },
  };
}
const recordingWrites = new WeakMap<
  QueryClient,
  Map<string, ReturnType<typeof createRecordingWriteState>>
>();

function recordingWriteState(client: QueryClient, key: string) {
  let states = recordingWrites.get(client);
  if (!states) {
    states = new Map();
    recordingWrites.set(client, states);
  }
  let state = states.get(key);
  if (!state) {
    state = createRecordingWriteState();
    states.set(key, state);
  }
  return state;
}

async function reconcileRecordings(client: QueryClient, scope: RecordingScope) {
  if (!recordingScopeActive(scope)) throw new StaleApiRequestContextError();
  // Cancel pre-write reads and force a fresh request even without a mounted list.
  await client.cancelQueries({ queryKey: scope.key });
  await client.invalidateQueries({ queryKey: scope.key, refetchType: "none" });
  await client.fetchQuery(recordingQueryOptions(scope));
  if (!recordingScopeActive(scope)) throw new StaleApiRequestContextError();
  await client.refetchQueries(
    {
      queryKey: scope.key,
      type: "active",
      predicate: (query) => query.queryKey.at(-1) !== "all",
    },
    { throwOnError: true },
  );
  if (!recordingScopeActive(scope)) throw new StaleApiRequestContextError();
}

function useRecordingWrite<T>(
  request: (variables: T) => { path: string; options: RequestInit },
  successMessage: BloemMessageKey,
  failureMessage: BloemMessageKey,
  authority?: ProfileRequestContextSnapshot,
) {
  const client = useQueryClient();
  const scope = captureRecordingScope(authority);
  const key = JSON.stringify(scope.key);
  const state = recordingWriteState(client, key);
  const phase = useSyncExternalStore(state.subscribe, state.getSnapshot);
  const reloadAction = {
    label: bloemT("bloem.liveTv.recordingWrites.reload"),
    onClick: () => {
      void reloadRecordings().catch(() => {});
    },
  };

  async function reloadRecordings() {
    if (!recordingScopeActive(scope)) throw new StaleApiRequestContextError();
    if (["pending", "reloading"].includes(state.getSnapshot()))
      throw new Error(bloemT("bloem.liveTv.recordingWrites.waitForReadback"));
    state.set("reloading");
    let reconciled = false;
    try {
      await reconcileRecordings(client, scope);
      reconciled = true;
      toast.success(bloemT("bloem.liveTv.recordingWrites.reloaded"));
    } catch (error) {
      if (recordingScopeActive(scope))
        toast.error(bloemT("bloem.liveTv.recordingWrites.reloadFailed"), {
          action: reloadAction,
        });
      throw error;
    } finally {
      state.set(reconciled ? "idle" : "reload-required");
    }
  }

  const mutation = useBaseMutation({
    mutationKey: scope.key,
    networkMode: "always",
    retry: false,
    onMutate: () => key,
    mutationFn: async (variables: T) => {
      if (!recordingScopeActive(scope)) throw new StaleApiRequestContextError();
      if (state.getSnapshot() !== "idle") {
        const message = bloemT("bloem.liveTv.recordingWrites.blocked");
        if (state.getSnapshot() === "reload-required")
          toast.error(message, { action: reloadAction });
        throw new Error(message);
      }
      state.set("pending");
      let reconciled = false;
      try {
        const { path, options } = request(variables);
        let data: LiveTVRecording;
        try {
          data = await recordingRequest<LiveTVRecording>(scope, path, options);
        } finally {
          await reconcileRecordings(client, scope);
          reconciled = true;
        }
        if (!recordingScopeActive(scope)) throw new StaleApiRequestContextError();
        toast.success(bloemT(successMessage));
        return data;
      } catch (error) {
        if (recordingScopeActive(scope))
          toast.error(
            bloemT(
              reconciled
                ? "bloem.liveTv.recordingWrites.failedReviewRefreshed"
                : "bloem.liveTv.recordingWrites.failedReloadFirst",
              { error: error instanceof Error ? error.message : bloemT(failureMessage) },
            ),
            reconciled ? undefined : { action: reloadAction },
          );
        throw error;
      } finally {
        state.set(reconciled ? "idle" : "reload-required");
      }
    },
  });
  const currentMutation = mutation.context === key;
  return {
    ...mutation,
    data: currentMutation ? mutation.data : undefined,
    error: currentMutation ? mutation.error : null,
    isSuccess: currentMutation && mutation.isSuccess,
    isError: currentMutation && mutation.isError,
    isPending: phase === "pending" || phase === "reloading",
    isBlocked: phase !== "idle",
    needsReload: phase === "reload-required" || phase === "reloading",
    reloadRecordings,
  };
}

export function useScheduleLiveTVRecording(authority?: ProfileRequestContextSnapshot) {
  return useRecordingWrite(
    (body: {
      program_id?: string;
      channel_id?: string;
      start?: string;
      stop?: string;
      title?: string;
    }) => ({
      path: "/livetv/recordings",
      options: { method: "POST", body: JSON.stringify(body) },
    }),
    "bloem.liveTv.recordingWrites.scheduled",
    "bloem.liveTv.recordingWrites.scheduleFailed",
    authority,
  );
}

export function useCancelLiveTVRecording() {
  return useRecordingWrite(
    (recordingId: string) => ({
      path: `/livetv/recordings/${encodeURIComponent(recordingId)}`,
      options: { method: "DELETE" },
    }),
    "bloem.liveTv.recordingWrites.cancelled",
    "bloem.liveTv.recordingWrites.cancelFailed",
  );
}
