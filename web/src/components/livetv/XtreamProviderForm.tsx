import { useEffect, useRef, useState, useSyncExternalStore, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  captureProfileRequestContext,
  captureSessionIdentity,
  getProfileId,
  getProfileTokenGeneration,
  isSessionIdentityCurrent,
  nativeApiWithProfileRequestContext,
  StaleApiRequestContextError,
} from "@/api/client";
import type { LiveTVTuner, LiveTVTunersResponse } from "@/api/types";
import { useAuth } from "@/hooks/useAuth";
import { adminKeys } from "@/hooks/queries/keys";
import { useLiveTVAccess } from "@/hooks/queries/useLiveTVAccess";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

type Phase = "initial" | "ready" | "pending" | "reloading" | "reload-required";
// Only non-secret authority identities and reconciliation state survive a
// remount. Credentials never enter React state, query/mutation caches or storage.
const states = new Map<string, { phase: Phase; version: number }>();
const listeners = new Set<() => void>();
let version = 0;
function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
function begin(scope: string, phase: Phase) {
  const next = ++version;
  states.set(scope, { phase, version: next });
  for (const [key, state] of states) {
    if (states.size <= 64) break;
    if (key !== scope && state.phase !== "pending" && state.phase !== "reloading")
      states.delete(key);
  }
  listeners.forEach((listener) => listener());
  return next;
}
function finish(scope: string, attempt: number, phase: Phase) {
  if (states.get(scope)?.version !== attempt) return false;
  states.set(scope, { phase, version: attempt });
  listeners.forEach((listener) => listener());
  return true;
}

export function XtreamProviderForm() {
  const { user } = useAuth();
  const capability = useLiveTVAccess();
  const identity = captureSessionIdentity();
  const profileId = getProfileId();
  const pinGeneration = getProfileTokenGeneration();
  const scope = JSON.stringify([
    identity.serverOrigin,
    identity.authContextVersion,
    profileId,
    pinGeneration,
  ]);
  if (user?.role !== "admin") return null;
  if (!profileId) return <p>Select your primary profile to configure an Xtream provider.</p>;
  if (capability.isLoading) return <p role="status">Checking Xtream support…</p>;
  if (capability.isError)
    return (
      <p role="alert">
        Live TV capabilities could not be verified. Reload before configuring a provider.
      </p>
    );
  if (!capability.data?.xtream_supported)
    return <p>This server does not advertise Xtream provider support.</p>;
  return (
    <ProviderForm
      key={scope}
      scope={scope}
      identity={identity}
      profileId={profileId}
      pinGeneration={pinGeneration}
    />
  );
}

