import type { AudiobookChapterIntent } from "@/player/bound-client-timeline";
import { useEffect, useMemo, useRef, useState, useCallback, type RefObject } from "react";
import { toast } from "sonner";
import { useQueryClient } from "@tanstack/react-query";
import { progressKeys } from "@/hooks/queries/keys";
import { discoverAudiobookTimeline } from "./timelineDiscovery";
import {
  validateSelectedTimeline,
  type PlaybackTimelineManifest,
  type BoundAcceptedProgress,
} from "@/player/bound-client-timeline";
import { hasDurableTermination } from "@/player/durable-session-mutations";
import { buildPlayerChapters, nextChapterStart, prevChapterStart } from "@/lib/audiobooks/chapters";
import type { AudiobookFile } from "@/lib/audiobooks/types";
import { getPersistedVolume, persistVolume } from "@/player/components/VolumeControl";
import {
  buildClientCapabilitiesV3,
  buildClientPlaybackContextV3,
  detectBandwidthEstimateKbpsV3,
  detectMeteredV3,
} from "@/player/client-context-v3";
import { usePlayerConfig } from "@/player/context/PlayerConfigContext";
import { startInitialPlayback } from "@/player/initial-v2";
import { replanDurableSession } from "@/player/lifecycle-v2";
import {
  captureSessionProgress,
  durableSessionFor,
  sendSessionProgress,
  stopSequencedSession,
} from "@/player/session-mutations";
import { reportDurableRouteEvent } from "@/player/route-events-v2";
import { randomUUID } from "@/lib/uuid";
import { describePlanTerminal } from "@/player/playback-errors";
import {
  MAX_ATTEMPT_COUNT_V3,
  MAX_ATTEMPTED_PLAN_KEYS_V3,
  QUALITY_ORIGINAL_V3,
  type DecisionResponseV3,
  type FailureV3,
  type PlanV3,
} from "@/player/protocol-v3";
import type { PlaybackRealtimeCommandEnvelope } from "@/player/realtime-protocol";
import { buildRouteEventV3, type RouteEventInput } from "@/player/route-events-v3";
import { buildPlayerStreamUrl } from "@/player/stream-url";
import type { PlayerChapter } from "@/player/types";
import { useCodecDetection } from "@/player/hooks/useCodecDetection";
import { usePlaybackRealtime } from "@/player/hooks/usePlaybackRealtime";
import { buildReplanRequestV3, buildStartRequestV3 } from "@/player/playback-session-wire-v3";
import type { SleepSetting } from "@/player/components/SleepTimerMenu";
import { smartRewindSeconds } from "./smartRewind";
import { clampAudiobookRate, getBookRate, setBookRate } from "./useAudiobookPrefs";

const REPORT_INTERVAL_MS = 10_000;

export interface UseAudiobookPlaybackOptions {
  contentId: string;
  files: AudiobookFile[];
  initialPositionSeconds: number;
  initialChapter?: AudiobookChapterIntent;
  autoPlay?: boolean;
  smartRewindEnabled?: boolean;
  onStopRequested?: () => void;
}

export interface AudiobookPlayback {
  audioRef: RefObject<HTMLAudioElement | null>;
  streamUrl: string;
  hasFile: boolean;
  playing: boolean;
  currentTime: number;
  duration: number;
  buffered: TimeRanges | null;
  rate: number;
  chapters: PlayerChapter[];
  currentChapter: PlayerChapter | null;
  volume: number;
  muted: boolean;
  togglePlay: () => void;
  stopForReplacement: () => Promise<void>;
  seekTo: (seconds: number) => void;
  skip: (delta: number) => void;
  setRate: (r: number) => void;
  setVolume: (volume: number) => void;
  setMuted: (muted: boolean) => void;
  nextChapter: () => void;
  prevChapter: () => void;
  sleep: { setting: SleepSetting; remainingMs: number | null };
  setSleep: (next: SleepSetting) => void;
}

interface AudiobookSessionState {
  sessionId: string | null;
  streamUrl: string;
}

interface AudiobookPart {
  file: AudiobookFile;
  start: number;
  end: number;
}

function safeNumber(value: number): number {
  return Number.isFinite(value) && value >= 0 ? value : 0;
}

export function audiobookAbsoluteTime(
  partStartSeconds: number,
  timelineOffsetSeconds: number,
  playerSeconds: number,
): number {
  const timelineOffset = Number.isFinite(timelineOffsetSeconds) ? timelineOffsetSeconds : 0;
  return Math.max(0, safeNumber(partStartSeconds) + timelineOffset + safeNumber(playerSeconds));
}

function buildParts(files: AudiobookFile[]): AudiobookPart[] {
  const parts: AudiobookPart[] = [];
  let offset = 0;
  for (const file of files) {
    const duration = safeNumber(file.duration_seconds ?? 0);
    parts.push({ file, start: offset, end: offset + duration });
    offset += duration;
  }
  return parts;
}

function totalDuration(parts: AudiobookPart[]): number {
  return parts.reduce((max, part) => Math.max(max, part.end), 0);
}

function clampedBookTime(seconds: number, duration: number): number {
  const value = safeNumber(seconds);
  if (duration <= 0) {
    return value;
  }
  return Math.max(0, Math.min(value, duration));
}

function findPartIndex(parts: AudiobookPart[], seconds: number): number {
  if (parts.length === 0) {
    return -1;
  }
  const time = safeNumber(seconds);
  const index = parts.findIndex(
    (part) => time >= part.start && time < Math.max(part.end, part.start + 1),
  );
  if (index >= 0) {
    return index;
  }
  return time >= parts[parts.length - 1]!.end ? parts.length - 1 : 0;
}

