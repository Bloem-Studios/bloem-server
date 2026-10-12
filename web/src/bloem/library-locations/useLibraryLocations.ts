import { QueryClientContext } from "@tanstack/react-query";
import { adminKeys } from "@/hooks/queries/keys";
import { adminV2QueryKey } from "@/api/adminV2Client";
import { useContext, useEffect, useMemo, useRef, useState } from "react";
import { AdminV2ClientError, captureAdminRequestContext } from "@/api/adminV2Client";
import type { CreateLibraryRequest, Library } from "@/api/types";
import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";
import { locationApi } from "./api";
import {
  supportsSource,
  type LocationCapabilities,
  type MixedLibraryLocations,
  type RootSelection,
  type StorageSource,
} from "./types";
// Only match retained GET references by labels when the match is unambiguous.
// New drafts keep their opaque selection references as identity.
function identity(root: RootSelection) {
  return JSON.stringify([root.source_key, root.name, root.logical_path]);
}
export function useLibraryLocations(
  id: number | null,
  type: string,
  onPaths?: (paths: string[]) => void,
) {
  const queryClient = useContext(QueryClientContext);
  const initialType = useRef(type);
  const active = useOptionalAdminContext()?.active;
  const captured = active ? captureAdminRequestContext(active.key) : null;
  const key = captured?.key;
  const generation = captured?.generation;
  const scope = active?.scope;
  const context = useMemo(
    () => (key !== undefined && generation !== undefined ? { key, generation } : null),
    [key, generation],
  );
  const api = useMemo(
    () => (scope && context ? locationApi(scope, context) : null),
    [scope, context],
  );
  const [caps, setCaps] = useState<LocationCapabilities | null>(null);
  const [sources, setSources] = useState<StorageSource[]>([]);
  const [original, setOriginal] = useState<MixedLibraryLocations | null>(null);
  const [selections, setSelections] = useState<RootSelection[]>([]);
  const [error, setError] = useState("");
  const [uncertainCreate, setUncertainCreate] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [pending, setPending] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [reloadVersion, setReloadVersion] = useState(0);
  const epoch = useRef(0);
  const busy = useRef(false);
  const loadedContext = useRef<typeof context>(null);
  useEffect(() => {
    const request = ++epoch.current;
    const abort = new AbortController();
    setCaps(null);
    setSources([]);
    setOriginal(null);
    setSelections([]);
    setLoaded(false);
    setError("");
    setConflict(false);
    setPending(false);
    busy.current = false;
    loadedContext.current = null;
    if (api)
      void (async () => {
        try {
          const capabilities = await api.capabilities(abort.signal);
          if (request !== epoch.current || abort.signal.aborted) return;
          setCaps(capabilities);
          if (
            !capabilities.library_location_browsing ||
            !capabilities.library_location_atomic_editing
          ) {
            setLoaded(true);
            return;
          }
          // Source availability must not prevent reading/removing saved roots.
          const sourceLoad = api
            .sources(abort.signal)
            .then((value) => {
              if (request === epoch.current && !abort.signal.aborted) setSources(value);
            })
            .catch((e) => {
              if (request === epoch.current && !abort.signal.aborted) setError(e.message);
            });
          const value = id ? await api.read(id, abort.signal) : null;
          if (request !== epoch.current || abort.signal.aborted) return;
          setOriginal(value);
          if (value) onPaths?.(value.paths);
          setSelections(value?.selections ?? []);
          setLoaded(true);
          loadedContext.current = context;
          await sourceLoad;
        } catch (e) {
          if (request === epoch.current && !abort.signal.aborted) {
            setError(e instanceof Error ? e.message : "Could not load locations.");
            setLoaded(true);
          }
        }
      })();
    return () => {
      abort.abort();
      invalidate();
    };
  }, [api, context, id, reloadVersion, onPaths]);
  function invalidate() {
    epoch.current++;
  }
  const enabled =
    !!api && !!caps?.library_location_atomic_editing && !!caps.library_location_browsing;
  const ready =
    enabled &&
    loaded &&
    (!id || original !== null) &&
    !conflict &&
    !uncertainCreate &&
    loadedContext.current === context;
  function contextCurrent() {
    if (!context) return !active;
    const now = captureAdminRequestContext(context.key);
    return !!now && now.generation === context.generation;
  }
  function isCurrent() {
    const now = context && captureAdminRequestContext(context.key);
    return !!now && now.generation === context?.generation && loadedContext.current === context;
  }
  function shapeError(paths: string[]) {
    if (!caps?.library_location_mixing) {
      const keys = new Set([
        ...(original?.locations.map((l) => l.source_key) ?? []),
        ...selections.map((s) => s.source_key),
      ]);
      if (keys.size > 1 || (paths.some((p) => p.trim()) && keys.size > 0))
        return "This server supports filesystem folders or one storage source per library. Mixed locations are not ready.";
      if (id && original?.paths.length && selections.length)
        return "This library keeps its filesystem locations until mixed locations are ready.";
    }
    if (
      selections.some(
        (s) =>
          (type !== initialType.current || !original?.selections.includes(s)) &&
          !sources.some(
            (source) =>
              source.source_key === s.source_key && caps && supportsSource(caps, source, type),
          ),
      )
    )
      return "A selected source does not support this library type. Remove it or change the type.";
    return "";
  }
  async function save(
    body: CreateLibraryRequest,
  ): Promise<{ library?: Library; locations: MixedLibraryLocations } | null> {
    if (!ready || !api || !isCurrent() || busy.current) return null;
    busy.current = true;
    setPending(true);
    setError("");
    const request = epoch.current;
    let writeStarted = false;
    try {
      const invalid = shapeError(body.paths);
      if (invalid) throw new Error(invalid);
      let fresh: MixedLibraryLocations | null = null;
      if (id && original) {
        fresh = await api.read(id);
        if (
          fresh.revision !== original.revision ||
          JSON.stringify(fresh.paths) !== JSON.stringify(original.paths) ||
          JSON.stringify(fresh.selections.map(identity)) !==
            JSON.stringify(original.selections.map(identity))
        )
          throw new AdminV2ClientError(
            409,
            "revision_conflict",
            "Locations changed. Reload before saving.",
          );
      }
      const refreshed: RootSelection[] = [];
      for (const root of selections) {
        // Object provenance distinguishes retained roots from new opaque folders,
        // even when a provider gives both directories identical display labels.
        if (original?.selections.includes(root)) {
          const matches =
            fresh?.selections.filter((next) => identity(next) === identity(root)) ?? [];
          if (
            matches.length === 1 &&
            matches[0]!.configuration_revision !== root.configuration_revision
          )
            throw new AdminV2ClientError(
              409,
              "revision_conflict",
              "Source configuration changed. Reload locations before saving.",
            );
          if (matches.length !== 1 || !matches[0]!.selection_ref)
            throw new Error(
              "A retained folder is unavailable. Remove it or reload when its source is available.",
            );
          refreshed.push(matches[0]!);
        } else {
          const page = await api.browse(root.source_key, root.selection_ref);
          refreshed.push({ ...root, ...page.current });
        }
      }
      if (!isCurrent() || request !== epoch.current) return null;
      writeStarted = true;
      const value =
        id && original
          ? { locations: await api.replace(id, body.paths, refreshed, original) }
          : await api.create(body, refreshed);
      if (!isCurrent() || request !== epoch.current) return null;
      setOriginal(value.locations);
      onPaths?.(value.locations.paths);
      setSelections(value.locations.selections);
      void queryClient?.invalidateQueries({ queryKey: adminKeys.libraries() });
      if (context) void queryClient?.invalidateQueries({ queryKey: adminV2QueryKey(context.key) });
      return value;
    } catch (e) {
      if (isCurrent() && request === epoch.current) {
        setError(e instanceof Error ? e.message : "Save failed.");
        if (
          !id &&
          writeStarted &&
          (!(e instanceof AdminV2ClientError) || e.code === "mutation_outcome_unknown")
        ) {
          setUncertainCreate(true);
        }
        if (
          e instanceof AdminV2ClientError &&
          (e.status === 409 || e.code === "mutation_outcome_unknown")
        )
          setConflict(true);
      }
      return null;
    } finally {
      if (request === epoch.current) {
        busy.current = false;
        setPending(false);
      }
    }
  }
  return {
    enabled,
    ready,
    pending,
    error: uncertainCreate
      ? "Creation outcome is unknown. Close this form and check the library list before creating another library."
      : error,
    uncertainCreate,
    conflict,
    caps,
    sources,
    selections,
    original,
    api,
    context,
    isCurrent,
    contextCurrent,
    save,
    shapeError,
    loading: !!active && !loaded,
    blocked: !!active && loaded && !caps,
    reset: () => {
      setOriginal(null);
      setSelections([]);
    },
    reload: async () => {
      if (uncertainCreate || busy.current) return;
      setReloadVersion((v) => v + 1);
    },
    removeSelection: (index: number) =>
      setSelections((current) => current.filter((_, i) => i !== index)),
    addSelections: (roots: RootSelection[]) =>
      setSelections((current) => [
        ...current,
        ...roots.filter(
          (root) =>
            !current.some(
              (old) =>
                old.source_key === root.source_key && old.selection_ref === root.selection_ref,
            ),
        ),
      ]),
  };
}
export type LibraryLocationsController = ReturnType<typeof useLibraryLocations>;
