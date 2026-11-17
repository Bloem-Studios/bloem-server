// @vitest-environment node

import path from "node:path";
import { describe, expect, it } from "vitest";
import { bloemPreview } from "./bloem-preview-plugin";

const WEB_ROOT = path.dirname(new URL(import.meta.url).pathname);
const SRC = path.join(WEB_ROOT, "src");

type ResolveId = (
  this: { resolve: (source: string, importer: string) => Promise<{ id: string }> },
  source: string,
  importer: string,
  options: object,
) => Promise<string | null>;

/**
 * Resolves like Vite: "@/" is src, a relative source is beside its importer,
 * ".tsx" appended unless the source names its extension.
 */
function resolveFrom(importer: string, source: string): Promise<string | null> {
  const plugin = bloemPreview();
  (plugin.configResolved as (config: { root: string }) => void)({ root: WEB_ROOT });
  const importerPath = path.join(SRC, importer);
  const context = {
    async resolve(spec: string, from: string) {
      const base = spec.startsWith("@/")
        ? path.join(SRC, spec.slice(2))
        : path.resolve(path.dirname(from), spec);
      return { id: /\.tsx?$/.test(base) ? base : `${base}.tsx` };
    },
  };
  return (plugin.resolveId as unknown as ResolveId).call(context, source, importerPath, {});
}

describe("bloemPreview requests page", () => {
  it("gives the app's requests route Bloem's page", async () => {
    await expect(resolveFrom("App.tsx", "@/pages/Requests")).resolves.toBe(
      path.join(SRC, "bloem/BloemRequests.tsx"),
    );
  });

  it("leaves Silo's page to its own test and to Bloem's page that wraps it", async () => {
    await expect(resolveFrom("pages/Requests.test.tsx", "./Requests")).resolves.toBeNull();
    await expect(resolveFrom("bloem/BloemRequests.tsx", "@/pages/Requests")).resolves.toBeNull();
  });
});

describe("bloemPreview importer-scoped swaps", () => {
  const ADAPTERS = "bloem/preview/adapters";
  it.each([
    ["App.tsx", "@/contexts/BrandingProvider", "bloem/preview/BrandingWithPreview.tsx"],
    ["App.tsx", "react-router", `${ADAPTERS}/appRouter.tsx`],
    ["components/Layout.tsx", "@/components/AppSidebar", `${ADAPTERS}/AppSidebar.tsx`],
    [
      "components/AdminLayout.tsx",
      "@/components/ServerActivity",
      `${ADAPTERS}/AdminServerActivity.tsx`,
    ],
    [
      "components/AdminSidebar.tsx",
      "@/hooks/queries/admin/system.ts",
      `${ADAPTERS}/adminBuildInfo.ts`,
    ],
    [
      "components/LibraryAccessSelector.tsx",
      "@/components/ui/switch",
      `${ADAPTERS}/LibraryAccessSwitch.tsx`,
    ],
    [
      "pages/admin-users/detail/access/LibraryAccessCard.tsx",
      "./useAccountCardDraft.ts",
      `${ADAPTERS}/libraryCardDraft.ts`,
    ],
    [
      "pages/admin-users/detail/access/EditableCard.tsx",
      "@/i18n/index.ts",
      `${ADAPTERS}/i18nWithBloem.ts`,
    ],
    [
      "components/admin/access-groups/AccessGroupPermissionsSection.tsx",
      "@/i18n/index.ts",
      `${ADAPTERS}/i18nWithBloem.ts`,
    ],
    ["lib/documentTitle.ts", "@/i18n/index.ts", `${ADAPTERS}/i18nWithBloem.ts`],
  ])("gives %s Bloem's %s", async (importer, source, adapter) => {
    await expect(resolveFrom(importer, source)).resolves.toBe(path.join(SRC, adapter));
  });

  it("leaves other importers and the adapter's own import of the original alone", async () => {
    await expect(resolveFrom("components/AppSidebar.test.tsx", "./AppSidebar")).resolves.toBeNull();
    await expect(
      resolveFrom(`bloem/preview/adapters/AppSidebar.tsx`, "@/components/AppSidebar"),
    ).resolves.toBeNull();
    await expect(resolveFrom("pages/AdminUsers.tsx", "@/i18n/index.ts")).resolves.toBeNull();
    await expect(resolveFrom("components/Layout.tsx", "react-router")).resolves.toBeNull();
    await expect(
      resolveFrom(`bloem/preview/adapters/appRouter.tsx`, "react-router"),
    ).resolves.toBeNull();
  });
});

describe("bloemPreview app routes", () => {
  it("gives the app's home and admin requests routes Bloem's pages", async () => {
    await expect(resolveFrom("App.tsx", "@/pages/Home")).resolves.toBe(
      path.join(SRC, "pages/BloemHome.tsx"),
    );
    await expect(resolveFrom("App.tsx", "@/pages/AdminRequests")).resolves.toBe(
      path.join(SRC, "bloem/BloemAdminRequests.tsx"),
    );
  });

  it("leaves the original pages to Bloem's own imports", async () => {
    // null: the plugin leaves the import to Vite, which resolves the original.
    await expect(resolveFrom("pages/BloemHome.tsx", "./Home")).resolves.toBeNull();
    await expect(
      resolveFrom("bloem/BloemAdminRequests.tsx", "@/pages/AdminRequests"),
    ).resolves.toBeNull();
  });
});

describe("bloemPreview watch host", () => {
  it("gives the watch host's player the pre-playback campaign", async () => {
    await expect(
      resolveFrom("playback/WatchPlaybackChrome.tsx", "@/player/components/WatchPage"),
    ).resolves.toBe(path.join(SRC, "bloem/preview/adapters/WatchPage.tsx"));
    await expect(
      resolveFrom("bloem/preview/adapters/WatchPage.tsx", "@/player/components/WatchPage"),
    ).resolves.toBeNull();
  });
});

describe("bloemPreview admin pages", () => {
  it("gives the admin layout Bloem's scoped page outlet", async () => {
    await expect(resolveFrom("components/AdminLayout.tsx", "react-router")).resolves.toBe(
      path.join(SRC, "bloem/preview/adapters/adminLayoutRouter.tsx"),
    );
  });

  it("gives every importer of the access groups page the organization switch", async () => {
    for (const importer of ["App.tsx", "bloem/lazyPages.ts"]) {
      await expect(resolveFrom(importer, "@/pages/AdminAccessGroups")).resolves.toBe(
        path.join(SRC, "bloem/preview/adapters/AdminAccessGroups.tsx"),
      );
    }
    await expect(
      resolveFrom("bloem/preview/adapters/AdminAccessGroups.tsx", "@/pages/AdminAccessGroups"),
    ).resolves.toBeNull();
  });
});
