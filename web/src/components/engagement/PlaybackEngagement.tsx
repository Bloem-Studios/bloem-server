import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/client";
import { useAuth } from "@/hooks/useAuth";
import { ChapterGate } from "./playback-policy";
import "./playback-engagement.css";
export type PlaybackCard = {
  duration_seconds?: number;
  id: string;
  headline: string;
  kicker?: string;
  subtitle?: string;
  image_url: string;
  video_url?: string;
  playback_style?: string;
  expires_at: string;
  cta?: { label: string; url: string };
};
const seen = new Set<string>();
export function PlaybackEngagement({
  contentId,
  position,
  playing,
  seeking,
  blocked,
  chapters,
}: {
  contentId: string;
  position: number;
  playing: boolean;
  seeking: boolean;
  blocked: boolean;
  chapters: number[];
}) {
  const { user, profile } = useAuth();
  const scope = user && profile && !profile.is_child ? `${user.id}:${profile.id}:${contentId}` : "";
  return scope ? (
    <PlaybackSession
      key={scope}
      {...{ scope, contentId, position, playing, seeking, blocked, chapters }}
    />
  ) : null;
}
export function PlaybackSession({
  scope,
  contentId,
  position,
  playing,
  seeking,
  blocked,
  chapters,
}: {
  scope: string;
  contentId: string;
  position: number;
  playing: boolean;
  seeking: boolean;
  blocked: boolean;
  chapters: number[];
}) {
  const gate = useRef(new ChapterGate());
  const [card, setCard] = useState<PlaybackCard | null>(null);
  const [began, setBegan] = useState(0),
    [clock, setClock] = useState(() => performance.now()),
    [wallClock, setWallClock] = useState(Date.now),
    [message, setMessage] = useState("");
  const host = useRef<HTMLDivElement>(null),
    previousFocus = useRef<HTMLElement | null>(null);
  const data = useQuery({
    queryKey: ["playback-campaign", scope],
    queryFn: () =>
      api<{ promotions: PlaybackCard[] }>(
        `/promotions?surface=in_playback&content_id=${encodeURIComponent(contentId)}`,
      ),
    refetchInterval: 30000,
    staleTime: 20000,
    retry: false,
  });
  useEffect(() => {
    const timer = window.setInterval(() => {
      setClock(performance.now());
      setWallClock(Date.now());
    }, 100);
    return () => clearInterval(timer);
  }, []);
  const candidate = data.data?.promotions.find(
    (c) => ["card", "pip"].includes(c.playback_style || "") && Date.parse(c.expires_at) > wallClock,
  );
  const fresh = wallClock - data.dataUpdatedAt < 45000 && !data.isError;
  useEffect(() => {
    if (
      gate.current.sample(
        position,
        performance.now(),
        playing,
        seeking,
        chapters,
        Boolean(candidate) &&
          fresh &&
          !blocked &&
          !seen.has(scope) &&
          document.visibilityState === "visible",
      )
    ) {
      seen.add(scope);
      setCard(candidate!);
      setBegan(performance.now());
    }
  }, [position, playing, seeking, chapters, candidate, fresh, blocked, scope]);
  function close() {
    if (host.current?.contains(document.activeElement)) previousFocus.current?.focus();
    setCard(null);
  }
  const age = clock - began;
  const duration = Math.max(5, Math.min(60, card?.duration_seconds || 10)) * 1000;
  useEffect(() => {
    if (
      card &&
      (age >= duration ||
        Date.parse(card.expires_at) <= Date.now() ||
        blocked ||
        !playing ||
        seeking ||
        !fresh ||
        !data.data?.promotions.some((c) => c.id === card.id))
    ) {
      if (host.current?.contains(document.activeElement)) previousFocus.current?.focus();
      setCard(null);
    }
  }, [age, duration, card, blocked, playing, seeking, fresh, data.data]);
  useEffect(() => {
    if (!card) return;
    const key = (e: KeyboardEvent) => {
      if (
        e.defaultPrevented ||
        e.key !== "ArrowRight" ||
        host.current?.contains(document.activeElement)
      )
        return;
      const target = e.target as HTMLElement;
      if (target.closest("input,textarea,button,a,[role=dialog]")) return;
      previousFocus.current = document.activeElement as HTMLElement;
      host.current?.querySelector("button")?.focus();
      e.preventDefault();
      e.stopPropagation();
    };
    document.addEventListener("keydown", key, true);
    return () => document.removeEventListener("keydown", key, true);
  }, [card]);
  if (!card) return null;
  return (
    <div
      ref={host}
      className="playback-campaign"
      style={{ opacity: Math.min(1, age / 650, (duration - age) / 1000) }}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Escape") {
          e.preventDefault();
          close();
        }
      }}
    >
      <PlaybackCreative card={card} />
      <div className="playback-campaign-copy">
        <small>{card.kicker || "From your server"}</small>
        <h3>{card.headline}</h3>
        {card.subtitle && <p>{card.subtitle}</p>}
        <div className="playback-campaign-actions">
          {card.cta && (
            <button
              disabled={message === "Saved to your inbox"}
              onClick={async () => {
                setMessage("Saving…");
                try {
                  await api(
                    `/promotions/${encodeURIComponent(card.id)}/save?content_id=${encodeURIComponent(contentId)}`,
                    { method: "POST" },
                  );
                  setMessage("Saved to your inbox");
                } catch {
                  setMessage("Could not save. Try again.");
                }
              }}
            >
              Send to my phone
            </button>
          )}
          <button aria-label="Close overlay" onClick={close}>
            ✕
          </button>
        </div>
        <span role="status">{message}</span>
      </div>
      <div
        className="playback-campaign-timer"
        style={{ transform: `scaleX(${Math.max(0, 1 - age / duration)})` }}
      />
    </div>
  );
}
export function PlaybackCreative({ card }: { card: PlaybackCard }) {
  const [failed, setFailed] = useState(false);
  return (
    <div className={card.playback_style === "pip" ? "playback-creative pip" : "playback-creative"}>
      {card.playback_style === "pip" && card.video_url && !failed ? (
        <video
          src={card.video_url}
          poster={card.image_url}
          muted
          autoPlay
          playsInline
          disablePictureInPicture
          onError={() => setFailed(true)}
          onVolumeChange={(e) => {
            e.currentTarget.muted = true;
            e.currentTarget.volume = 0;
          }}
        />
      ) : (
        <img src={card.image_url} alt="" />
      )}
      {card.playback_style === "pip" && <span>MUTED</span>}
    </div>
  );
}
