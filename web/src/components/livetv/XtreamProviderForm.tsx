import { useEffect, useRef, useState, useSyncExternalStore, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  captureProfileRequestContext,
  captureSessionIdentity,
  getProfileTokenGeneration,
  isSessionIdentityCurrent,
  StaleApiRequestContextError,
} from "@/api/client";
import { getProfileId, nativeApiWithProfileRequestContext } from "@/api/bloemClient";
import type { LiveTVTuner, LiveTVTunersResponse } from "@/api/bloemTypes";
import { useBloemT } from "@/bloem/i18n";
import { useAuth } from "@/hooks/useAuth";
import { liveTVKeys } from "@/hooks/queries/bloemKeys";
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
  const bt = useBloemT();
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
  if (!profileId) return <p>{bt("bloem.admin.liveTv.xtreamProvider.switchProfile")}</p>;
  if (capability.isLoading)
    return <p role="status">{bt("bloem.admin.liveTv.xtreamProvider.checkingSupport")}</p>;
  if (capability.isError)
    return <p role="alert">{bt("bloem.admin.liveTv.xtreamProvider.supportCheckFailed")}</p>;
  if (!capability.data?.xtream_supported)
    return <p>{bt("bloem.admin.liveTv.xtreamProvider.unsupported")}</p>;
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
  const bt = useBloemT();
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
    client.setQueryData(liveTVKeys.liveTVTuners(), tuners);
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
    // eslint-disable-next-line react-hooks/exhaustive-deps -- the non-secret scope captures every authority boundary used above
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
        setNotice(bt("bloem.admin.liveTv.xtreamProvider.added"));
        void client.invalidateQueries({ queryKey: liveTVKeys.liveTVChannels() });
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
      <h3 id="xtream-provider-title" className="text-base font-medium">
        {bt("bloem.admin.liveTv.xtreamProvider.heading")}
      </h3>
      <p className="text-muted-foreground text-sm">
        {bt("bloem.admin.liveTv.xtreamProvider.intro")}
      </p>
      <details className="text-sm">
        <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
          {bt("bloem.admin.liveTv.xtreamProvider.requirements")}
        </summary>
        <p className="text-muted-foreground mt-2">
          {bt("bloem.admin.liveTv.xtreamProvider.requirementsDetail")}
        </p>
      </details>
      {(phase === "initial" || phase === "reloading") && (
        <p role="status">{bt("bloem.admin.liveTv.xtreamProvider.loading")}</p>
      )}
      {phase === "pending" && <p role="status">{bt("bloem.admin.liveTv.xtreamProvider.adding")}</p>}
      {phase === "reload-required" && (
        <div role="alert" className="space-y-3">
          <p>{bt("bloem.admin.liveTv.xtreamProvider.outcomeUnknown")}</p>
          <Button variant="outline" onClick={() => void reload()}>
            {bt("bloem.admin.liveTv.xtreamProvider.reload")}
          </Button>
        </div>
      )}
      {notice && <p role="status">{notice}</p>}
      {phase === "ready" && loaded && (
        <form className="space-y-4" autoComplete="off" onSubmit={(event) => void submit(event)}>
          <div className="space-y-1.5">
            <Label htmlFor="xtream-name">{bt("bloem.admin.liveTv.xtreamProvider.name")}</Label>
            <Input
              id="xtream-name"
              name="name"
              maxLength={128}
              placeholder={bt("bloem.admin.liveTv.xtreamProvider.namePlaceholder")}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="xtream-url">
              {bt("bloem.admin.liveTv.xtreamProvider.serverAddress")}
            </Label>
            <Input
              id="xtream-url"
              name="url"
              type="url"
              required
              maxLength={2048}
              placeholder={bt("bloem.admin.liveTv.xtreamProvider.serverAddressPlaceholder")}
              autoComplete="off"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="xtream-username">
              {bt("bloem.admin.liveTv.xtreamProvider.username")}
            </Label>
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
            <Label htmlFor="xtream-password">
              {bt("bloem.admin.liveTv.xtreamProvider.password")}
            </Label>
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
            <Label htmlFor="xtream-connections">
              {bt("bloem.admin.liveTv.xtreamProvider.streamsAtOnce")}
            </Label>
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
              {bt("bloem.admin.liveTv.xtreamProvider.streamsAtOnceHelp")}
            </p>
          </div>
          <Button type="submit">{bt("bloem.admin.liveTv.xtreamProvider.submit")}</Button>
        </form>
      )}
    </section>
  );
}
