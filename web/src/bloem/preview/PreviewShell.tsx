import "./preview.css";
import { lazy, Suspense, useCallback, useEffect, useState, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router";
import { PreviewCtaContext, PreviewModeContext, usePreviewMode } from "./usePreviewMode";
import { useAuth } from "@/hooks/useAuth";
import { guestSession, previewForUser } from "./session";
import { previewEvent } from "./api";
const PreviewBanner = lazy(() => import("./PreviewBanner"));
const SubscribeCtaDialog = lazy(() => import("./SubscribeCtaDialog"));
export function PreviewShell({ children }: { children: ReactNode }) {
  const { user } = useAuth();
  const mode = usePreviewMode();
  const presentation = previewForUser(user) ?? mode.presentation;
  const location = useLocation();
  const navigate = useNavigate();
  const [dialog, setDialog] = useState<{ item?: string } | null>(null);
  const [dismissedEnd, setDismissedEnd] = useState<string | null>(null);
  const expiry = guestSession()?.expires_at ?? null;
  const routeCta = presentation !== null && new URLSearchParams(location.search).get("cta") === "1";
  const routeItem = location.pathname.match(/^\/item\/(.+)$/)?.[1];
  const request = useCallback((item?: string) => {
    setDialog({ item });
    previewEvent("cta_shown");
  }, []);
  useEffect(() => {
    if (routeCta) previewEvent("cta_shown");
  }, [routeCta, location.key]);
  function close(open: boolean) {
    if (open) return;
    setDialog(null);
    setDismissedEnd(expiry);
    if (routeCta) {
      const params = new URLSearchParams(location.search);
      params.delete("cta");
      navigate({ pathname: location.pathname, search: params.toString() }, { replace: true });
    }
  }
  return (
    <PreviewModeContext.Provider value={presentation}>
      <PreviewCtaContext.Provider value={request}>
        <div className={presentation ? "bloem-preview contents" : "contents"}>
          <Suspense fallback={null}>{presentation && <PreviewBanner />}</Suspense>
          {children}
          <Suspense fallback={null}>
            {presentation !== null &&
              (dialog !== null || routeCta || (mode.ended && dismissedEnd !== expiry)) && (
                <SubscribeCtaDialog
                  open={
                    presentation !== null &&
                    (dialog !== null || routeCta || (mode.ended && dismissedEnd !== expiry))
                  }
                  item={
                    dialog?.item ??
                    (routeCta && routeItem ? decodeURIComponent(routeItem) : undefined)
                  }
                  ended={mode.ended}
                  freePlayEnded={
                    guestSession()?.free_play_ended === true && !presentation?.free_play
                  }
                  onOpenChange={close}
                />
              )}
          </Suspense>
        </div>
      </PreviewCtaContext.Provider>
    </PreviewModeContext.Provider>
  );
}
