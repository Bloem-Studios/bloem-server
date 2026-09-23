import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useLocation } from "react-router";
import {
  ApiClientError,
  captureProfileRequestContext,
  captureSessionIdentity,
  isCapturedProfileAuthorityActive,
  isSessionIdentityCurrent,
  getProfileTokenGeneration,
  StaleApiRequestContextError,
} from "@/api/client";
import { nativeApiWithProfileRequestContext } from "@/api/bloemClient";
import { useAuth } from "@/hooks/useAuth";
import { useBloemCapabilities } from "@/hooks/queries/useBloemCapabilities";
import { campaignImage } from "@/components/engagement/campaigns";
import { useWatchPlaybackController } from "@/playback/watchPlaybackContext";
import { activeSeason, type SeasonalPack } from "./schedule";

const preference = "bloem-seasonal-effects";
export default function SeasonalLayer() {
  const { pathname } = useLocation();
  const { user, profile } = useAuth();
  const identity = captureSessionIdentity();
  const authority = captureProfileRequestContext();
  const playback = useWatchPlaybackController();
  const surface = pathname === "/" ? "home" : pathname === "/login" ? "login" : "";
  const [unverifiedViewer, setUnverifiedViewer] = useState<string | null>(null);
  const viewerKey = JSON.stringify([
    identity.serverOrigin,
    identity.authContextVersion,
    profile?.id,
    authority?.profileTokenGeneration,
  ]);
  const authenticatedHome = surface === "home" && Boolean(user);
  const eligible = Boolean(authenticatedHome && profile && authority?.profileId === profile.id);
  const capabilities = useBloemCapabilities(eligible);
  const nativeViewer = eligible && capabilities.data?.feature_tokens.includes("seasonal_viewer_v1");
  const ready =
    !authenticatedHome ||
    (eligible &&
      unverifiedViewer !== viewerKey &&
      (capabilities.isSuccess || capabilities.isError));
  const authorityActive = () =>
    isSessionIdentityCurrent(identity) &&
    (!authority ||
      (isCapturedProfileAuthorityActive(authority) &&
        authority.profileTokenGeneration === getProfileTokenGeneration()));
  const [enabled, setEnabled] = useState(() => {
    try {
      return localStorage.getItem(preference) !== "off";
    } catch {
      return true;
    }
  });
  const [reduced, setReduced] = useState(
    () => matchMedia("(prefers-reduced-motion: reduce)").matches,
  );
  const [now, setNow] = useState(Date.now);
  const data = useQuery({
    queryKey: [
      "seasonal-branding",
      identity.serverOrigin,
      identity.authContextVersion,
      profile?.id,
      authority?.profileTokenGeneration,
      surface,
      nativeViewer ? "native" : "public",
    ],
    queryFn: async ({ signal }) => {
      let result: { ambience?: SeasonalPack[] };
      if (!authorityActive()) throw new StaleApiRequestContextError();
      if (nativeViewer && authority) {
        try {
          result = await nativeApiWithProfileRequestContext("/ambience", authority, { signal });
        } catch (error) {
          // The shared client clears rejected PIN proofs, advancing their
          // generation. Wait for a new authority instead of retrying that
          // denial immediately under the cleared proof's fresh query key.
          const current = captureProfileRequestContext();
          if (
            error instanceof ApiClientError &&
            error.code === "profile_unverified" &&
            isSessionIdentityCurrent(identity) &&
            current?.profileId === authority.profileId &&
            current.profileToken === null
          ) {
            setUnverifiedViewer(
              JSON.stringify([
                current.serverOrigin,
                current.authContextVersion,
                current.profileId,
                current.profileTokenGeneration,
              ]),
            );
          }
          throw error;
        }
      } else {
        // Branding is public-only, including on servers without viewer delivery.
        // It must never be mistaken for an authenticated seasonal response.
        const response = await fetch("/api/v1/theme/branding", { signal, credentials: "omit" });
        if (!response.ok) throw new Error("Seasonal presentation is unavailable");
        result = await response.json();
      }
      if (!authorityActive()) throw new StaleApiRequestContextError();
      return result;
    },
    enabled: Boolean(surface) && ready,
    retry: false,
    refetchInterval: 30000,
    staleTime: 0,
  });
  useEffect(() => {
    const m = matchMedia("(prefers-reduced-motion: reduce)");
    const update = () => setReduced(m.matches);
    m.addEventListener("change", update);
    return () => m.removeEventListener("change", update);
  }, []);
  useEffect(() => {
    if (!surface) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [surface]);
  if (!surface || !ready || !authorityActive() || playback.state.request || reduced) return null;
  const packs =
    !data.isError && now - data.dataUpdatedAt < 45_000
      ? (data.data?.ambience || []).filter((p) => activeSeason(p, surface, now))
      : [];
  // One restrained layer; overlapping packs never multiply visual density.
  const pack = packs.find(
    (p) =>
      p.effect_id === "snow" ||
      campaignImage(p.assets?.banner_url) ||
      p.assets?.sprites?.some((url) => campaignImage(url)),
  );
  if (!pack) return null;
  return (
    <>
      {enabled &&
        (pack.effect_id === "snow" ? <SnowCanvas pack={pack} /> : <SeasonalArtwork pack={pack} />)}
      <button
        className="bg-background/90 text-muted-foreground fixed right-4 bottom-4 z-20 rounded-md border px-3 py-1.5 text-xs"
        onClick={() => {
          setEnabled(!enabled);
          try {
            localStorage.setItem(preference, enabled ? "off" : "on");
          } catch {
            /* Preference still applies for this session. */
          }
        }}
        aria-pressed={enabled}
      >
        Seasonal effects: {enabled ? "On" : "Off"}
      </button>
    </>
  );
}
export function SeasonalArtwork({ pack }: { pack: SeasonalPack }) {
  const banner = campaignImage(pack.assets?.banner_url);
  const sprites = (pack.assets?.sprites ?? [])
    .flatMap((url) => {
      const safe = campaignImage(url);
      return safe ? [safe] : [];
    })
    .slice(0, 6);
  const opacity = Math.min(1, Math.max(0, pack.intensity)) * 0.2;
  return (
    <div
      aria-hidden="true"
      className="pointer-events-none fixed inset-0 z-10 overflow-hidden"
      style={{ opacity }}
    >
      {banner && (
        <img
          src={banner}
          alt=""
          referrerPolicy="no-referrer"
          className="absolute top-24 right-0 max-h-[28vh] w-[min(32vw,24rem)] object-contain"
        />
      )}
      {sprites.map((url, index) => (
        <img
          key={`${index}:${url}`}
          src={url}
          alt=""
          referrerPolicy="no-referrer"
          className="absolute h-12 w-12 object-contain sm:h-20 sm:w-20"
          style={{
            right: `${3 + (index % 3) * 7}%`,
            top: `${38 + Math.floor(index / 3) * 26 + (index % 3) * 6}%`,
            transform: `rotate(${index % 2 ? 12 : -8}deg)`,
          }}
        />
      ))}
    </div>
  );
}

function SnowCanvas({ pack }: { pack: SeasonalPack }) {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const canvas = ref.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    let frame = 0,
      last = 0,
      elapsed = 0;
    const seed = (i: number) => {
      const x = Math.sin(i * 127.1 + 311.7) * 43758.5453;
      return x - Math.floor(x);
    };
    const count = Math.round(10 + 22 * Math.min(1, pack.intensity));
    function draw(at: number) {
      if (!canvas || !ctx) return;
      const w = innerWidth,
        h = innerHeight,
        d = Math.min(devicePixelRatio, 2);
      if (canvas.width !== Math.round(w * d) || canvas.height !== Math.round(h * d)) {
        canvas.width = Math.round(w * d);
        canvas.height = Math.round(h * d);
      }
      ctx.setTransform(d, 0, 0, d, 0, 0);
      ctx.clearRect(0, 0, w, h);
      if (Date.now() >= Date.parse(pack.window.ends_at)) return;
      if (last && !document.hidden) elapsed += Math.min((at - last) / 1000, 0.05);
      last = at;
      if (!document.hidden)
        for (let i = 0; i < count; i++) {
          const x = (seed(i + 1) + Math.sin(elapsed * 0.11 + seed(i + 300) * 6.28) * 0.008) * w;
          const y = ((seed(i + 80) + elapsed * (0.022 + seed(i + 160) * 0.018)) % 1) * h;
          if (y < h * 0.12) continue;
          ctx.globalAlpha = 0.52 * Math.min(1, Math.max(0, (x / w - 0.34) * 5));
          ctx.fillStyle = "#f0f4fa";
          ctx.beginPath();
          ctx.arc(x, y, (1.1 + seed(i + 230) * 1.4) * Math.max(0.85, w / 1300), 0, Math.PI * 2);
          ctx.fill();
        }
      frame = requestAnimationFrame(draw);
    }
    frame = requestAnimationFrame(draw);
    return () => cancelAnimationFrame(frame);
  }, [pack]);
  return (
    <canvas
      ref={ref}
      aria-hidden="true"
      className="pointer-events-none fixed inset-0 z-10 h-full w-full"
    />
  );
}
