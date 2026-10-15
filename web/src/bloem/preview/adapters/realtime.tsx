/* eslint-disable react-refresh/only-export-components -- Preserve the original module exports at this adapter boundary. */
import type { ReactNode } from "react";
import { RealtimeEventsProvider as Original } from "../../../components/RealtimeEventsProvider";
import { usePreviewMode } from "../usePreviewMode";
export * from "../../../components/RealtimeEventsProvider";
export function RealtimeEventsProvider({ children }: { children: ReactNode }) {
  return usePreviewMode().active ? children : <Original>{children}</Original>;
}
