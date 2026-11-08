import { useState } from "react";
import { Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  type StorageArtifact,
  type StorageSource,
  useStorageArtifacts,
  useStorageSources,
} from "@/hooks/queries/admin/storageSources";

import { AddStorageSourceDialog } from "./AddStorageSourceDialog";
import { StorageSourceRow } from "./StorageSourceRow";
import { type SourceStatus, sourceStatus } from "./storageStatus";

const PANEL = "rounded-xl border bg-card px-4 py-4";

/** The release describing a source's plugin: the newest catalog release, else the newest upload. */
function releaseFor(
  source: StorageSource,
  artifacts: StorageArtifact[],
): StorageArtifact | undefined {
  return artifacts
    .filter((a) => a.plugin_id === source.plugin_id)
    .sort(
      (a, b) =>
        Number(a.catalog) - Number(b.catalog) ||
        a.version.localeCompare(b.version, undefined, { numeric: true }),
    )
    .at(-1);
}

const STATUS_ORDER: Record<SourceStatus, number> = { live: 0, setup: 1, off: 2, removed: 3 };

/** Live sources first, then ones needing setup, then ones that are off. */
function byStatus(a: StorageSource, b: StorageSource): number {
  return STATUS_ORDER[sourceStatus(a)] - STATUS_ORDER[sourceStatus(b)];
}

/**
 * Storage plugins: sources a library can read its media from instead of
 * folders. A source is used by choosing it when creating a library.
 */
export function StorageSourcesPanel() {
  const sources = useStorageSources(true);
  const artifacts = useStorageArtifacts(true);
  const [adding, setAdding] = useState(false);
  const approved = artifacts.data ?? [];
  const all = sources.data ?? [];
  const current = all.filter((source) => sourceStatus(source) !== "removed").sort(byStatus);
  const removed = all.filter((source) => sourceStatus(source) === "removed");
  const live = current.filter((source) => sourceStatus(source) === "live").length;

  return (
    <section className={PANEL} aria-labelledby="storage-sources-heading">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 id="storage-sources-heading" className="text-[15px] font-semibold">
            Storage sources
          </h2>
          <p className="text-muted-foreground text-[13px]">
            Where a library can read its books, movies or audiobooks from instead of folders. Choose
            a source when you create the library.
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
          <Plus className="h-3.5 w-3.5" />
          Add source
        </Button>
      </div>

      {sources.isError || artifacts.isError ? (
        <p role="alert" className="text-destructive mt-3 text-sm">
          Failed to load storage sources.
        </p>
      ) : null}
      <AddStorageSourceDialog
        artifacts={artifacts.data}
        sources={sources.data ?? []}
        open={adding}
        onOpenChange={setAdding}
        error={artifacts.isError ? artifacts.error : undefined}
        onRetry={() => void artifacts.refetch()}
        isRetrying={artifacts.isFetching}
      />

      {sources.isSuccess && all.length > 0 ? (
        <p className="text-muted-foreground mt-3 text-[13px]">
          {live === 1 ? "1 source is live." : `${live} sources are live.`}
        </p>
      ) : null}

      {current.length > 0 ? (
        <ul className="mt-1 divide-y">
          {current.map((source) => (
            <StorageSourceRow
              key={source.source_key}
              source={source}
              artifact={releaseFor(source, approved)}
              artifacts={approved}
            />
          ))}
        </ul>
      ) : sources.isSuccess && removed.length === 0 ? (
        <p className="text-muted-foreground mt-3 text-[13px]">No storage sources yet.</p>
      ) : null}

      {removed.length > 0 ? (
        <details className="mt-3 border-t pt-2">
          <summary className="text-muted-foreground cursor-pointer text-[13px]">
            Removed sources ({removed.length})
          </summary>
          <ul className="divide-y">
            {removed.map((source) => (
              <StorageSourceRow
                key={source.source_key}
                source={source}
                artifact={releaseFor(source, approved)}
                artifacts={approved}
              />
            ))}
          </ul>
        </details>
      ) : null}
    </section>
  );
}
