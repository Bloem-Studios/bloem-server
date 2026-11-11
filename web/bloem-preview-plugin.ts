import path from "node:path";
import type { Plugin } from "vite";

// Exact module boundaries; original imports made by their own adapter bypass
// decoration once. Relative and aliased consumer imports resolve identically.
const modules: Record<string, string> = {
  "pages/Collections.tsx": "adapters/Collections.tsx",
  "pages/Calendar.tsx": "adapters/Calendar.tsx",
  "hooks/useSearchMediaScope.ts": "adapters/searchScope.ts",
  "pages/ItemDetail/components/SubtitleSearchDialog.tsx": "adapters/SubtitleSearchDialog.tsx",
  "hooks/queries/metadataAI.ts": "adapters/metadataAI.ts",
  "components/RealtimeEventsProvider.tsx": "adapters/realtime.tsx",
  "components/TasteSeedBanner.tsx": "adapters/TasteSeedBanner.tsx",
  "hooks/queries/useRequests.ts": "adapters/requests.ts",
  "components/HeroBanner.tsx": "adapters/HeroBanner.tsx",
  "components/LibraryHeader.tsx": "adapters/LibraryHeader.tsx",
  "bloem/audiobooks/BloemLibraryRecommended.tsx": "adapters/BloemLibraryRecommended.tsx",
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

// Boundaries replaced for one importer only, so every other importer, Silo's
// own tests included, keeps the original module. A package is named "npm:<name>".
const importerModules: Record<string, Record<string, string>> = {
  // The app's requests route gets Bloem's page, which adds the audiobook and
  // ebook request tabs around Silo's and imports Silo's page itself.
  "App.tsx": {
    "pages/Requests.tsx": "../BloemRequests.tsx",
    // The app shell's one route outlet sits behind the preview's route gate.
    "npm:react-router": "adapters/appRouter.tsx",
    // The preview chrome sits inside Silo's branding provider.
    "contexts/BrandingProvider.tsx": "BrandingWithPreview.tsx",
  },
  // A guest previewing gets the preview's sidebar.
  "components/Layout.tsx": { "components/AppSidebar.tsx": "adapters/AppSidebar.tsx" },
  // Server activity and the build route are the platform administrator's.
  "components/AdminLayout.tsx": {
    "components/ServerActivity.tsx": "adapters/AdminServerActivity.tsx",
  },
  "components/AdminSidebar.tsx": { "hooks/queries/admin/system.ts": "adapters/adminBuildInfo.ts" },
  // Silo's all-libraries switch gets an accessible name naming the scope.
  "components/LibraryAccessSelector.tsx": {
    "components/ui/switch.tsx": "adapters/LibraryAccessSwitch.tsx",
  },
  // The account library card counts Bloem's Live TV permission as a changed
  // row, and the card names that row through Bloem's catalog.
  "pages/admin-users/detail/access/LibraryAccessCard.tsx": {
    "pages/admin-users/detail/access/useAccountCardDraft.ts": "adapters/libraryCardDraft.ts",
  },
  "pages/admin-users/detail/access/EditableCard.tsx": {
    "i18n/index.ts": "adapters/i18nWithBloem.ts",
  },
  // Access groups list Bloem's Live TV permission with Silo's.
  "components/admin/access-groups/AccessGroupPermissionsSection.tsx": { "i18n/index.ts": "adapters/i18nWithBloem.ts" },
  // Bloem's admin pages have Bloem titles.
  "lib/documentTitle.ts": { "i18n/index.ts": "adapters/i18nWithBloem.ts" },
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
      const from = path.relative(src, importer.split("?")[0] ?? importer).replaceAll("\\", "/");
      const packageAdapter = importerModules[from]?.[`npm:${source}`];
      if (packageAdapter) return path.join(src, "bloem/preview", packageAdapter);
      if (!source.startsWith(".") && !source.startsWith("@/") && !source.startsWith(src))
        return null;
      const resolved = await this.resolve(source, importer, { ...options, skipSelf: true });
      if (!resolved) return null;
      const target = path
        .relative(src, resolved.id.split("?")[0] ?? resolved.id)
        .replaceAll("\\", "/");
      const adapter = importerModules[from]?.[target] ?? modules[target];
      if (!adapter) return null;
      const id = path.join(src, "bloem/preview", adapter);
      if (importer.split("?")[0] === id) return null;
      return id;
    },
  };
}
