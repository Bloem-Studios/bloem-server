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

/** Resolves like Vite: "@/" is src, a relative source is beside its importer, ".tsx" appended. */
function resolveFrom(importer: string, source: string): Promise<string | null> {
  const plugin = bloemPreview();
  (plugin.configResolved as (config: { root: string }) => void)({ root: WEB_ROOT });
  const importerPath = path.join(SRC, importer);
  const context = {
    async resolve(spec: string, from: string) {
      const base = spec.startsWith("@/")
        ? path.join(SRC, spec.slice(2))
        : path.resolve(path.dirname(from), spec);
      return { id: base.endsWith(".tsx") ? base : `${base}.tsx` };
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
