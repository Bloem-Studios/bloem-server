/**
 * Vite plugin: rebrand upstream's prose as the bundle is built.
 *
 * See web/src/lib/product-brand.ts for why this is a build step and not an edit
 * to the source. Keeping the source identical to upstream is what makes this a
 * tracking fork rather than a hard one.
 */
import type { Plugin } from "vite";
import { applyBrand } from "./src/lib/product-brand";

/** Source that carries user-facing prose. Assets and generated output are left
 *  alone; the wordmark is an image and is replaced by swapping the file. */
const BRANDABLE = /\.(ts|tsx|html)$/;

export function bloemBrand(): Plugin {
  return {
    name: "bloem-brand",
    // After other transforms, so the string we rewrite is the one that ships.
    enforce: "post",
    transform(code, id) {
      if (!BRANDABLE.test(id) || id.includes("node_modules")) return null;
      // product-brand.ts declares the names themselves; rebranding it would
      // rewrite the definition of what a brand is.
      if (id.includes("product-brand")) return null;
      const branded = applyBrand(code);
      return branded === code ? null : { code: branded, map: null };
    },
    transformIndexHtml(html) {
      return applyBrand(html);
    },
  };
}