function localTimeForPart(part: AudiobookPart | undefined, absoluteSeconds: number): number {
  if (!part) {
    return 0;
  }
  const duration = safeNumber(part.file.duration_seconds ?? 0);
  const local = safeNumber(absoluteSeconds) - part.start;
  if (duration <= 0) {
    return Math.max(0, local);
  }
  return Math.max(0, Math.min(local, duration));
}

function absoluteBufferedRanges(
  ranges: TimeRanges,
  part: AudiobookPart | undefined,
  bookDuration: number,
  timelineOffsetSeconds = 0,
): TimeRanges {
  if (!part) {
    return { length: 0, start: () => 0, end: () => 0 } as TimeRanges;
  }
  const timelineOffset = Number.isFinite(timelineOffsetSeconds) ? timelineOffsetSeconds : 0;
  const out: Array<{ start: number; end: number }> = [];
  for (let i = 0; i < ranges.length; i++) {
    const start = Math.max(
      0,
      Math.min(bookDuration, part.start + timelineOffset + safeNumber(ranges.start(i))),
    );
    const end = Math.max(
      0,
      Math.min(bookDuration, part.start + timelineOffset + safeNumber(ranges.end(i))),
    );
    if (end > start) {
      out.push({ start, end });
    }
  }
  return {
    length: out.length,
    start(index: number) {
      const range = out[index];
      if (!range) throw new Error("TimeRanges index out of bounds");
      return range.start;
    },
    end(index: number) {
      const range = out[index];
      if (!range) throw new Error("TimeRanges index out of bounds");
      return range.end;
    },
  } as TimeRanges;
}

function readNumericPayload(
  payload: Record<string, unknown> | undefined,
  ...keys: string[]
): number | null {
  if (!payload) {
    return null;
  }
  for (const key of keys) {
    const value = payload[key];
    if (typeof value === "number" && Number.isFinite(value)) {
      return value;
    }
  }
  return null;
}

function readStringPayload(
  payload: Record<string, unknown> | undefined,
  key: string,
): string | null {
  const value = payload?.[key];
  return typeof value === "string" && value.trim().length > 0 ? value.trim() : null;
}

