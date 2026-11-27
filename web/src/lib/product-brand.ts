/**
 * The product's name, applied at BUILD TIME rather than written into the source.
 *
 * Bloem Server is a tracking fork of Silo Server (see FORK.md): upstream's
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
export const PRODUCT_NAME = "Bloem";

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
// Plugin authorship is provenance, so the "Made by Silo" credit stays intact.
// Silo's relay card is titled "Silo Push Relay": that relay is Silo's (below).
const BRAND_PATTERN = new RegExp(
  `(^|[^A-Za-z0-9_/.\\-])(?<!Made by )${UPSTREAM_NAME}(?![A-Za-z0-9_\\-])(?! Push Relay)`,
  "g",
);

export function applyBrand(source: string, productName: string = PRODUCT_NAME): string {
  return source.replace(BRAND_PATTERN, (_match, before: string) => `${before}${productName}`);
}

/**
 * Silo's messages about Silo's push relay keep Silo's name. Silo's apps on
 * this server are woken through Silo's relay
 * (docs/architecture/bloem-push-relays.md), and these describe that relay:
 * the relay card, its settings search entry and the setup step. Bloem's own
 * relay, for the Bloem Admin app, has its own card in Bloem's catalog.
 */
const SILO_RELAY_MESSAGES = new Set([
  // Catalog keys the build matches; this module loads no text.
  // eslint-disable-next-line silo-admin-text/no-admin-message -- a key the build matches, not text shown
  "web.admin.settings.notifications.relay.description",
  // eslint-disable-next-line silo-admin-text/no-admin-message -- a key the build matches, not text shown
  "web.admin.settings.search.notifications.setting.siloPushRelay",
  "web.setup.features.pushDescription",
]);

/**
 * The viewer's privacy notice names Silo's relay, which stays Silo's. Older
 * wording also named the server first ("your Silo Server sends…"), which is
 * this build; translations that still carry it keep that order. A notice with
 * a single Silo ("your server sends… to Silo's push relay") is left as it is.
 */
const SERVER_THEN_SILO_RELAY = new Set(["web.notifications.pushPrivacy.relay"]);

/** Brand one catalog message, given as JSON text, under its key's rule. */
function brandMessage(key: string, message: string, productName: string): string {
  if (SILO_RELAY_MESSAGES.has(key)) return message;
  if (!SERVER_THEN_SILO_RELAY.has(key)) return applyBrand(message, productName);
  if ((message.match(BRAND_PATTERN) ?? []).length < 2) return message;
  let first = true;
  return message.replace(BRAND_PATTERN, (match: string, before: string) => {
    if (!first) return match;
    first = false;
    return `${before}${productName}`;
  });
}

/** Rebrand a Silo message catalog (key to message), keeping Silo's relay messages Silo's. */
export function applyCatalogBrand(
  messages: Record<string, unknown>,
  productName: string = PRODUCT_NAME,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [key, message] of Object.entries(messages))
    out[key] = JSON.parse(brandMessage(key, JSON.stringify(message), productName)) as unknown;
  return out;
}
