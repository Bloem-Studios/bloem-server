import { lazy, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router";
import { useBloemT } from "@/bloem/i18n";
import { useAuth } from "@/hooks/useAuth";
import { previewDiscoveryPending } from "./session";
import { usePreviewMode } from "./usePreviewMode";
import { freePlayRoute, previewRouteRedirect } from "./routes";
const PreviewLanding = lazy(() => import("./PreviewLanding"));
// The pages App.tsx serves outside RequireAuth. They never redirect to sign in,
// so the preview status wait below does not hold them.
const PUBLIC_PAGE =
  /^\/(?:login|activate|setup|signup|invite|reset-password|forgot-password|change-password|rooms\/join|household-setup)(?:\/|$)/i;
export function RequireFullAccess({ children }: { children: ReactNode }) {
  const mode = usePreviewMode();
  const location = useLocation();
  if (mode.active && !(mode.freePlay && freePlayRoute(location.pathname)))
    return (
      <Navigate
        to={previewRouteRedirect(location.pathname, location.search) ?? "/?cta=1"}
        replace
      />
    );
  return children;
}
function LoadingApplication() {
  const bt = useBloemT();
  return (
    <div className="p-8" role="status" aria-live="polite">
      <span className="sr-only">{bt("bloem.preview.route.loadingApplication")}</span>
      {bt("bloem.preview.route.loading")}
    </div>
  );
}
export function PreviewRouteBoundary({ children }: { children: ReactNode }) {
  const mode = usePreviewMode();
  const { user } = useAuth();
  const location = useLocation();
  if (location.pathname === "/preview/start") return children;
  // A signed-out visitor on a page behind sign-in waits for the preview status,
  // so a preview host shows its landing instead of first sending them to sign in.
  if (!user && !mode.host && previewDiscoveryPending() && !PUBLIC_PAGE.test(location.pathname))
    return <LoadingApplication />;
  if (
    mode.host &&
    (/^\/(?:login|signup|invite|reset-password|forgot-password|choose-password|setup)(?:\/|$)/i.test(
      location.pathname,
    ) ||
      !mode.active)
  )
    return <PreviewLanding />;
  if (mode.ended) return <PreviewLanding />;
  const redirect = mode.active
    ? previewRouteRedirect(location.pathname, location.search, mode.freePlay !== null)
    : null;
  return redirect ? <Navigate to={redirect} replace /> : children;
}
