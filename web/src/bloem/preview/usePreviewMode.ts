import { createContext, useContext, useSyncExternalStore } from "react";
import {
  guestHost,
  guestSession,
  previewSessionRevision,
  subscribePreviewSession,
  type PreviewPresentation,
} from "./session";
export const PreviewModeContext = createContext<PreviewPresentation | null>(null);
export const PreviewCtaContext = createContext<(item?: string) => void>(() => {});
export function usePreviewMode() {
  useSyncExternalStore(subscribePreviewSession, previewSessionRevision, previewSessionRevision);
  const guest = guestSession();
  const presentation =
    useContext(PreviewModeContext) ?? (guestHost() ? (guest?.presentation ?? null) : null);
  return {
    active: presentation !== null,
    guest: presentation?.kind === "guest",
    presentation,
    host: guestHost(),
    ended: guestHost() && guest?.ended === true,
    requestSubscription: useContext(PreviewCtaContext),
  };
}
