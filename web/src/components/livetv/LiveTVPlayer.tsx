import { useEffect, useRef, useState } from "react";
import Hls from "hls.js";
import mpegts from "mpegts.js";
import { getAccessToken, getProfileToken } from "@/api/client";
import { getProfileId } from "@/api/bloemClient";
import { cn } from "@/lib/utils";

type LiveTVPlayerProps = {
  streamUrl: string;
  /** Defaults to mpegts when omitted. */
  transport?: "mpegts" | "hls";
  title?: string;
  className?: string;
  /** Hide native controls when the host draws its own chrome (fullscreen watch). */
  hideNativeControls?: boolean;
  onPlayingChange?: (playing: boolean) => void;
  onErrorChange?: (error: string | null) => void;
  onStartingChange?: (starting: boolean) => void;
};

function authHeaders(): Record<string, string> {
  const headers: Record<string, string> = {};
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const profileId = getProfileId();
  if (profileId) headers["X-Profile-Id"] = profileId;
  const profileToken = getProfileToken();
  if (profileToken) headers["X-Profile-Token"] = profileToken;
  return headers;
}

function resolveStreamUrl(streamUrl: string): string {
  if (streamUrl.startsWith("http://") || streamUrl.startsWith("https://")) {
    return streamUrl;
  }
  if (typeof window === "undefined") return streamUrl;
  return new URL(streamUrl, window.location.origin).toString();
}

/** Match VideoPlayer's load policy so cold HLS remuxes can retry through 404s. */
const retryingLoadPolicy = {
  maxTimeToFirstByteMs: 45000,
  maxLoadTimeMs: 45000,
  timeoutRetry: { maxNumRetry: 6, retryDelayMs: 400, maxRetryDelayMs: 2000 },
  errorRetry: { maxNumRetry: 6, retryDelayMs: 400, maxRetryDelayMs: 2000 },
};

/**
 * Plays a Live TV stream. Uses hls.js for remuxed HLS (`transport=hls`) and
 * mpegts.js for the MPEG-TS session proxy.
 */
