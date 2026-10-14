const blocked =
  /^\/(?:watch|reader|livetv|rooms|taste-seed|settings|profiles|notifications|requests|title|activate|household-setup|recommendations|favorites|watchlist|history|profile|admin)(?:\/|$)/i;
export function previewRouteRedirect(path: string, search: string): string | null {
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
export function subscriptionHref(url: string, item?: string, ref?: string): string | null {
  try {
    const target = new URL(url);
    if (target.protocol !== "https:" && target.protocol !== "http:") return null;
    target.searchParams.set("src", "preview");
    target.searchParams.delete("item");
    target.searchParams.delete("ref");
    if (item) target.searchParams.set("item", item);
    if (ref) target.searchParams.set("ref", ref);
    return target.href;
  } catch {
    return null;
  }
}
export function safePreviewHref(url: string | undefined): string | null {
  try {
    const target = new URL(url ?? "");
    return ["http:", "https:"].includes(target.protocol) ? target.href : null;
  } catch {
    return null;
  }
}