export function useAudiobookPlayback({
  contentId,
  files,
  initialPositionSeconds,
  initialChapter,
  autoPlay = true,
  smartRewindEnabled = true,
  onStopRequested,
}: UseAudiobookPlaybackOptions): AudiobookPlayback {
  const config = usePlayerConfig();
  // One player mount belongs to its original account/profile. A config change
  // cannot transfer the old book or its pending requests to a new viewer.
  const authority = useRef(config.capturePlaybackMutationContext?.()).current;
  const partTransitionRef = useRef<object | null>(null);
  const replacementTransitionRef = useRef<object | null>(null);
  const endedRef = useRef(false);
  const lifetimeRef = useRef(0);
  useEffect(
    () => () => {
      lifetimeRef.current += 1;
    },
    [],
  );
  // The same probe the video player uses: its audio codecs are already tested
  // against `audio/mp4` as well as `video/mp4`, so an audio-only source is
  // described honestly without a second detection path.
  const capabilityProbe = useCodecDetection();
  const capabilitiesSettled = capabilityProbe.settled;
  const clientCapabilities = useMemo(
    () => buildClientCapabilitiesV3(capabilityProbe),
    [capabilityProbe],
  );
  const clientPlaybackContext = useMemo(
    () => buildClientPlaybackContextV3(capabilityProbe),
    [capabilityProbe],
  );
  const audioRef = useRef<HTMLAudioElement>(null);
  const queryClient = useQueryClient();
  const intent = useRef({
    config,
    contentId,
    files,
    initialPositionSeconds,
    autoPlay,
    initialChapter: initialChapter ? { ...initialChapter } : undefined,
  }).current;
  const initialTargetRef = useRef(initialPositionSeconds);
  const [manifest, setManifest] = useState<Readonly<PlaybackTimelineManifest> | null>(null);
  const discovery = useRef<ReturnType<typeof discoverAudiobookTimeline> | null>(null);
  useEffect(() => {
    let canceled = false;
    if (!authority?.isCurrent() || !intent.files[0]) return;
    discovery.current ??= discoverAudiobookTimeline(
      intent.config,
      authority,
      intent.contentId,
      intent.initialChapter?.fileId ?? String(intent.files[0].id),
    );
    void discovery.current
      .then((snapshot) => {
        if (!canceled && authority.isCurrent()) {
          if (intent.initialChapter) {
            const chapter = intent.initialChapter;
            const part = snapshot.parts.find((part) => part.file_id === chapter.fileId);
            if (
              !part ||
              !Number.isFinite(chapter.positionSeconds) ||
              chapter.positionSeconds < 0 ||
              chapter.positionSeconds >= part.duration_seconds
            )
              throw new Error("The selected chapter is outside this audiobook timeline");
            initialTargetRef.current = part.offset_seconds + chapter.positionSeconds;
          }
          setManifest(snapshot);
        }
      })
      .catch((error) => {
        if (!canceled && authority.isCurrent())
          toast.error(error instanceof Error ? error.message : "Audiobook timeline unavailable");
      });
    return () => {
      canceled = true;
    };
  }, [authority, intent]);
  const timelineFiles = useMemo(
    () =>
      manifest?.parts.map((part) => {
        const detail = intent.files.find((file) => String(file.id) === part.file_id);
        return {
          ...detail,
          id: Number(part.file_id),
          duration_seconds: part.duration_seconds,
          chapters: detail?.chapters
            ?.filter(
              (chapter) =>
                chapter.start_seconds >= 0 && chapter.start_seconds < part.duration_seconds,
            )
            .map((chapter) => ({
              ...chapter,
              end_seconds: Math.min(chapter.end_seconds, part.duration_seconds),
            })),
        };
      }) ?? [],
    [manifest, intent],
  );
  const parts = useMemo(() => buildParts(timelineFiles), [timelineFiles]);
  const duration = useMemo(() => totalDuration(parts), [parts]);
  const chapters = useMemo(() => buildPlayerChapters(timelineFiles), [timelineFiles]);
  const [activeFileIndex, setActiveFileIndex] = useState(() => {
    return findPartIndex(parts, initialPositionSeconds);
  });
  const [playing, setPlaying] = useState(false);
  const [currentTime, setCurrentTime] = useState(() =>
    clampedBookTime(initialPositionSeconds, duration),
  );
  const [buffered, setBuffered] = useState<TimeRanges | null>(null);
  const [rate, setRateState] = useState(() => getBookRate(contentId) ?? 1);
  const [volume, setVolumeState] = useState(() => getPersistedVolume().volume);
  const [muted, setMutedState] = useState(() => getPersistedVolume().muted);
  const [sessionState, setSessionState] = useState<AudiobookSessionState>({
    sessionId: null,
    streamUrl: "",
  });
  const [sourceRevision, setSourceRevision] = useState(0);

  const refreshAcceptedProgress = useCallback(
    (accepted: BoundAcceptedProgress | void) => {
      if (!accepted || !authority?.isCurrent()) return;
      // The server stored item_position; never replace the live audio clock with a receipt.
      void queryClient.invalidateQueries({ queryKey: progressKeys.all });
      void queryClient.invalidateQueries({
        predicate: ({ queryKey: key }) =>
          (key[0] === "catalog" &&
            key[1] === "items" &&
            key[2] === intent.contentId &&
            key[3] === "detail") ||
          (key[0] === "sections" && key.includes("items")),
      });
    },
    [authority, intent, queryClient],
  );
  const activePart = activeFileIndex >= 0 ? parts[activeFileIndex] : undefined;
  const fileId = activePart?.file.id;
  const currentTimeRef = useRef(currentTime);
  const activePartRef = useRef<AudiobookPart | undefined>(activePart);
  const sessionIdRef = useRef<string | null>(null);
  const planRef = useRef<PlanV3 | null>(null);
  const playbackAttemptIdRef = useRef<string | null>(null);
  const planAttemptIdRef = useRef(randomUUID());
  const attemptedPlanKeysRef = useRef<string[]>([]);
  const attemptCountRef = useRef(1);
  const replanInFlightPlanKeyRef = useRef<string | null>(null);
  const failedPlanKeyRef = useRef<string | null>(null);
  const timelineOffsetSecondsRef = useRef(0);
  const canSeekAnywhereRef = useRef(true);
  const reportRef = useRef<(pos: number) => void>(() => {});
  const reportSessionRef = useRef<(pos: number, isPaused: boolean, keepalive?: boolean) => void>(
    () => {},
  );
  const pendingLocalSeekRef = useRef<number | null>(
    localTimeForPart(activePart, clampedBookTime(initialPositionSeconds, duration)),
  );
  const playAfterSourceSwitchRef = useRef(false);
  const autoPlayPendingRef = useRef(autoPlay);
  const playingRef = useRef(false);
  // Wall-clock time playback last paused, for smart rewind on resume. Cleared
  // by explicit seeks so a hand-picked position is never second-guessed.
  const pausedAtRef = useRef<number | null>(null);

  const setAbsoluteTime = useCallback(
    (seconds: number) => {
      const next = clampedBookTime(seconds, duration);
      currentTimeRef.current = next;
      setCurrentTime(next);
    },
    [duration],
  );

  useEffect(() => {
    activePartRef.current = activePart;
  }, [activePart]);

  const reportRouteEvent = useCallback(
    (input: RouteEventInput) => {
      if (!authority?.isCurrent() || !input.sessionId) return;
      void reportDurableRouteEvent(config, input.sessionId, buildRouteEventV3(input));
    },
    [authority, config],
  );

  const stopSession = useCallback(
    (sessionId: string, keepalive = false) => {
      if (!durableSessionFor(sessionId)) return;
      void stopSequencedSession(config, sessionId, keepalive).catch(() => {
        // The shared durable helper preserves the request and offers recovery.
      });
    },
    [config],
  );

  const adoptPlan = useCallback(
    (decision: DecisionResponseV3, playbackAttemptId: string): string | null => {
      const plan = decision.playback_plan;
      if (!plan) return null;
      if (!manifest || !activePartRef.current) throw new Error("Audiobook timeline unavailable");
      const selectedFile = String(activePartRef.current.file.id);
      validateSelectedTimeline(decision.progress_timeline, manifest, selectedFile);
      if (
        String(plan.effective_media_file_id) !== selectedFile ||
        String(plan.requested_media_file_id) !== selectedFile
      )
        throw new Error("Audiobook recovery changed the selected part");

      const sessionId = plan.session_id ?? decision.session_id ?? sessionIdRef.current;
      const planAttemptId = randomUUID();
      endedRef.current = false;
      planRef.current = plan;
      playbackAttemptIdRef.current = playbackAttemptId;
      planAttemptIdRef.current = planAttemptId;
      sessionIdRef.current = sessionId ?? null;
      const durable = sessionId ? durableSessionFor(sessionId) : undefined;
      if (durable) durable.onAccepted = refreshAcceptedProgress;
      if (sessionId)
        captureSessionProgress(sessionId, {
          position: plan.timeline.source_start_seconds,
          is_paused: true,
        });
      failedPlanKeyRef.current = null;
      timelineOffsetSecondsRef.current = plan.timeline.timeline_offset_seconds;
      canSeekAnywhereRef.current = plan.timeline.can_seek_anywhere;
      // The plan owns where the stream is anchored. On a converted part the
      // server anchors the stream at the seek position and restarts the player
      // clock at zero, so replaying the requested position would seek twice.
      pendingLocalSeekRef.current = plan.timeline.player_start_seconds;
      setSessionState({
        sessionId: sessionId ?? null,
        streamUrl: buildPlayerStreamUrl(
          config.apiBaseUrl,
          plan.stream.url,
          config.getAccessToken(),
        ),
      });
      reportRouteEvent({
        event: "plan_selected",
        playbackAttemptId,
        ...(sessionId ? { sessionId } : {}),
        planId: plan.plan_id,
        planAttemptId,
        planAttemptKey: plan.plan_attempt_key,
      });
      return sessionId ?? null;
    },
    [config, manifest, refreshAcceptedProgress, reportRouteEvent],
  );

  const recoverFromPlanFailure = useCallback(
    async (failure: FailureV3) => {
      if (!authority?.isCurrent() || partTransitionRef.current || endedRef.current) return;
      const plan = planRef.current;
      const sessionId = sessionIdRef.current;
      const playbackAttemptId = playbackAttemptIdRef.current;
      if (!plan || !sessionId || !playbackAttemptId || replanInFlightPlanKeyRef.current) return;
      if (failedPlanKeyRef.current === plan.plan_attempt_key) return;
      if (attemptCountRef.current > MAX_ATTEMPT_COUNT_V3) {
        toast.error("Playback failed", {
          description: "Audiobook playback failed after repeated recovery attempts.",
        });
        return;
      }

      const expectedPlanKey = plan.plan_attempt_key;
      const planAttemptId = planAttemptIdRef.current;
      const attemptedPlanKeys = [...attemptedPlanKeysRef.current, expectedPlanKey].slice(
        -MAX_ATTEMPTED_PLAN_KEYS_V3,
      );
      const attemptCount = attemptCountRef.current;
      const positionSeconds = localTimeForPart(activePartRef.current, currentTimeRef.current);
      failedPlanKeyRef.current = expectedPlanKey;
      replanInFlightPlanKeyRef.current = expectedPlanKey;

      reportRouteEvent({
        event: "plan_failed",
        playbackAttemptId,
        sessionId,
        planId: plan.plan_id,
        planAttemptId,
        planAttemptKey: expectedPlanKey,
        failureClassification: failure.classification,
        ...(failure.message ? { diagnostics: { message: failure.message } } : {}),
      });

      try {
        const decision = await replanDurableSession(
          config,
          sessionId,
          buildReplanRequestV3({
            operation: "failure_recovery",
            extraClientFeatures: ["bound_client_timeline"],
            positionSeconds,
            failure,
            plan,
            playbackAttemptId,
            replanRequestId: randomUUID(),
            planAttemptId,
            qualityPreference: QUALITY_ORIGINAL_V3,
            attemptedPlanKeys,
            attemptCount,
            metered: detectMeteredV3(),
            bandwidthEstimateKbps: detectBandwidthEstimateKbpsV3(),
            clientCapabilities,
            clientPlaybackContext,
          }),
        );

        if (
          planRef.current?.plan_attempt_key !== expectedPlanKey ||
          playbackAttemptIdRef.current !== playbackAttemptId
        ) {
          return;
        }
        attemptedPlanKeysRef.current = attemptedPlanKeys;
        attemptCountRef.current = Math.min(attemptCount + 1, MAX_ATTEMPT_COUNT_V3 + 1);

        if (!decision.playback_plan) {
          const terminal = decision.terminal
            ? describePlanTerminal(decision.terminal)
            : {
                title: "Playback unavailable",
                message: "This server could not recover audiobook playback.",
              };
          reportRouteEvent({
            event: "terminal",
            playbackAttemptId,
            sessionId,
            planId: plan.plan_id,
            planAttemptId,
            planAttemptKey: expectedPlanKey,
            ...(decision.terminal ? { fallbackReason: decision.terminal.reason } : {}),
          });
          toast.error(terminal.title, { description: terminal.message });
          return;
        }

        playAfterSourceSwitchRef.current =
          playingRef.current || autoPlayPendingRef.current || !(audioRef.current?.paused ?? true);
        setBuffered(null);
        adoptPlan(decision, playbackAttemptId);
      } catch (err) {
        if (!authority?.isCurrent()) return;
        if (
          planRef.current?.plan_attempt_key === expectedPlanKey &&
          playbackAttemptIdRef.current === playbackAttemptId &&
          failedPlanKeyRef.current === expectedPlanKey
        ) {
          failedPlanKeyRef.current = null;
        }
        console.error("audiobook playback recovery failed", err);
        toast.error(err instanceof Error ? err.message : "Failed to recover audiobook playback");
      } finally {
        if (replanInFlightPlanKeyRef.current === expectedPlanKey) {
          replanInFlightPlanKeyRef.current = null;
        }
      }
    },
    [adoptPlan, authority, clientCapabilities, clientPlaybackContext, config, reportRouteEvent],
  );

  useEffect(() => {
    reportRef.current = (posSeconds: number) => {
      if (!authority?.isCurrent()) return;
      reportSessionRef.current(posSeconds, audioRef.current?.paused ?? true);
    };
  }, [authority]);

  useEffect(() => {
    reportSessionRef.current = (posSeconds: number, isPaused: boolean, keepalive = false) => {
      const sessionId = sessionIdRef.current;
      const part = activePartRef.current;
      if (!sessionId || !part) {
        return;
      }
      if (!authority?.isCurrent() || !durableSessionFor(sessionId)) return;
      const sample = { position: localTimeForPart(part, posSeconds), is_paused: isPaused };
      captureSessionProgress(sessionId, sample);
      void sendSessionProgress(config, sessionId, sample, keepalive).catch(() => {
        // The shared journal retains the exact unconfirmed sample.
      });
    };
  }, [authority, config, refreshAcceptedProgress]);

  useEffect(() => {
    const target = clampedBookTime(initialTargetRef.current, duration);
    const index = findPartIndex(parts, target);
    pendingLocalSeekRef.current = localTimeForPart(parts[index], target);
    timelineOffsetSecondsRef.current = 0;
    canSeekAnywhereRef.current = true;
    autoPlayPendingRef.current = autoPlay;
    setBuffered(null);
    setActiveFileIndex(index);
    currentTimeRef.current = target;
    setCurrentTime(target);
  }, [autoPlay, contentId, duration, parts]);

  useEffect(() => {
    if (!fileId || !activePart) {
      setSessionState({ sessionId: null, streamUrl: "" });
      sessionIdRef.current = null;
      planRef.current = null;
      playbackAttemptIdRef.current = null;
      return;
    }
    if (!capabilitiesSettled || !manifest) return;

    let canceled = false;
    let startedSessionId: string | null = null;
    const localStart =
      pendingLocalSeekRef.current ?? localTimeForPart(activePart, currentTimeRef.current);

    setSessionState({ sessionId: null, streamUrl: "" });

    const playbackAttemptId = randomUUID();
    playbackAttemptIdRef.current = playbackAttemptId;
    planRef.current = null;
    attemptedPlanKeysRef.current = [];
    attemptCountRef.current = 1;
    replanInFlightPlanKeyRef.current = null;
    failedPlanKeyRef.current = null;

    (async () => {
      const profileId = config.getProfileId();
      if (!profileId) {
        throw new Error("Missing active profile");
      }

      if (!authority?.isCurrent()) throw new Error("Playback identity changed");
      const decision = await startInitialPlayback(
        config,
        buildStartRequestV3({
          fileId,
          profileId,
          playbackAttemptId,
          // An audiobook has one rung. Asking for anything else would only
          // invite the planner to consider a ladder that does not exist.
          qualityPreference: QUALITY_ORIGINAL_V3,
          position: localStart,
          // The book's absolute position is already resolved to this part's
          // local clock, so zero means "the start of this part" rather than
          // "resume wherever the server last saw us".
          forceStartPosition: true,
          progressPersistence: "client_bound",
          timelineId: manifest.timeline_id,
          extraClientFeatures: ["bound_client_timeline"],
          metered: detectMeteredV3(),
          bandwidthEstimateKbps: detectBandwidthEstimateKbpsV3() ?? null,
          clientCapabilities,
          clientPlaybackContext,
        }),
        manifest.installation_id,
        {
          timeline_id: manifest.timeline_id,
          media_item_id: manifest.media_item_id,
          file_id: String(fileId),
          part_offset_seconds: activePart.start,
          part_duration_seconds: activePart.end - activePart.start,
          duration_seconds: manifest.duration_seconds,
        },
      );

      const plan = decision.playback_plan;
      if (!plan) {
        if (decision.session_id) stopSession(decision.session_id, true);
        if (!canceled && authority?.isCurrent()) {
          const failure = decision.terminal
            ? describePlanTerminal(decision.terminal)
            : {
                title: "Playback unavailable",
                message: "This server is not accepting playback requests right now.",
              };
          reportRouteEvent({
            event: "terminal",
            playbackAttemptId,
            ...(decision.session_id ? { sessionId: decision.session_id } : {}),
            ...(decision.terminal ? { fallbackReason: decision.terminal.reason } : {}),
          });
          toast.error(failure.title, { description: failure.message });
        }
        return;
      }

      const sessionId = plan.session_id ?? decision.session_id ?? null;
      if (canceled || !authority?.isCurrent()) {
        if (sessionId) stopSession(sessionId, true);
        return;
      }

      startedSessionId = adoptPlan(decision, playbackAttemptId);
    })().catch((err) => {
      if (!canceled && authority?.isCurrent()) {
        console.error("audiobook playback session failed", err);
        reportRouteEvent({
          event: "plan_failed",
          playbackAttemptId,
          failureClassification: "transport_error",
        });
        toast.error(err instanceof Error ? err.message : "Failed to start audiobook playback");
      }
    });

    return () => {
      canceled = true;
      if (startedSessionId) {
        stopSession(startedSessionId, true);
        if (sessionIdRef.current === startedSessionId) {
          sessionIdRef.current = null;
        }
      }
      if (playbackAttemptIdRef.current === playbackAttemptId) {
        playbackAttemptIdRef.current = null;
        planRef.current = null;
      }
    };
  }, [
    activePart,
    authority,
    adoptPlan,
    clientCapabilities,
    clientPlaybackContext,
    capabilitiesSettled,
    config,
    fileId,
    manifest,
    sourceRevision,
    stopSession,
    reportRouteEvent,
  ]);

  const stopForReplacement = useCallback(async () => {
    if (!authority?.isCurrent()) throw new Error("Playback identity changed");
    if (partTransitionRef.current && partTransitionRef.current !== replacementTransitionRef.current)
      throw new Error("The current audiobook part change is still pending");
    const sessionId = sessionIdRef.current;
    // No session was adopted. Unmount cleanup and the durable initial-start
    // journal still fence any late or uncertain start before another dispatch.
    if (!sessionId) return;
    const binding = durableSessionFor(sessionId);
    if (!binding) throw new Error("Audiobook playback authority is unavailable");
    const lifetime = lifetimeRef.current;
    replacementTransitionRef.current ??= {};
    partTransitionRef.current = replacementTransitionRef.current;
    audioRef.current?.pause();
    playingRef.current = false;
    setPlaying(false);
    // The provider owns retry and retains the chapter intent while this exact
    // stop is unknown. Keep this player mounted until the receipt is durable.
    await stopSequencedSession({ ...config, onPlaybackStopError: undefined }, sessionId);
    if (!authority.isCurrent() || lifetimeRef.current !== lifetime)
      throw new Error("Playback identity changed while stopping");
    if (hasDurableTermination(binding)) throw new Error("Audiobook playback was terminated");
  }, [authority, config]);

  const transitionPart = useCallback(
    (nextIndex: number, target: number, local: number, resume: boolean) => {
      if (partTransitionRef.current || !authority?.isCurrent()) return;
      const sessionId = sessionIdRef.current;
      if (!sessionId || !durableSessionFor(sessionId)) {
        toast.error("Audiobook start unconfirmed", {
          description: "Resolve the current playback start before changing parts.",
        });
        return;
      }
      const transition = {};
      const lifetime = lifetimeRef.current;
      partTransitionRef.current = transition;
      audioRef.current?.pause();
      playingRef.current = false;
      setPlaying(false);
      const isCurrent = () =>
        partTransitionRef.current === transition &&
        lifetimeRef.current === lifetime &&
        authority.isCurrent();
      const finish = async () => {
        if (!isCurrent()) return;
        try {
          // A failed stop retains its exact command. This transition owns the
          // retry action so success resumes this intent, not a different seek.
          await stopSequencedSession({ ...config, onPlaybackStopError: undefined }, sessionId);
          const binding = durableSessionFor(sessionId);
          if (!binding || hasDurableTermination(binding))
            throw new Error("Audiobook playback was terminated");
        } catch (error) {
          if (isCurrent())
            toast.error("Audiobook part change pending", {
              description:
                error instanceof Error ? error.message : "The current part has not stopped.",
              action: {
                label: "Retry",
                onClick: () => {
                  void finish();
                },
              },
            });
          return;
        }
        if (!isCurrent()) return;
        partTransitionRef.current = null;
        pendingLocalSeekRef.current = local;
        playAfterSourceSwitchRef.current = resume;
        setBuffered(null);
        setAbsoluteTime(target);
        if (nextIndex === activeFileIndex) setSourceRevision((revision) => revision + 1);
        else setActiveFileIndex(nextIndex);
      };
      void finish();
    },
    [activeFileIndex, authority, config, setAbsoluteTime],
  );

  useEffect(() => {
    const audio = audioRef.current;
    if (!audio || !fileId || !activePart) return;

    const absoluteFromAudio = () =>
      audiobookAbsoluteTime(activePart.start, timelineOffsetSecondsRef.current, audio.currentTime);

    const onTimeUpdate = () => {
      const absolute = absoluteFromAudio();
      setAbsoluteTime(absolute);
      const sessionId = sessionIdRef.current;
      if (sessionId && authority?.isCurrent())
        captureSessionProgress(sessionId, {
          position: localTimeForPart(activePart, absolute),
          is_paused: audio.paused,
        });
    };
    const onProgress = () =>
      setBuffered(
        absoluteBufferedRanges(
          audio.buffered,
          activePart,
          duration,
          timelineOffsetSecondsRef.current,
        ),
      );
    const onDurationChange = () =>
      setBuffered(
        absoluteBufferedRanges(
          audio.buffered,
          activePart,
          duration,
          timelineOffsetSecondsRef.current,
        ),
      );
    const onLoadedMetadata = () => {
      audio.playbackRate = rate;
      const pending = pendingLocalSeekRef.current;
      const local = pending ?? localTimeForPart(activePart, currentTimeRef.current);
      if (local > 0) {
        const max = Number.isFinite(audio.duration) ? Math.max(0, audio.duration - 1) : local;
        audio.currentTime = Math.min(local, max);
      }
      pendingLocalSeekRef.current = null;
      const shouldPlay = autoPlayPendingRef.current || playAfterSourceSwitchRef.current;
      autoPlayPendingRef.current = false;
      playAfterSourceSwitchRef.current = false;
      if (shouldPlay) {
        audio.play().catch((err) => {
          console.warn("audiobook autoplay blocked", err);
        });
      }
    };
    const onPlay = () => {
      playingRef.current = true;
      setPlaying(true);
      reportSessionRef.current(absoluteFromAudio(), false);
    };
    const onPause = () => {
      playingRef.current = false;
      pausedAtRef.current = performance.now();
      setPlaying(false);
      reportRef.current(absoluteFromAudio());
    };
    const onSeeked = () => {
      const absolute = absoluteFromAudio();
      setAbsoluteTime(absolute);
      reportRef.current(absolute);
    };
    const onEnded = () => {
      const nextIndex = activeFileIndex + 1;
      if (nextIndex < parts.length) {
        const nextPart = parts[nextIndex];
        if (nextPart) {
          const sessionId = sessionIdRef.current;
          if (sessionId)
            captureSessionProgress(sessionId, {
              position: activePart.end - activePart.start,
              is_paused: true,
            });
          transitionPart(nextIndex, nextPart.start, 0, true);
          return;
        }
      }
      currentTimeRef.current = duration;
      setCurrentTime(duration);
      playingRef.current = false;
      setPlaying(false);
      endedRef.current = true;
      reportRef.current(duration);
      if (sessionIdRef.current) stopSession(sessionIdRef.current);
    };
    const onError = () => {
      const err = audio.error;
      const message = err?.message || "The browser rejected the audiobook stream.";
      console.error("audiobook audio error", {
        code: err?.code,
        message: err?.message,
        networkState: audio.networkState,
        readyState: audio.readyState,
        src: audio.currentSrc,
      });
      void recoverFromPlanFailure({ classification: "decoder_error", message });
    };

    audio.addEventListener("timeupdate", onTimeUpdate);
    audio.addEventListener("progress", onProgress);
    audio.addEventListener("durationchange", onDurationChange);
    audio.addEventListener("loadedmetadata", onLoadedMetadata);
    audio.addEventListener("play", onPlay);
    audio.addEventListener("pause", onPause);
    audio.addEventListener("seeked", onSeeked);
    audio.addEventListener("ended", onEnded);
    audio.addEventListener("error", onError);

    return () => {
      audio.removeEventListener("timeupdate", onTimeUpdate);
      audio.removeEventListener("progress", onProgress);
      audio.removeEventListener("durationchange", onDurationChange);
      audio.removeEventListener("loadedmetadata", onLoadedMetadata);
      audio.removeEventListener("play", onPlay);
      audio.removeEventListener("pause", onPause);
      audio.removeEventListener("seeked", onSeeked);
      audio.removeEventListener("ended", onEnded);
      audio.removeEventListener("error", onError);
    };
  }, [
    activeFileIndex,
    activePart,
    authority,
    duration,
    fileId,
    parts,
    rate,
    recoverFromPlanFailure,
    setAbsoluteTime,
    transitionPart,
    stopSession,
  ]);

  useEffect(() => {
    if (!playing) return;
    const id = window.setInterval(() => {
      reportRef.current(currentTimeRef.current);
    }, REPORT_INTERVAL_MS);
    return () => window.clearInterval(id);
  }, [playing]);

  useEffect(() => {
    const audio = audioRef.current;
    if (!audio) return;
    audio.volume = volume;
    audio.muted = muted;
    // sessionState.streamUrl keeps this in the deps so a freshly mounted
    // element picks the persisted values up before playback starts.
  }, [muted, sessionState.streamUrl, volume]);

  useEffect(() => {
    const audio = audioRef.current;
    return () => {
      if (audio && !audio.paused) {
        audio.pause();
        reportRef.current(currentTimeRef.current);
      }
    };
  }, []);

  const seekTo = useCallback(
    (seconds: number, resumeAfterSeek = false) => {
      pausedAtRef.current = null;
      const target = clampedBookTime(seconds, duration);
      const nextIndex = findPartIndex(parts, target);
      const nextPart = parts[nextIndex];
      const audio = audioRef.current;
      const shouldContinuePlaying = resumeAfterSeek || (audio ? !audio.paused : playing);
      const local = localTimeForPart(nextPart, target);

      if (partTransitionRef.current || !authority?.isCurrent()) return;
      if (
        endedRef.current ||
        nextIndex !== activeFileIndex ||
        !audio ||
        !canSeekAnywhereRef.current
      ) {
        reportSessionRef.current(currentTimeRef.current, true);
        transitionPart(nextIndex, target, local, shouldContinuePlaying);
        return;
      }
      audio.currentTime = Math.max(0, local - timelineOffsetSecondsRef.current);
      reportRef.current(target);
      currentTimeRef.current = target;
      setCurrentTime(target);
    },
    [activeFileIndex, authority, duration, parts, playing, transitionPart],
  );

  const resumePlayback = useCallback(() => {
    if (partTransitionRef.current || !authority?.isCurrent() || endedRef.current) return;
    const audio = audioRef.current;
    if (!audio) return;
    const pausedAtMs = pausedAtRef.current;
    pausedAtRef.current = null;
    if (smartRewindEnabled && pausedAtMs != null) {
      const rewind = smartRewindSeconds(performance.now() - pausedAtMs);
      if (rewind > 0) {
        const target = clampedBookTime(currentTimeRef.current - rewind, duration);
        const targetIndex = findPartIndex(parts, target);
        seekTo(target, true);
        if (targetIndex !== activeFileIndex || partTransitionRef.current) {
          // The transition captured this explicit Play intent. It resumes only
          // after the terminal old-part receipt and new-source metadata.
          return;
        }
      }
    }
    audio.play().catch((err) => console.error("audiobook play failed", err));
  }, [activeFileIndex, authority, duration, parts, seekTo, smartRewindEnabled]);

  const togglePlay = useCallback(() => {
    const audio = audioRef.current;
    if (!audio) return;
    if (audio.paused) {
      resumePlayback();
    } else {
      audio.pause();
    }
  }, [resumePlayback]);

  const skip = useCallback(
    (delta: number) => {
      seekTo(currentTimeRef.current + delta);
    },
    [seekTo],
  );

  const setRate = useCallback(
    (nextRate: number) => {
      const clamped = clampAudiobookRate(nextRate);
      setRateState(clamped);
      setBookRate(contentId, clamped);
      if (audioRef.current) audioRef.current.playbackRate = clamped;
    },
    [contentId],
  );

  const setVolume = useCallback(
    (next: number) => {
      const clamped = Math.min(1, Math.max(0, Number.isFinite(next) ? next : 1));
      setVolumeState(clamped);
      persistVolume(clamped, muted);
    },
    [muted],
  );

  const setMuted = useCallback(
    (next: boolean) => {
      setMutedState(next);
      persistVolume(volume, next);
    },
    [volume],
  );

  const executeRealtimeCommand = useCallback(
    async (command: PlaybackRealtimeCommandEnvelope) => {
      const audio = audioRef.current;

      switch (command.name) {
        case "pause":
          audio?.pause();
          return;
        case "unpause":
          if (!audio) return;
          resumePlayback();
          return;
        case "play_pause":
          if (!audio) return;
          if (audio.paused) {
            resumePlayback();
          } else {
            audio.pause();
          }
          return;
        case "seek": {
          const position = readNumericPayload(
            command.payload,
            "position",
            "position_seconds",
            "seconds",
          );
          if (position === null) {
            throw new Error("missing_seek_position");
          }
          seekTo(position);
          return;
        }
        case "set_volume": {
          const nextVolume = readNumericPayload(command.payload, "volume", "level");
          if (nextVolume === null || !audio) {
            throw new Error("missing_volume");
          }
          setVolume(nextVolume);
          if (nextVolume > 0) {
            setMuted(false);
          }
          return;
        }
        case "display_message":
          toast.info(
            readStringPayload(command.payload, "message") ?? "A server message was received.",
          );
          return;
        case "server_restarting":
        case "server_shutting_down":
          toast.warning(
            readStringPayload(command.payload, "message") ??
              (command.name === "server_restarting"
                ? "Playback may end shortly while the server restarts."
                : "Playback may end shortly while the server shuts down."),
          );
          return;
        case "stop":
        case "terminate":
          audio?.pause();
          if (audio) {
            audio.removeAttribute("src");
            audio.load();
          }
          window.setTimeout(() => onStopRequested?.(), 0);
          return;
        default:
          throw new Error("unsupported");
      }
    },
    [onStopRequested, resumePlayback, seekTo, setMuted, setVolume],
  );

  usePlaybackRealtime({
    sessionId: sessionState.sessionId,
    onCommand: executeRealtimeCommand,
  });

  const currentChapter = useMemo(() => {
    if (chapters.length === 0) return null;
    for (let i = chapters.length - 1; i >= 0; i--) {
      const chapter = chapters[i];
      if (chapter && currentTime >= chapter.start_seconds) {
        return chapter;
      }
    }
    return chapters[0] ?? null;
  }, [chapters, currentTime]);

  const nextChapter = useCallback(() => {
    const target = nextChapterStart(chapters, currentChapter);
    if (target != null) seekTo(target);
  }, [chapters, currentChapter, seekTo]);

  const prevChapter = useCallback(() => {
    const target = prevChapterStart(chapters, currentChapter, currentTimeRef.current);
    if (target != null) seekTo(target);
  }, [chapters, currentChapter, seekTo]);

  const [sleepSetting, setSleepSetting] = useState<SleepSetting>({ kind: "off" });
  const [sleepTargetMs, setSleepTargetMs] = useState<number | null>(null);
  const [sleepChapterEndSeconds, setSleepChapterEndSeconds] = useState<number | null>(null);
  const [sleepNowMs, setSleepNowMs] = useState<number>(() => Date.now());

  useEffect(() => {
    if (sleepSetting.kind !== "duration") {
      setSleepTargetMs(null);
      return;
    }
    setSleepChapterEndSeconds(null);
    setSleepTargetMs(Date.now() + sleepSetting.seconds * 1000);
  }, [sleepSetting]);

  useEffect(() => {
    if (sleepSetting.kind !== "end-of-chapter") {
      setSleepChapterEndSeconds(null);
      return;
    }
    setSleepTargetMs(null);
    setSleepChapterEndSeconds((current) => {
      if (current != null && current > currentTime) return current;
      return currentChapter?.end_seconds ?? null;
    });
  }, [currentChapter, currentTime, sleepSetting]);

  useEffect(() => {
    if (sleepTargetMs == null) return;
    const id = window.setInterval(() => setSleepNowMs(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [sleepTargetMs]);

  useEffect(() => {
    if (sleepTargetMs == null) return;
    if (sleepNowMs < sleepTargetMs) return;
    const audio = audioRef.current;
    if (audio && !audio.paused) audio.pause();
    setSleepSetting({ kind: "off" });
    setSleepTargetMs(null);
  }, [sleepNowMs, sleepTargetMs]);

  useEffect(() => {
    if (sleepSetting.kind !== "end-of-chapter" || sleepChapterEndSeconds == null) return;
    if (currentTime < sleepChapterEndSeconds) return;
    const audio = audioRef.current;
    if (audio && !audio.paused) audio.pause();
    setSleepSetting({ kind: "off" });
    setSleepChapterEndSeconds(null);
  }, [sleepSetting, sleepChapterEndSeconds, currentTime]);

  const setSleep = useCallback((next: SleepSetting) => setSleepSetting(next), []);
  const sleepRemainingMs = sleepTargetMs == null ? null : Math.max(0, sleepTargetMs - sleepNowMs);

  return {
    audioRef,
    streamUrl: sessionState.streamUrl,
    hasFile: Boolean(activePart),
    playing,
    currentTime,
    duration,
    buffered,
    rate,
    chapters,
    currentChapter,
    volume,
    muted,
    togglePlay,
    stopForReplacement,
    seekTo,
    skip,
    setRate,
    setVolume,
    setMuted,
    nextChapter,
    prevChapter,
    sleep: { setting: sleepSetting, remainingMs: sleepRemainingMs },
    setSleep,
  };
}
