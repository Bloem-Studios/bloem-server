import path from "node:path";
import type { Plugin } from "vite";

// Exact module boundaries; original imports made by their own adapter bypass
// decoration once. Relative and aliased consumer imports resolve identically.
const modules: Record<string, string> = {
  "hooks/useSearchMediaScope.ts": "adapters/searchScope.ts",
  "pages/ItemDetail/components/SubtitleSearchDialog.tsx": "adapters/SubtitleSearchDialog.tsx",
  "hooks/queries/metadataAI.ts": "adapters/metadataAI.ts",
  "components/RealtimeEventsProvider.tsx": "adapters/realtime.tsx",
  "components/TasteSeedBanner.tsx": "adapters/TasteSeedBanner.tsx",
  "hooks/queries/useRequests.ts": "adapters/requests.ts",
  "components/HeroBanner.tsx": "adapters/HeroBanner.tsx",
  "components/LibraryHeader.tsx": "adapters/LibraryHeader.tsx",
  "hooks/queries/settingValues.ts": "adapters/settings.ts",
  "hooks/queries/libraryPageState.ts": "adapters/libraryPageState.ts",
  "pages/ItemDetail/useThemeMusic.ts": "adapters/themeMusic.ts",
  "hooks/useOnViewTranslation.ts": "adapters/translation.ts",
  "components/onboarding/OnboardingGate.tsx": "adapters/gates.tsx",
  "components/TasteSeedGate.tsx": "adapters/TasteSeedGate.tsx",
  "pages/ItemDetail/EbookContent.tsx": "adapters/EbookContent.tsx",
  "pages/ItemDetail/AudiobookContent.tsx": "adapters/AudiobookContent.tsx",
  "pages/ItemDetail/MangaContent.tsx": "adapters/MangaContent.tsx",
  "pages/ItemDetail/components/ActionBar.tsx": "adapters/ActionBar.tsx",
  "components/CardPlayOverlay.tsx": "adapters/CardPlayOverlay.tsx",
  "components/MediaItemMenu.tsx": "adapters/MediaItemMenu.tsx",
  "api/v2/account.ts": "account.ts",
  "pages/audiobooks/player/audiobookPlaybackContext.tsx": "adapters/audiobookPlayback.tsx",
};
export function bloemPreview(): Plugin {
  let src = "";
  return {
    name: "bloem-preview",
    enforce: "pre",
    configResolved(config) {
      src = path.resolve(config.root, "src");
    },
    async resolveId(source, importer, options) {
      if (!importer || source.startsWith("\0") || source.includes("node_modules")) return null;
      if (!source.startsWith(".") && !source.startsWith("@/") && !source.startsWith(src))
        return null;
      const resolved = await this.resolve(source, importer, { ...options, skipSelf: true });
      if (!resolved) return null;
      const target = path
        .relative(src, resolved.id.split("?")[0] ?? resolved.id)
        .replaceAll("\\", "/");
      const adapter = modules[target];
      if (!adapter) return null;
      const id = path.join(src, "bloem/preview", adapter);
      if (importer.split("?")[0] === id) return null;
      return id;
    },
  };
}
