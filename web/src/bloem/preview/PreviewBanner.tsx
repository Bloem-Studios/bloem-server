import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { API_READ_TIMEOUT_MS, startRequestDeadline } from "@/api/requestDeadline";
import { getAccessToken } from "@/api/client";
import { useAuth } from "@/hooks/useAuth";
import { useBranding } from "@/hooks/useBranding";
import { endGuestSession } from "./session";
import { usePreviewMode } from "./usePreviewMode";
import { safePreviewHref } from "./routes";
import { previewEvent } from "./api";
import { Button } from "@/components/ui/button";
export default function PreviewBanner() {
  const { active, guest, ended, presentation, requestSubscription } = usePreviewMode();
  const { logout } = useAuth();
  const { serverName } = useBranding();
  const [now, setNow] = useState(Date.now);
  const [exiting, setExiting] = useState(false);
  const banner = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (!active || !banner.current) return;
    const element = banner.current;
    const root = document.documentElement;
    const measure = () => {
      const height = element.getBoundingClientRect().height;
      if (height > 0)
        root.style.setProperty("--bloem-preview-banner-height", `${Math.ceil(height)}px`);
    };
    measure();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(measure);
    observer?.observe(element);
    window.addEventListener("resize", measure);
    return () => {
      observer?.disconnect();
      window.removeEventListener("resize", measure);
      root.style.removeProperty("--bloem-preview-banner-height");
    };
  }, [active]);
  useEffect(() => {
    if (!active || !guest || ended) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [active, guest, ended]);
  const end = presentation?.ends_at ? Date.parse(presentation.ends_at) : null;
  useEffect(() => {
    if (guest && !ended && end !== null && now >= end) {
      previewEvent("session_ended");
      endGuestSession();
    }
  }, [guest, ended, end, now]);
  if (!active) return null;
  const remaining = end === null ? null : Math.max(0, Math.ceil((end - now) / 60000));
  const deadline = guest
    ? remaining === null
      ? "Browse-only preview"
      : `${remaining} min remaining`
    : end === null
      ? "Your library preview"
      : `Your preview ends on ${new Date(end).toLocaleDateString()}`;
  // Ending a guest visit frees its place; the guest then lands on the store,
  // or on the operator's subscription when they chose to subscribe.
  async function exit(destination: string | undefined) {
    const href = safePreviewHref(destination);
    const token = getAccessToken();
    setExiting(true);
    const deadline = startRequestDeadline(
      API_READ_TIMEOUT_MS,
      () => new Error("Preview exit timed out"),
    );
    try {
      if (token)
        await fetch("/api/v2/auth/logout", {
          method: "POST",
          headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
          body: "{}",
          keepalive: true,
          signal: deadline.signal,
        }).catch(() => {});
    } finally {
      deadline.dispose();
    }
    endGuestSession();
    if (href) window.location.assign(href);
  }
  // With a subscription link the banner offers it beside the trial, in place
  // of the plain exit.
  const subscribe = guest ? safePreviewHref(presentation?.subscribe_url) : null;
  return (
    <div
      ref={banner}
      role="status"
      className="bg-primary/10 sticky top-0 z-50 flex flex-wrap items-center gap-3 border-b px-4 py-2 text-sm"
    >
      <span>{deadline}</span>
      <Button size="sm" className="ml-auto" onClick={() => requestSubscription()}>
        {presentation?.cta_label || `Subscribe to ${serverName}`}
      </Button>
      {subscribe ? (
        <Button size="sm" variant="outline" disabled={exiting} onClick={() => void exit(subscribe)}>
          {presentation?.subscribe_label || "Subscribe"}
        </Button>
      ) : (
        <Button
          size="sm"
          variant="ghost"
          disabled={exiting}
          onClick={guest ? () => void exit(presentation?.storefront_url) : logout}
        >
          {guest ? "Exit preview" : "Sign out"}
        </Button>
      )}
    </div>
  );
}
