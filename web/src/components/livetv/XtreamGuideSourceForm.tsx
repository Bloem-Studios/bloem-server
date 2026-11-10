import { useEffect, useRef, useState, useSyncExternalStore, type FormEvent } from "react";
import {
  captureSessionIdentity,
  getProfileTokenGeneration,
  isSessionIdentityCurrent,
  StaleApiRequestContextError,
} from "@/api/client";
import { getProfileId } from "@/api/bloemClient";
import { useBloemT } from "@/bloem/i18n";
import { useAuth } from "@/hooks/useAuth";
import {
  useCreateLiveTVGuideSource,
  useLiveTVGuideSources,
  useLiveTVTuners,
} from "@/hooks/queries/useLiveTV";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";

type Phase = "ready" | "pending" | "reload-required" | "reloading";
// Non-secret admission state survives remounts. A background query refresh
// cannot acknowledge an uncertain creation; the user must explicitly reload.
const phases = new Map<string, Phase>();
const listeners = new Set<() => void>();
function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
function setPhase(key: string, phase: Phase) {
  phases.set(key, phase);
  for (const [oldKey, oldPhase] of phases) {
    if (phases.size <= 64) break;
    if (oldKey !== key && oldPhase === "ready") phases.delete(oldKey);
  }
  listeners.forEach((listener) => listener());
}

export function XtreamGuideSourceForm() {
  const bt = useBloemT();
  const { user } = useAuth();
  const identity = captureSessionIdentity();
  const profileId = getProfileId();
  const pinGeneration = getProfileTokenGeneration();
  if (user?.role !== "admin") return null;
  if (!profileId) return <p>{bt("bloem.admin.liveTv.xtreamGuide.switchProfile")}</p>;
  const scope = JSON.stringify([
    identity.serverOrigin,
    identity.authContextVersion,
    profileId,
    pinGeneration,
  ]);
  return (
    <GuideForm
      key={scope}
      scope={scope}
      identity={identity}
      profileId={profileId}
      pinGeneration={pinGeneration}
    />
  );
}

function GuideForm({
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
  const tuners = useLiveTVTuners();
  const sources = useLiveTVGuideSources();
  const create = useCreateLiveTVGuideSource();
  const [selected, setSelected] = useState("");
  const phase = useSyncExternalStore(subscribe, () => phases.get(scope) ?? "ready");
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  function current() {
    return (
      isSessionIdentityCurrent(identity) &&
      getProfileId() === profileId &&
      getProfileTokenGeneration() === pinGeneration
    );
  }
  async function reconcile(expectedId?: string) {
    if (!current()) throw new StaleApiRequestContextError();
    const result = await sources.refetch({ throwOnError: true });
    if (!current()) throw new StaleApiRequestContextError();
    if (!result.data || (expectedId && !result.data.some((source) => source.id === expectedId)))
      throw new Error("Guide source could not be confirmed");
  }
  async function reload() {
    if (!current() || phases.get(scope) !== "reload-required") return;
    setPhase(scope, "reloading");
    try {
      await reconcile();
      setPhase(scope, "ready");
    } catch {
      setPhase(scope, "reload-required");
    }
  }
  const configured = new Set(
    (sources.data ?? [])
      .filter((source) => source.type === "xtream")
      .map((source) => source.config.tuner_id),
  );
  const available = (tuners.data ?? []).filter(
    (tuner) => tuner.type === "xtream" && !configured.has(tuner.id),
  );
  const limitReached = (sources.data ?? []).filter((source) => source.enabled).length >= 3;
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const tuner = available.find((entry) => entry.id === selected);
    if (
      !current() ||
      (phases.get(scope) ?? "ready") !== "ready" ||
      !tuner ||
      create.isPending ||
      limitReached ||
      tuners.isLoading ||
      sources.isLoading ||
      tuners.isError ||
      sources.isError
    )
      return;
    setPhase(scope, "pending");
    setSelected("");
    try {
      const saved = await create.mutateAsync({
        type: "xtream",
        enabled: true,
        priority: 100,
        display_name: `${tuner.model || "Xtream"} guide`,
        config: { tuner_id: tuner.id },
      });
      await reconcile(saved.id);
      setPhase(scope, "ready");
    } catch {
      setPhase(scope, "reload-required");
    } finally {
      if (alive.current && current()) setSelected("");
    }
  }
  return (
    <section className="max-w-xl space-y-4" aria-labelledby="xtream-guide-title">
      <h3 id="xtream-guide-title" className="sr-only">
        {bt("bloem.admin.liveTv.xtreamGuide.heading")}
      </h3>
      <p className="text-muted-foreground text-sm">{bt("bloem.admin.liveTv.xtreamGuide.intro")}</p>
      {phase === "reload-required" && (
        <div role="alert" className="space-y-3">
          <p>{bt("bloem.admin.liveTv.xtreamGuide.outcomeUnknown")}</p>
          <Button variant="outline" onClick={() => void reload()}>
            {bt("bloem.admin.liveTv.xtreamGuide.reload")}
          </Button>
        </div>
      )}
      {phase === "reloading" && (
        <p role="status">{bt("bloem.admin.liveTv.xtreamGuide.reloading")}</p>
      )}
      {tuners.isError || sources.isError ? (
        <p role="alert">{bt("bloem.admin.liveTv.xtreamGuide.loadFailed")}</p>
      ) : (
        <form onSubmit={(event) => void submit(event)} className="space-y-3">
          <Label htmlFor="xtream-guide-provider">
            {bt("bloem.admin.liveTv.xtreamGuide.provider")}
          </Label>
          <select
            id="xtream-guide-provider"
            className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
            value={selected}
            onChange={(event) => setSelected(event.target.value)}
            disabled={
              phase !== "ready" || create.isPending || tuners.isLoading || sources.isLoading
            }
            required
          >
            <option value="">{bt("bloem.admin.liveTv.xtreamGuide.chooseProvider")}</option>
            {available.map((tuner) => (
              <option key={tuner.id} value={tuner.id}>
                {tuner.model
                  ? bt("bloem.admin.liveTv.xtreamGuide.providerOption", {
                      name: tuner.model,
                      address: tuner.base_url,
                    })
                  : bt("bloem.admin.liveTv.xtreamGuide.providerOptionUnnamed", {
                      address: tuner.base_url,
                    })}
              </option>
            ))}
          </select>
          {available.length === 0 && !tuners.isLoading && (
            <p className="text-muted-foreground text-xs">
              {bt("bloem.admin.liveTv.xtreamGuide.noneAvailable")}
            </p>
          )}
          <Button
            type="submit"
            disabled={phase !== "ready" || create.isPending || !selected || limitReached}
          >
            {phase === "pending" || create.isPending
              ? bt("bloem.admin.liveTv.xtreamGuide.adding")
              : bt("bloem.admin.liveTv.xtreamGuide.submit")}
          </Button>
        </form>
      )}
    </section>
  );
}
