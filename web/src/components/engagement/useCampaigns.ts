import { useEffect, useState, useSyncExternalStore } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  apiWithProfileRequestContext,
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
} from "@/api/client";
import { useAuth } from "@/hooks/useAuth";
import type { CampaignCard } from "./campaigns";

export type CampaignSurface = "home" | "detail" | "pre_playback";
interface CampaignResult {
  cards: CampaignCard[];
  position: number;
}
const preferenceEvent = "bloem-campaign-preference";
const sessionPreferences = new Map<string, boolean>();
function subscribePreference(notify: () => void) {
  window.addEventListener("storage", notify);
  window.addEventListener(preferenceEvent, notify);
  return () => {
    window.removeEventListener("storage", notify);
    window.removeEventListener(preferenceEvent, notify);
  };
}
export function useHomeCampaignPreference() {
  const { user, profile } = useAuth();
  const key =
    user && profile
      ? `bloem-home-campaigns:${window.location.origin}:${user.id}:${profile.id}`
      : "";
  const enabled = useSyncExternalStore(
    subscribePreference,
    () => {
      const session = sessionPreferences.get(key);
      if (session !== undefined) return session;
      try {
        return Boolean(key && localStorage.getItem(key) === "on");
      } catch {
        return false;
      }
    },
    () => false,
  );
  return {
    enabled,
    eligible: Boolean(user && profile && !profile.is_child),
    setEnabled: (value: boolean) => {
      if (!key) return false;
      let persisted = true;
      try {
        localStorage.setItem(key, value ? "on" : "off");
        sessionPreferences.delete(key);
      } catch {
        sessionPreferences.set(key, value);
        persisted = false;
      }
      window.dispatchEvent(new Event(preferenceEvent));
      return persisted;
    },
  };
}
export function useCampaigns(surface: CampaignSurface, contentId = "", enabled = true) {
  const { profile } = useAuth();
  const authority = captureProfileRequestContext();
  const eligible = Boolean(
    authority && profile && authority.profileId === profile.id && !profile.is_child,
  );
  const queryKey = [
    "bloem-campaigns",
    authority?.serverOrigin,
    authority?.authContextVersion,
    authority?.profileId,
    authority?.profileTokenGeneration,
    surface,
    contentId,
  ];
  const client = useQueryClient();
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    if (!enabled || !eligible) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [enabled, eligible]);
  async function read<T>(path: string, signal: AbortSignal) {
    if (!authority || !isCapturedProfileAuthorityActive(authority))
      throw new StaleApiRequestContextError();
    const data = await apiWithProfileRequestContext<T>(path, authority, { signal });
    if (!isCapturedProfileAuthorityActive(authority)) throw new StaleApiRequestContextError();
    return data;
  }
  const query = useQuery({
    queryKey,
    enabled: eligible && enabled,
    retry: false,
    staleTime: 20_000,
    refetchInterval: 30_000,
    queryFn: async ({ signal }): Promise<CampaignResult> => {
      if (surface !== "home") {
        const data = await read<{ promotions: CampaignCard[] }>(
          `/promotions?surface=${surface}&content_id=${encodeURIComponent(contentId)}`,
          signal,
        );
        return { cards: data.promotions ?? [], position: 0 };
      }
      const layout = await read<{ sections: { id: string; section_type: string }[] }>(
        "/home/layout?promoted=1",
        signal,
      );
      const position = layout.sections.findIndex((section) => section.section_type === "promoted");
      const section = layout.sections[position];
      if (!section) return { cards: [], position: 0 };
      const data = await read<{ section: { items: { promo?: CampaignCard }[] } }>(
        `/home/sections/${encodeURIComponent(section.id)}/items?promoted=1`,
        signal,
      );
      return {
        position,
        cards: data.section.items.flatMap((item) => (item.promo ? [item.promo] : [])),
      };
    },
  });
  const cards =
    eligible && enabled && !query.isError && now - query.dataUpdatedAt < 45_000
      ? (query.data?.cards ?? []).filter((card) => Date.parse(card.expires_at) > now)
      : [];
  async function dismiss(id: string) {
    if (!authority || !isCapturedProfileAuthorityActive(authority))
      throw new StaleApiRequestContextError();
    await apiWithProfileRequestContext(
      `/home/dismissals/${encodeURIComponent(`promo:${surface}`)}/${encodeURIComponent(id)}`,
      authority,
      { method: "PUT", body: "{}" },
      "none",
    );
    if (!isCapturedProfileAuthorityActive(authority)) throw new StaleApiRequestContextError();
    client.setQueryData<CampaignResult>(queryKey, (old) =>
      old ? { ...old, cards: old.cards.filter((card) => card.id !== id) } : old,
    );
    void client.invalidateQueries({ queryKey });
  }
  return { query, cards, eligible, position: query.data?.position ?? 0, dismiss };
}
