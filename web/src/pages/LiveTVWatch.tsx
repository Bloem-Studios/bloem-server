import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router";
import { ArrowLeft, Radio } from "lucide-react";
import { captureProfileRequestContext } from "@/api/client";
import type { LiveTVSessionStartResponse } from "@/api/types";
import { LiveTVAccessGate } from "@/components/livetv/LiveTVAccessGate";
import { LiveTVPlayer } from "@/components/livetv/LiveTVPlayer";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/hooks/useAuth";
import { useCodecDetection } from "@/player/hooks/useCodecDetection";
import { buildLiveTVCapabilities } from "@/lib/liveTVCapabilities";
import { startLiveTVPlayback } from "@/lib/liveTVSession";

export default function LiveTVWatch() {
  const { profile } = useAuth();
  const { channelId } = useParams<{ channelId: string }>();
  return (
    <div className="fixed inset-0 z-50 bg-black text-white">
      <LiveTVAccessGate>
        <LiveTVWatchContent key={`${profile?.id}:${channelId}`} channelId={channelId ?? ""} />
      </LiveTVAccessGate>
    </div>
  );
}

function LiveTVWatchContent({ channelId }: { channelId: string }) {
  const probe = useCodecDetection();
  const [attempt, setAttempt] = useState(0);
  const capabilities = probe.settled
    ? JSON.stringify(
        buildLiveTVCapabilities({
          codecs_video: probe.codecsVideo,
          codecs_audio: probe.codecsAudio,
          max_resolution: probe.maxResolution,
          containers: probe.containers,
          hdr: probe.hdr,
        }),
      )
    : null;
  return (
    <LiveTVWatchAttempt
      key={`${capabilities}:${attempt}`}
      channelId={channelId}
      capabilities={capabilities}
      onRetry={() => setAttempt((value) => value + 1)}
    />
  );
}

function LiveTVWatchAttempt({
  channelId,
  capabilities,
  onRetry,
}: {
  channelId: string;
  capabilities: string | null;
  onRetry: () => void;
}) {
  const [authority] = useState(captureProfileRequestContext);
  const [session, setSession] = useState<LiveTVSessionStartResponse | null>(null);
  const [error, setError] = useState<string | null>(
    !authority || !channelId ? "Select a profile and channel to watch Live TV." : null,
  );
  const stopPlayback = useRef<(() => void) | null>(null);
  useEffect(() => {
    if (!capabilities || !authority || !channelId) return;
    const stop = startLiveTVPlayback(
      channelId,
      authority,
      JSON.parse(capabilities),
      setSession,
      (failure) => {
        setSession(null);
        setError(failure.message);
      },
    );
    stopPlayback.current = stop;
    return stop;
  }, [channelId, capabilities, authority]);

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center gap-4 border-b border-white/10 px-5 py-4">
        <Button asChild variant="ghost" size="icon" aria-label="Back to Live TV">
          <Link to="/livetv">
            <ArrowLeft aria-hidden />
          </Link>
        </Button>
        <Radio className="h-5 w-5 text-orange-400" aria-hidden />
        <h1 className="text-lg font-medium">Live TV</h1>
      </header>
      <main className="relative flex min-h-0 flex-1 items-center justify-center">
        {session ? (
          <LiveTVPlayer
            streamUrl={session.hls_url || session.stream_url!}
            transport={session.transport}
            title="Live TV"
            className="h-full w-full"
            onErrorChange={(message) => {
              if (!message) return;
              stopPlayback.current?.();
              setSession(null);
              setError(message);
            }}
          />
        ) : (
          <div className="flex max-w-lg flex-col items-center gap-5 px-6 text-center" role="status">
            <p className="text-white/75">{error ?? "Connecting to your channel…"}</p>
            {error && <Button onClick={onRetry}>Try again</Button>}
          </div>
        )}
      </main>
    </div>
  );
}
