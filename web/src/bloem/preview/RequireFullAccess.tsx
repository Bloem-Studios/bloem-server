import { lazy, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router";
import { usePreviewMode } from "./usePreviewMode";
import { freePlayRoute, previewRouteRedirect } from "./routes";
const PreviewLanding = lazy(() => import("./PreviewLanding"));
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
export function PreviewRouteBoundary({ children }: { children: ReactNode }) {
  const mode = usePreviewMode();
  const location = useLocation();
  if (location.pathname === "/preview/start") return children;
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
