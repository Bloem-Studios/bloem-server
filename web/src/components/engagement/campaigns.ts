export interface CampaignCard {
  id: string;
  headline: string;
  kicker?: string;
  subtitle?: string;
  image_url: string;
  deeplink?: string;
  cta?: { label: string; url: string } | null;
  dismissible: boolean;
  expires_at: string;
  duration_seconds?: number;
  video_url?: string;
  playback_style?: string;
}
export interface CampaignTargeting {
  audience: "all" | "role" | "organization" | "library" | "explicit";
  role?: string;
  organization_id?: string;
  library_id?: number;
  user_ids?: number[];
  profile_ids?: string[];
}
export interface CampaignInput {
  organization_id: string | null;
  surfaces: string[];
  placement: {
    home_position?: number;
    detail_slot?: string;
    content_ids?: string[];
    duration_seconds?: number;
    video_url?: string;
    playback_style?: string;
  };
  kicker: string;
  headline: string;
  subtitle: string;
  image_url: string;
  image_width?: number | null;
  image_height?: number | null;
  deeplink: string;
  cta: { label: string; url: string } | null;
  priority: number;
  starts_at: string;
  ends_at: string;
  targeting: CampaignTargeting;
  dismissible: boolean;
}
export interface StoredCampaign extends CampaignInput {
  id: string;
  updated_at: string;
}
export interface SeasonalInput {
  effect_id: string;
  window: { starts_at: string; ends_at: string; repeat_yearly: boolean; timezone: string };
  intensity: number;
  surfaces: string[];
  assets: { banner_url?: string; sprites?: string[] };
  organization_id: string | null;
}
export interface StoredSeason extends SeasonalInput {
  id: string;
  updated_at: string;
}

/** Server-authored links still cross a rendering trust boundary. */
export function campaignLink(value?: string): string | null {
  if (!value || [...value].some((character) => character === "\\" || character.charCodeAt(0) <= 32))
    return null;
  if (value.startsWith("/") && !value.startsWith("//")) return value;
  try {
    const url = new URL(value);
    return !url.username &&
      !url.password &&
      (url.protocol === "https:" || url.protocol === "bloem:")
      ? url.href
      : null;
  } catch {
    return null;
  }
}
export function campaignImage(value?: string): string | null {
  if (!value) return null;
  if (/^\/api\/v1\/ambience\/assets\/[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(value)) return value;
  const url = campaignLink(value);
  return url?.startsWith("https://") ? url : null;
}
