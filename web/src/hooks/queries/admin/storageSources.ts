import { useQuery } from "@tanstack/react-query";

import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";
import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";

/** A storage source an ebook library can read its books from. */
export interface StorageSource {
  source_key: string;
  owner_kind: "platform" | "organization";
  plugin_id: string;
  provider_source_id: string;
  enabled: boolean;
  state: string;
  configured: boolean;
}

interface StorageSourcePage {
  sources: StorageSource[];
  next_after: string | null;
}

/**
 * The storage sources visible in the active administrative context. Only
 * enabled, configured sources can back a new library. Outside an
 * administrative context (the setup wizard) there are none.
 */
export function useStorageSources(enabled: boolean) {
  const active = useOptionalAdminContext()?.active ?? null;
  const scope = active?.scope ?? "platform";
  return useQuery({
    queryKey: adminV2QueryKey(active?.key ?? "platform", "native-storage", "sources"),
    queryFn: () =>
      adminV2Api<StorageSourcePage>(`/${scope}/native-storage/sources?limit=100`).then(
        (page) => page.sources,
      ),
    enabled: enabled && active !== null,
    staleTime: 30_000,
  });
}

export function storageSourceLabel(source: StorageSource): string {
  return `${source.provider_source_id} (${source.plugin_id})`;
}
