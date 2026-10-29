const blocked =
  /^\/(?:watch|reader|livetv|rooms|taste-seed|settings|profiles|notifications|requests|title|activate|household-setup|recommendations|favorites|watchlist|history|profile|admin)(?:\/|$)/i;
// Free play opens the video player and nothing else that is blocked.
export function freePlayRoute(path: string): boolean {
  return /^\/watch\/[^/]+\/?$/i.test(path);
}
export function previewRouteRedirect(
  path: string,
  search: string,
  freePlay = false,
): string | null {
  if (freePlay && freePlayRoute(path)) return null;
  const params = new URLSearchParams(search);
  const collectionWrite = /^\/collections\/(?:new|[^/]+\/edit)(?:\/|$)/i.test(path);
  const personalCatalog =
    path === "/catalog" &&
    ["favorites", "watchlist", "history", "recommendations", "user_collection"].includes(
      params.get("source") ?? "",
    );
  if (!blocked.test(path) && !collectionWrite && !personalCatalog) return null;
  const back = params.get("backTo");
  if (back && /^\/item\/[^/?#\\]+(?:\?[^#\\]*)?$/.test(back)) {
    const url = new URL(back, window.location.origin);
    url.searchParams.set("cta", "1");
    return url.pathname + url.search;
  }
  const content = path.match(/^\/(?:watch|reader\/ebook)\/([^/]+)\/?$/i)?.[1];
  return content ? `/item/${content}?cta=1` : "/?cta=1";
}
// The title the visitor was looking at rides along as preview_item. The link's
// own parameters are the operator's and stay exactly as written: a store's cart
// link names its plan with item=, and replacing that with the title emptied the
// cart the Subscribe button was meant to fill.
export function subscriptionHref(url: string, item?: string, ref?: string): string | null {
  try {
    const target = new URL(url);
    if (target.protocol !== "https:" && target.protocol !== "http:") return null;
    target.searchParams.set("src", "preview");
    target.searchParams.delete("preview_item");
    target.searchParams.delete("ref");
    if (item) target.searchParams.set("preview_item", item);
    if (ref) target.searchParams.set("ref", ref);
    return target.href;
  } catch {
    return null;
  }
}
// The storefront's free-play request page, for the title the guest was on.
export function freePlayHref(url: string | undefined, item?: string, ref?: string): string | null {
  const href = safePreviewHref(url);
  if (!href) return null;
  const target = new URL(href);
  if (item) target.searchParams.set("item", item);
  if (ref) target.searchParams.set("ref", ref);
  return target.href;
}
export function safePreviewHref(url: string | undefined): string | null {
  try {
    const target = new URL(url ?? "");
    return ["http:", "https:"].includes(target.protocol) ? target.href : null;
  } catch {
    return null;
  }
}
