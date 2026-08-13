/**
 * The product's name, applied at BUILD TIME rather than written into the source.
 *
 * Vondel Server is a tracking fork of Silo Server (see FORK.md): upstream's
 * history is merged continuously, so every line this fork edits is a line that
 * conflicts on some future merge. Renaming the product in the source would mean
 * editing ~87 web files — and they are precisely the files upstream edits most,
 * so the rebrand would be the entire ongoing cost of the fork.
 *
 * Instead the source keeps upstream's wording and the name is substituted as
 * the bundle is built. Source divergence is one plugin file. An upstream change
 * to any string merges cleanly, and any NEW upstream copy is rebranded
 * automatically rather than being missed until somebody notices.
 *
 * The transform is a pure function so it can be tested without building.
 */

/** What this build calls itself. */
export const PRODUCT_NAME = "Vondel";

/** Upstream's name for itself, as it appears in prose. */
const UPSTREAM_NAME = "Silo";

/**
 * Rewrite upstream's product name in user-facing prose, and nothing else.
 *
 * The word is only a brand when it stands alone. Everywhere else it is load
 * bearing and renaming it breaks the build or the wire:
 *
 *   SiloBrand, SiloThemeFile     component and type names
 *   silo_profile_id              a field name the API and database agree on
 *   silo.autoscan.arr            a plugin identifier
 *   /silo-wordmark-sidebar.png   an asset that exists under that path
 *   github.com/Silo-Server/...   upstream's module path and URLs
 *   /tmp/silo-transcode          a path
 *
 * So: match `Silo` only when it is not part of a longer identifier — nothing
 * word-like or path-like on either side. Lowercase `silo` is left entirely
 * alone, because every occurrence of it is technical.
 */
const BRAND_PATTERN = new RegExp(`(^|[^A-Za-z0-9_/.\\-])${UPSTREAM_NAME}(?![A-Za-z0-9_\\-])`, "g");

export function applyBrand(source: string, productName: string = PRODUCT_NAME): string {
  return source.replace(BRAND_PATTERN, (_match, before: string) => `${before}${productName}`);
}
