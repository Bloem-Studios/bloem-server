import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useLocation } from "react-router";
import { api } from "@/api/client";
import { useWatchPlaybackController } from "@/playback/watchPlaybackContext";
import { activeSeason, type SeasonalPack } from "./schedule";

const preference = "bloem-seasonal-effects";
export default function SeasonalLayer() {
  const { pathname } = useLocation();
  const playback = useWatchPlaybackController();
  const surface = pathname === "/" ? "home" : pathname === "/login" ? "login" : "";
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
    queryKey: ["seasonal-branding"],
    queryFn: () => api<{ ambience?: SeasonalPack[] }>("/theme/branding"),
    enabled: Boolean(surface),
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
  if (!surface || playback.state.request || reduced) return null;
  const packs = (data.data?.ambience || []).filter((p) => activeSeason(p, surface, now));
  if (!packs.length) return null;
  // One restrained layer; overlapping campaigns never multiply visual density.
  const snow = packs.find((p) => p.effect_id === "snow");
  return (
    <>
      {enabled && snow && <SnowCanvas pack={snow} />}
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
