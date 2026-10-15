import { getAccessToken } from "@/api/client";
import { API_READ_TIMEOUT_MS, startRequestDeadline } from "@/api/requestDeadline";
import { guestRefreshToken, setGuestHost } from "./session";

export interface PreviewStatus {
  enabled: boolean;
  brand?: string;
  cta_url?: string;
  cta_label?: string;
  storefront_url?: string;
  launch_url?: string;
}
export interface GuestExchange {
  access_token: string;
  refresh_token: string;
  expires_in: number;
  expires_at: string;
  item: string;
  ref?: string;
}
async function publicRequest<T>(path: string, body?: unknown): Promise<T> {
  const deadline = startRequestDeadline(
    API_READ_TIMEOUT_MS,
    () => new Error("Preview request timed out"),
  );
  try {
    const response = await fetch(`/api/bloem/v1/preview/${path}`, {
      method: body === undefined ? "GET" : "POST",
      headers: {
        Accept: "application/json",
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: deadline.signal,
      cache: "no-store",
    });
    if (!response.ok) throw new Error("The preview could not start. Please use a new launch link.");
    return (await response.json()) as T;
  } finally {
    deadline.dispose();
  }
}
let discovery: Promise<PreviewStatus | null> | null = null;
export async function discoverPreviewHost() {
  discovery ??= publicRequest<PreviewStatus>("status").catch(() => null);
  const status = await discovery;
  if (status) setGuestHost(typeof status.brand === "string");
  return status;
}
export async function exchangePreviewTicket(ticket: string) {
  const status = await discoverPreviewHost();
  if (!status || typeof status.brand !== "string" || !status.enabled)
    throw new Error("Use the storefront's preview launch link.");
  return publicRequest<GuestExchange>("sessions", {
    ticket,
    ...(guestRefreshToken() ? { refresh_token: guestRefreshToken() } : {}),
  });
}
export function previewEvent(event: "cta_shown" | "cta_clicked" | "session_ended") {
  const token = getAccessToken();
  if (!token) return;
  void fetch("/api/bloem/v1/preview/events", {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ event }),
    keepalive: true,
  }).catch(() => {});
}
