import type { User } from "@/api/types";
import type { components } from "@/api/v2/schema";

export type PreviewPresentation = components["schemas"]["BloemPreviewPresentation"];
export interface GuestSession {
  refresh_token: string | null;
  expires_at: string;
  item: string;
  ref?: string;
  ended?: boolean;
  /** Set once this guest's free play ran out, so it is not offered again. */
  free_play_ended?: boolean;
  presentation?: PreviewPresentation;
  /** The shared account's profile. A guest never picks one, but the v2 home
   * and library layouts require X-Profile-Id. */
  profile_id?: string;
}
const KEY = "bloem-preview-session";
const HOST_KEY = "bloem-preview-host";
function read(): GuestSession | null {
  try {
    const value = JSON.parse(sessionStorage.getItem(KEY) ?? "null") as GuestSession | null;
    if (
      !value ||
      typeof value.expires_at !== "string" ||
      !Number.isFinite(Date.parse(value.expires_at)) ||
      typeof value.item !== "string" ||
      (value.refresh_token !== null && typeof value.refresh_token !== "string") ||
      (value.ref !== undefined && typeof value.ref !== "string") ||
      (value.profile_id !== undefined && typeof value.profile_id !== "string")
    )
      return null;
    return value;
  } catch {
    return null;
  }
}
let current = read();
let host =
  (typeof window !== "undefined" && window.location.pathname.startsWith("/preview/")) ||
  current !== null;
try {
  host ||= sessionStorage.getItem(HOST_KEY) === "1";
} catch {
  /* optional */
}
let revision = 0;
const listeners = new Set<() => void>();
export function subscribePreviewSession(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
export function previewSessionRevision() {
  return revision;
}
function changed() {
  revision++;
  listeners.forEach((listener) => listener());
}
function persist() {
  try {
    if (current) sessionStorage.setItem(KEY, JSON.stringify(current));
    else sessionStorage.removeItem(KEY);
  } catch {
    /* Tab memory still works when storage is unavailable. */
  }
  changed();
}
export function guestHost() {
  return host;
}
export function setGuestHost(value: boolean) {
  if (host === value) return;
  host = value;
  try {
    if (value) sessionStorage.setItem(HOST_KEY, "1");
    else sessionStorage.removeItem(HOST_KEY);
  } catch {
    /* optional */
  }
  changed();
}
export function guestSession() {
  return current;
}
export function guestRefreshToken() {
  return current?.refresh_token ?? null;
}
export function guestProfileId() {
  return current?.ended ? null : (current?.profile_id ?? null);
}
export function setGuestProfile(id: string | null) {
  if (current) {
    current = { ...current, profile_id: id ?? undefined };
    persist();
  }
}
export function storeGuestSession(value: GuestSession) {
  host = true;
  current = { ...value, ended: false };
  persist();
}
export function setGuestRefreshToken(token: string | null) {
  if (current) {
    current = { ...current, refresh_token: token };
    persist();
  }
}
export function endGuestSession() {
  if (current) {
    current = { ...current, refresh_token: null, ended: true };
    persist();
  }
}
export function previewForUser(user: User | null | undefined): PreviewPresentation | null {
  return (user as (User & { preview?: PreviewPresentation | null }) | undefined)?.preview ?? null;
}
export function markFreePlayEnded() {
  if (current && !current.free_play_ended) {
    current = { ...current, free_play_ended: true };
    persist();
  }
}
export function rememberGuestPresentation(value: PreviewPresentation) {
  if (value.kind === "guest" && current) {
    current = { ...current, presentation: value };
    persist();
  }
}
