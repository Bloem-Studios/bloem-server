import type { ComponentProps } from "react";
import { Outlet as RouterOutlet } from "react-router";

import { PreviewRouteBoundary } from "../RequireFullAccess";

// eslint-disable-next-line react-refresh/only-export-components -- the app's react-router, with one component replaced
export * from "react-router";

/** The app shell's route outlet, behind the preview's route gate. */
export function Outlet(props: ComponentProps<typeof RouterOutlet>) {
  return (
    <PreviewRouteBoundary>
      <RouterOutlet {...props} />
    </PreviewRouteBoundary>
  );
}