export function LiveTVPlayer({
  streamUrl,
  transport = "mpegts",
  title,
  className,
  hideNativeControls = false,
  onPlayingChange,
  onErrorChange,
  onStartingChange,
}: LiveTVPlayerProps) {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const mpegtsRef = useRef<ReturnType<typeof mpegts.createPlayer> | null>(null);
  const hlsRef = useRef<Hls | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [starting, setStarting] = useState(true);
  const networkRecoveryRef = useRef(0);
  const lastRecoveryAtRef = useRef(0);

  useEffect(() => {
    onErrorChange?.(error);
  }, [error, onErrorChange]);

  useEffect(() => {
    onStartingChange?.(starting);
  }, [starting, onStartingChange]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || !streamUrl) return;

    setError(null);
    setStarting(true);
    networkRecoveryRef.current = 0;
    lastRecoveryAtRef.current = 0;

    let parsed: URL;
    try {
      parsed = new URL(resolveStreamUrl(streamUrl));
    } catch {
      setError("The server returned an invalid Live TV stream address.");
      setStarting(false);
      return;
    }
    const url = parsed.href;
    if (parsed.origin !== window.location.origin || parsed.username || parsed.password) {
      setError("The server returned an unsupported Live TV stream address.");
      setStarting(false);
      return;
    }
    const isHLS = transport === "hls" || parsed.pathname.endsWith(".m3u8");

    if (isHLS) {
      // Server-issued delivery tokens are bound to the active stream and viewer.
      // Never add account credentials to URLs, including native media requests.
      const playUrl = url;
      if (!Hls.isSupported()) {
        if (!video.canPlayType("application/vnd.apple.mpegurl") || !parsed.searchParams.get("st")) {
          setError("This browser needs a server-signed native HLS stream to play Live TV.");
          setStarting(false);
          return;
        }
        let disposed = false;
        const ready = () => {
          setStarting(false);
          void video.play().catch(() => {
            if (!disposed) onPlayingChange?.(false);
          });
        };
        const failed = () => {
          setError("The live stream stopped or could not be loaded. Retune to try again.");
          setStarting(false);
          onPlayingChange?.(false);
        };
        const playing = () => onPlayingChange?.(true);
        const paused = () => onPlayingChange?.(false);
        video.addEventListener("loadedmetadata", ready);
        video.addEventListener("error", failed);
        video.addEventListener("playing", playing);
        video.addEventListener("pause", paused);
        video.src = playUrl;
        video.load();
        return () => {
          disposed = true;
          video.removeEventListener("loadedmetadata", ready);
          video.removeEventListener("error", failed);
          video.removeEventListener("playing", playing);
          video.removeEventListener("pause", paused);
          video.pause();
          video.removeAttribute("src");
          video.load();
        };
      }
      const hls = new Hls({
        enableWorker: true,
        // The live bridge emits standard HLS: 1s MPEG-TS segments, no
        // #EXT-X-PART and no #EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD. Low-latency
        // mode plus a tight max-latency ceiling made hls.js chase a live edge
        // that briefly outruns it at startup (the encoder bursts above realtime
        // draining the tuner buffer), forcing an edge seek into an unbuffered
        // gap → bufferStalledError → teardown after ~3 segments.
        lowLatencyMode: false,
        liveSyncDurationCount: 3,
        backBufferLength: 30,
        manifestLoadPolicy: { default: retryingLoadPolicy },
        playlistLoadPolicy: { default: retryingLoadPolicy },
        fragLoadPolicy: { default: retryingLoadPolicy },
        xhrSetup: (xhr, requestUrl) => {
          if (new URL(requestUrl, url).origin !== parsed.origin) {
            throw new Error("Cross-origin Live TV media requests are not supported.");
          }
          const headers = authHeaders();
          for (const [key, value] of Object.entries(headers)) {
            xhr.setRequestHeader(key, value);
          }
        },
      });
      hlsRef.current = hls;
      hls.loadSource(playUrl);
      hls.attachMedia(video);
      hls.on(Hls.Events.MANIFEST_PARSED, () => {
        void video.play().then(
          () => {
            setStarting(false);
            onPlayingChange?.(true);
          },
          () => {
            setStarting(false);
            onPlayingChange?.(false);
          },
        );
      });
      hls.on(Hls.Events.ERROR, (_event, data) => {
        if (!data.fatal) return;
        // Surface the real cause: the handler used to swallow details, which hid
        // whether a stall was a network, media, or buffer problem.
        const frag = data.frag as { sn?: number | string; url?: string } | undefined;
        console.warn("livetv hls fatal error", {
          type: data.type,
          details: data.details,
          sourceBufferName: (data as { sourceBufferName?: string }).sourceBufferName,
          fragSn: frag?.sn,
          mediaErrorCode: video.error?.code ?? null,
        });
        const now = Date.now();
        if (now - lastRecoveryAtRef.current < 1500) return;
        lastRecoveryAtRef.current = now;

        if (data.type === Hls.ErrorTypes.NETWORK_ERROR && networkRecoveryRef.current < 8) {
          networkRecoveryRef.current += 1;
          // Playlist often 404s for a beat while ffmpeg writes the first segment.
          hls.startLoad();
          return;
        }
        // A live encode can briefly outrun the player; a stall means the buffer
        // drained, not that the media is undecodable. Jump back to the live edge
        // and resume loading instead of the disruptive full-MSE reset that
        // recoverMediaError() performs (which tore the session down on startup).
        if (
          data.details === Hls.ErrorDetails.BUFFER_STALLED_ERROR &&
          networkRecoveryRef.current < 8
        ) {
          networkRecoveryRef.current += 1;
          if (hls.liveSyncPosition != null) {
            video.currentTime = hls.liveSyncPosition;
          }
          hls.startLoad();
          return;
        }
        // bufferAppendError is usually a bad fragment (video-only first segment,
        // corrupt TS, track layout change). recoverMediaError alone re-appends
        // the same frag; skip forward to the live edge so the sliding window can
        // age the offender out before we reset MSE.
        if (
          data.details === Hls.ErrorDetails.BUFFER_APPEND_ERROR &&
          networkRecoveryRef.current < 5
        ) {
          networkRecoveryRef.current += 1;
          if (hls.liveSyncPosition != null) {
            video.currentTime = hls.liveSyncPosition;
          }
          hls.recoverMediaError();
          hls.startLoad();
          return;
        }
        if (data.type === Hls.ErrorTypes.MEDIA_ERROR && networkRecoveryRef.current < 3) {
          networkRecoveryRef.current += 1;
          hls.recoverMediaError();
          return;
        }
        const detail = data.details || "HLS playback error";
        const friendly =
          detail === "manifestLoadError"
            ? "Could not load the live stream playlist. The channel may still be tuning — try again."
            : detail === "bufferAppendError"
              ? "Live stream media was rejected by the browser. Try switching channels or refreshing."
              : detail;
        setError(friendly);
        setStarting(false);
      });
      return () => {
        hls.destroy();
        if (hlsRef.current === hls) hlsRef.current = null;
      };
    }

    if (!mpegts.isSupported() || !mpegts.getFeatureList().mseLivePlayback) {
      setError("Live MPEG-TS playback is not supported in this browser.");
      setStarting(false);
      return;
    }

    const player = mpegts.createPlayer(
      {
        type: "mpegts",
        isLive: true,
        url,
      },
      {
        enableStashBuffer: false,
        liveBufferLatencyChasing: true,
        liveSync: true,
        headers: authHeaders(),
      },
    );
    mpegtsRef.current = player;
    player.attachMediaElement(video);
    player.on(mpegts.Events.ERROR, (_type: string, _detail: string, info: unknown) => {
      const message =
        typeof info === "object" && info && "msg" in info
          ? String((info as { msg?: string }).msg ?? "Playback error")
          : "Playback error";
      setError(message);
      setStarting(false);
    });
    player.load();
    const playResult = player.play();
    if (playResult && typeof playResult.then === "function") {
      void playResult.then(
        () => {
          setStarting(false);
          onPlayingChange?.(true);
        },
        () => {
          setStarting(false);
          onPlayingChange?.(false);
        },
      );
    } else {
      setStarting(false);
    }

    return () => {
      try {
        player.pause();
        player.unload();
        player.detachMediaElement();
        player.destroy();
      } catch {
        // Best-effort teardown when the session URL changes.
      }
      if (mpegtsRef.current === player) mpegtsRef.current = null;
    };
  }, [streamUrl, transport, onPlayingChange]);

  return (
    <div className={cn("relative overflow-hidden bg-black", className)}>
      <video
        ref={videoRef}
        className="h-full w-full object-contain"
        controls={!hideNativeControls}
        playsInline
        autoPlay
        aria-label={title ? `Live: ${title}` : "Live TV"}
      />
      {starting && !hideNativeControls ? (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center bg-black/40">
          <div className="h-8 w-8 animate-spin rounded-full border-2 border-white/20 border-t-white" />
        </div>
      ) : null}
      {error && !hideNativeControls ? (
        <div className="absolute inset-x-0 bottom-0 bg-black/80 px-3 py-2 text-sm text-white">
          {error}
        </div>
      ) : null}
    </div>
  );
}