function ProviderForm({
  scope,
  identity,
  profileId,
  pinGeneration,
}: {
  scope: string;
  identity: ReturnType<typeof captureSessionIdentity>;
  profileId: string;
  pinGeneration: number;
}) {
  const client = useQueryClient();
  const phase = useSyncExternalStore(
    subscribe,
    () => states.get(scope)?.phase ?? "initial",
    () => "initial" as Phase,
  );
  const [notice, setNotice] = useState("");
  const [loaded, setLoaded] = useState(false);
  const alive = useRef(true);
  const active = useRef<AbortController | null>(null);
  function current() {
    return (
      isSessionIdentityCurrent(identity) &&
      getProfileId() === profileId &&
      getProfileTokenGeneration() === pinGeneration
    );
  }
  async function request<T>(path: string, options: RequestInit): Promise<T> {
    if (!current()) throw new StaleApiRequestContextError();
    // A same-account token refresh is allowed, but never a new profile/PIN or
    // session authority. Native Live TV always requires a selected profile.
    const profile = captureProfileRequestContext();
    if (!profile) throw new StaleApiRequestContextError();
    return nativeApiWithProfileRequestContext<T>(path, profile, options, "none");
  }
  async function readback(controller: AbortController, expectedId?: string) {
    const data = await request<LiveTVTunersResponse>("/livetv/tuners", {
      signal: controller.signal,
    });
    if (!current() || controller.signal.aborted || !alive.current)
      throw new StaleApiRequestContextError();
    const tuners = data.tuners ?? [];
    if (expectedId && !tuners.some((tuner) => tuner.id === expectedId))
      throw new Error("Provider could not be confirmed");
    client.setQueryData(adminKeys.liveTVTuners(), tuners);
    return tuners;
  }
  async function reload() {
    if (!current() || states.get(scope)?.phase === "pending") return;
    const attempt = begin(scope, "reloading");
    const controller = new AbortController();
    active.current?.abort();
    active.current = controller;
    const timeout = setTimeout(() => controller.abort(), 75_000);
    try {
      await readback(controller);
      if (finish(scope, attempt, "ready") && alive.current) {
        setLoaded(true);
        setNotice("");
      }
    } catch {
      finish(scope, attempt, "reload-required");
    } finally {
      clearTimeout(timeout);
      if (active.current === controller) active.current = null;
    }
  }
  useEffect(() => {
    alive.current = true;
    const previous = states.get(scope)?.phase;
    // Reads can be restarted after a remount, but an uncertain write requires
    // an explicit reload and a pending write must never be admitted twice.
    if (previous !== "pending" && previous !== "reload-required") void reload();
    return () => {
      alive.current = false;
      active.current?.abort();
    };
    // The non-secret scope captures every authority boundary used above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scope]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    if (!current() || states.get(scope)?.phase !== "ready") {
      form.reset();
      return;
    }
    const fields = new FormData(form);
    const payload = {
      type: "xtream",
      name: String(fields.get("name") ?? "").trim(),
      url: String(fields.get("url") ?? "").trim(),
      username: String(fields.get("username") ?? ""),
      password: String(fields.get("password") ?? ""),
      max_connections: Number(fields.get("max_connections")),
    };
    form.reset();
    setNotice("");
    const attempt = begin(scope, "pending");
    const controller = new AbortController();
    active.current = controller;
    const timeout = setTimeout(() => controller.abort(), 75_000);
    try {
      const tuner = await request<LiveTVTuner>("/livetv/tuners/xtream", {
        method: "POST",
        body: JSON.stringify(payload),
        signal: controller.signal,
      });
      await readback(controller, tuner.id);
      if (finish(scope, attempt, "ready") && current() && alive.current) {
        setLoaded(true);
        setNotice("Provider added. Add its Xtream guide in the Guide tab.");
        void client.invalidateQueries({ queryKey: adminKeys.liveTVChannels() });
      }
    } catch {
      // Do not display/store raw provider or transport errors: they may carry
      // sensitive diagnostics. Reconcile, never retry this creation.
      finish(scope, attempt, "reload-required");
    } finally {
      payload.username = "";
      payload.password = "";
      fields.delete("username");
      fields.delete("password");
      clearTimeout(timeout);
      if (active.current === controller) active.current = null;
    }
  }

  return (
    <section className="max-w-xl space-y-4 border-t pt-6" aria-labelledby="xtream-provider-title">
      <h3 id="xtream-provider-title" className="text-sm font-medium">
        Xtream live TV provider
      </h3>
      <p className="text-muted-foreground text-sm">
        Import live channels from an Xtream account. VOD and series are not imported. The provider
        must serve MPEG-TS directly, without redirects. Credentials are encrypted on the server and
        cleared from this form on submission. Prefer HTTPS; HTTP sends them without TLS.
      </p>
      {(phase === "initial" || phase === "reloading") && <p role="status">Loading providers…</p>}
      {phase === "pending" && <p role="status">Adding provider and confirming its saved state…</p>}
      {phase === "reload-required" && (
        <div role="alert" className="space-y-3">
          <p>
            Provider state could not be confirmed. A previous request may have succeeded.
            Credentials were cleared; reload before making another change.
          </p>
          <Button variant="outline" onClick={() => void reload()}>
            Reload providers
          </Button>
        </div>
      )}
      {notice && <p role="status">{notice}</p>}
      {phase === "ready" && loaded && (
        <form className="space-y-4" autoComplete="off" onSubmit={(event) => void submit(event)}>
          <div className="space-y-1.5">
            <Label htmlFor="xtream-name">Provider name (optional)</Label>
            <Input id="xtream-name" name="name" maxLength={128} placeholder="My TV provider" />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="xtream-url">Provider base URL</Label>
            <Input
              id="xtream-url"
              name="url"
              type="url"
              required
              maxLength={2048}
              placeholder="https://provider.example:443"
              autoComplete="off"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="xtream-username">Provider username</Label>
            <Input
              id="xtream-username"
              name="username"
              required
              maxLength={512}
              autoComplete="off"
              spellCheck={false}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="xtream-password">Provider password</Label>
            <Input
              id="xtream-password"
              name="password"
              type="password"
              required
              maxLength={512}
              autoComplete="off"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="xtream-connections">Maximum provider connections</Label>
            <Input
              id="xtream-connections"
              name="max_connections"
              type="number"
              min={1}
              max={64}
              required
              defaultValue={1}
            />
            <p className="text-muted-foreground text-xs">
              Shared by watching and recording across Bloem replicas. Never exceeds the limit
              reported by your provider.
            </p>
          </div>
          <Button type="submit">Add Xtream provider</Button>
        </form>
      )}
    </section>
  );
}
