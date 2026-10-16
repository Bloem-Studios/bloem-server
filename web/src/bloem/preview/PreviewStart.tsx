import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { useAuth } from "@/hooks/useAuth";
import { setAccessToken } from "@/api/client";
import { v2 } from "@/api/v2/request";
import { userFromAccount } from "@/api/v2/account";
import { exchangePreviewTicket, type GuestExchange } from "./api";
import {
  guestRefreshToken,
  guestSession,
  setGuestHost,
  setGuestProfile,
  storeGuestSession,
} from "./session";
import PreviewLanding from "./PreviewLanding";
let flight: { ticket: string; promise: Promise<GuestExchange> } | null = null;
function captureLaunch() {
  const ticket = new URLSearchParams(window.location.hash.slice(1)).get("ticket");
  if (ticket) {
    // Clear before any asynchronous work, including StrictMode's second mount.
    history.replaceState(history.state, "", window.location.pathname + window.location.search);
    setGuestHost(true);
    if (flight?.ticket !== ticket) {
      flight = { ticket, promise: exchangePreviewTicket(ticket) };
      void flight.promise.catch(() => {});
    }
    return flight;
  }
  return flight;
}
export default function PreviewStart() {
  const { completeLogin, loading, user } = useAuth();
  const navigate = useNavigate();
  const [launch] = useState(captureLaunch);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    if (loading || !launch) return;
    let cancelled = false;
    void launch.promise
      .then(async (pair) => {
        if (cancelled) return;
        storeGuestSession(pair);
        setAccessToken(pair.access_token);
        const [account, profiles] = await Promise.all([
          v2("GET /api/v2/account/me"),
          v2("GET /api/v2/profiles"),
        ]);
        if (cancelled) return;
        // The shared account's own profile scopes browsing; the guest is never
        // asked to pick one.
        const profile = profiles.items.find((item) => item.is_primary) ?? profiles.items[0] ?? null;
        setGuestProfile(profile?.id ?? null);
        completeLogin({ ...pair, user: userFromAccount(account) });
        if (flight === launch) flight = null;
        navigate(pair.item ? `/item/${encodeURIComponent(pair.item)}` : "/", { replace: true });
      })
      .catch(() => {
        if (!cancelled) {
          if (flight === launch) flight = null;
          setFailed(true);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [launch, loading, completeLogin, navigate]);
  useEffect(() => {
    if (loading || launch) return;
    if (user && guestRefreshToken()) {
      const item = guestSession()?.item;
      navigate(item ? `/item/${encodeURIComponent(item)}` : "/", { replace: true });
    }
  }, [launch, loading, user, navigate]);
  if (failed || (!loading && !launch && !(user && guestRefreshToken()))) return <PreviewLanding />;
  return (
    <main role="status" className="p-8">
      Opening your library preview…
    </main>
  );
}
