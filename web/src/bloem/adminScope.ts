/**
 * Whether an admin page needs the platform context. Every page does except
 * the organization pages, which carry their own organization guard, and the
 * context chooser. Matching is case-insensitive, as route matching is.
 */
export function adminPathRequiresPlatform(pathname: string): boolean {
  const page = pathname.replace(/^\/admin(?=\/|$)/i, "");
  return !/^\/(organization|context)(\/|$)/i.test(page);
}
