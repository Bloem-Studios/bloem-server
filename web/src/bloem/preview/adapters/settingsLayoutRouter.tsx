import { Outlet as RouterOutlet, useLocation, type OutletProps } from "react-router";

import { isSourceCodeSettingsPath } from "@/bloem/sourceCode/api";
import SourceCodeSettings from "@/bloem/sourceCode/SourceCodeSettings";

// eslint-disable-next-line react-refresh/only-export-components -- the settings layout's react-router, with one component replaced
export * from "react-router";

/**
 * The personal settings layout's page outlet. At /settings/source-code it shows
 * Bloem's Source code page inside Silo's settings frame; App.tsx has no route
 * for it, so the real outlet would render the `*` redirect to playback there.
 * Every other path gets the real outlet.
 */
export function Outlet(props: OutletProps) {
  const { pathname } = useLocation();
  if (isSourceCodeSettingsPath(pathname)) return <SourceCodeSettings />;
  return <RouterOutlet {...props} />;
}
