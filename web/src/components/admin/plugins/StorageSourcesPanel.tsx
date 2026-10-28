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

/**
 * Storage plugins: sources a library can read its media from instead of
 * folders. A source is used by choosing it when creating a library.
 */
export function StorageSourcesPanel() {
  const sources = useStorageSources(true);
  const artifacts = useStorageArtifacts(true);
  const [adding, setAdding] = useState(false);
  const approved = artifacts.data ?? [];

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

      {sources.data && sources.data.length > 0 ? (
        <ul className="mt-2 divide-y">
          {sources.data.map((source) => (
            <StorageSourceRow
              key={source.source_key}
              source={source}
              artifact={releaseFor(source, approved)}
              artifacts={approved}
            />
          ))}
        </ul>
      ) : sources.isSuccess ? (
        <p className="text-muted-foreground mt-3 text-[13px]">No storage sources yet.</p>
      ) : null}
    </section>
  );
}
