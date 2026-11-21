/**
 * Vite plugin: retain Bloem presentation while keeping upstream source intact.
 *
 * Public files bypass module transforms, so development serves adapted bytes
 * and builds emit them before the existing compression hook reads output.
 */
import { readFile } from "node:fs/promises";
import path from "node:path";
import type { Plugin } from "vite";
import { brandTestRegexes } from "./product-brand-regex";
import { applyBrand, applyCatalogBrand, PRODUCT_NAME } from "./src/lib/product-brand";
import { applyBloemTestConfig } from "./bloem-test-config";

const BRANDABLE = /\.(ts|tsx|html)$/;
// Silo's generated message catalogs, where its interface text lives (src/i18n).
// Bloem's own catalog (src/bloem/locales) is Bloem's wording and stays as written.
const SILO_CATALOG = /\/src\/locales\/[^/]+\.json$/;
const PUBLIC_ASSETS = ["site.webmanifest", "sw.js"] as const;
// The server renders the shell's title: branding.RenderIndexHTML replaces this
// exact upstream literal with the configured server name (Bloem by default), so
// a build must ship it unchanged. Only the dev server, which has no server
// rendering in front of it, brands the title itself.
const SERVER_RENDERED_TITLE = "<title>Silo</title>";

/**
 * Brand a Silo catalog module message by message, so Silo's push relay
 * messages keep Silo's name (applyCatalogBrand). A catalog is megabytes, and
 * Vite hands one on as `export default JSON.parse("...")`; any other form is
 * branded as text, as before.
 */
function brandCatalogModule(code: string): string | undefined {
  const call = "JSON.parse(";
  const start = code.indexOf(call);
  const end = code.lastIndexOf(")");
  if (!code.startsWith("export default") || start < 0 || end < start) return undefined;
  let messages: unknown;
  try {
    messages = JSON.parse(JSON.parse(code.slice(start + call.length, end)) as string);
  } catch {
    return undefined;
  }
  if (!messages || typeof messages !== "object" || Array.isArray(messages)) return undefined;
  const branded = applyCatalogBrand(messages as Record<string, unknown>);
  return (
    code.slice(0, start + call.length) + JSON.stringify(JSON.stringify(branded)) + code.slice(end)
  );
}

function brandPublicAsset(fileName: (typeof PUBLIC_ASSETS)[number], source: string): string {
  if (fileName === "site.webmanifest") {
    // Keep custom names and every other manifest field byte-for-byte.
    return source.replace(
      /("(?:name|short_name)"\s*:\s*")Silo(")/g,
      (_match, before: string, after: string) => before + PRODUCT_NAME + after,
    );
  }
  // Only the built-in fallback changes; payload titles and the worker protocol
  // are evaluated by the original upstream code.
  return source.replace('data.title || "Silo"', "data.title || " + JSON.stringify(PRODUCT_NAME));
}

export function bloemBrand(): Plugin {
  let publicDir = "";
  return {
    name: "bloem-brand",
    // After other transforms, so the string we rewrite is the one that ships.
    enforce: "post",
    // Bloem's Vitest settings (bloem-test-config.ts) ride on this plugin.
    config(config) {
      applyBloemTestConfig(config);
    },
    configResolved(config) {
      publicDir = config.publicDir;
    },
    transform(code, id) {
      const sourceId = id.replace(/\?.*$/, "").replaceAll("\\", "/");
      if (
        !(BRANDABLE.test(sourceId) || SILO_CATALOG.test(sourceId)) ||
        sourceId.includes("node_modules")
      )
        return null;
      // Brand definitions and adapter tests need their original upstream literals.
      if (
        sourceId.includes("product-brand") ||
        /\/bloem-brand-plugin(?:\.test)?\.ts$/.test(sourceId)
      )
        return null;
      let branded = code;
      if (sourceId.endsWith("/src/components/SiloBrand.tsx")) {
        // Exact built-in literals only: custom URLs, including suffixes and
        // query strings, retain their original value.
        branded = branded.replace(
          /(["'])\/silo-(wordmark-sidebar|icon-1024)\.png\1/g,
          (_match, quote: string, asset: string) => quote + "/bloem-" + asset + ".png" + quote,
        );
      }
      const catalog = SILO_CATALOG.test(sourceId) ? brandCatalogModule(branded) : undefined;
      branded =
        catalog ?? applyBrand(/\.test\.tsx?$/.test(sourceId) ? brandTestRegexes(branded) : branded);
      return branded === code ? null : { code: branded, map: null };
    },
    transformIndexHtml(html, ctx) {
      if (ctx.server) return applyBrand(html);
      return html
        .split(SERVER_RENDERED_TITLE)
        .map((part) => applyBrand(part))
        .join(SERVER_RENDERED_TITLE);
    },
    configureServer(server) {
      if (!publicDir) return;
      // Register before Vite's public-file middleware; never edit public/.
      server.middlewares.use(async (req, res, next) => {
        if (req.method !== "GET" && req.method !== "HEAD") return next();
        const pathname = new URL(req.url ?? "/", "http://vite.local").pathname;
        const base = server.config.base;
        if (!pathname.startsWith(base)) return next();
        const fileName = pathname.slice(base.length);
        if (fileName !== "site.webmanifest" && fileName !== "sw.js") return next();
        try {
          const source = await readFile(path.join(publicDir, fileName), "utf8");
          const branded = brandPublicAsset(fileName, source);
          res.setHeader(
            "Content-Type",
            fileName === "site.webmanifest"
              ? "application/manifest+json; charset=utf-8"
              : "application/javascript; charset=utf-8",
          );
          res.setHeader("Cache-Control", "no-cache");
          res.end(req.method === "HEAD" ? undefined : branded);
        } catch (error) {
          next(error);
        }
      });
    },
    async generateBundle() {
      if (!publicDir) return;
      // Rollup writes these over Vite's copied public files. Emitting the worker
      // also includes it in the bundle consumed by precompressStaticAssets.
      for (const fileName of PUBLIC_ASSETS) {
        const source = await readFile(path.join(publicDir, fileName), "utf8");
        this.emitFile({ type: "asset", fileName, source: brandPublicAsset(fileName, source) });
      }
    },
  };
}
