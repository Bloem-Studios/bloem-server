export interface SeasonalPack {
  id: string;
  effect_id: string;
  window: { starts_at: string; ends_at: string };
  intensity: number;
  surfaces: string[];
  assets?: { banner_url?: string; sprites?: string[] };
}
export function activeSeason(pack: SeasonalPack, surface: string, now: number): boolean {
  return (
    Number.isFinite(pack.intensity) &&
    pack.intensity > 0 &&
    (pack.surfaces.includes(surface) || pack.surfaces.includes("all")) &&
    Date.parse(pack.window.starts_at) <= now &&
    now < Date.parse(pack.window.ends_at)
  );
}
