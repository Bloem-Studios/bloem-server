import { useState } from "react";
import type { FormEvent } from "react";
import { ArrowUpCircle, HardDrive, Plus } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  type StorageArtifact,
  type StorageSource,
  useConfigureStorageSource,
  useStorageArtifacts,
  useStorageSources,
  useUpgradeStorageSource,
} from "@/hooks/queries/admin/storageSources";

import { AddStorageSourceDialog } from "./AddStorageSourceDialog";

const PANEL = "rounded-xl border bg-card px-4 py-4";

function artifactLabel(artifact: StorageArtifact): string {
  return `${artifact.plugin_id} ${artifact.version} (${artifact.os}/${artifact.arch})`;
}

function ArtifactSelect({
  artifacts,
  value,
  onChange,
  label,
}: {
  artifacts: StorageArtifact[];
  value: string;
  onChange: (value: string) => void;
  label: string;
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger aria-label={label} className="sm:flex-1">
        <SelectValue placeholder="Choose a release" />
      </SelectTrigger>
      <SelectContent>
        {artifacts.map((artifact) => (
          <SelectItem key={artifact.artifact_key} value={artifact.artifact_key}>
            {artifactLabel(artifact)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

/** Upgrades one source's plugin to another catalog release of the same plugin. */
function UpgradeForm({
  source,
  artifacts,
  onDone,
}: {
  source: StorageSource;
  artifacts: StorageArtifact[];
  onDone: () => void;
}) {
  const upgrade = useUpgradeStorageSource();
  const releases = artifacts.filter((a) => a.plugin_id === source.plugin_id);
  const [artifactKey, setArtifactKey] = useState("");

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    if (!artifactKey || upgrade.isPending) return;
    upgrade.mutate({ source, artifactKey }, { onSuccess: onDone });
  }

  if (releases.length === 0) {
    return (
      <p className="text-muted-foreground mt-2 text-[13px]">
        No catalog releases of {source.plugin_id} are available.
      </p>
    );
  }
  return (
    <form onSubmit={handleSubmit} className="mt-2 flex flex-col gap-2 sm:flex-row">
      <ArtifactSelect
        artifacts={releases}
        value={artifactKey}
        onChange={setArtifactKey}
        label="Release to upgrade to"
      />
      <Button type="submit" size="sm" disabled={!artifactKey || upgrade.isPending}>
        {upgrade.isPending ? "Upgrading..." : "Upgrade"}
      </Button>
    </form>
  );
}

function ConnectionForm({ source }: { source: StorageSource }) {
  const configure = useConfigureStorageSource();
  const [url, setURL] = useState("");
  const [apiKey, setAPIKey] = useState("");
  function submit(event: FormEvent) {
    event.preventDefault();
    if (!url.trim() || !apiKey || configure.isPending) return;
    configure.mutate({ source, url: url.trim(), apiKey }, { onSuccess: () => setAPIKey("") });
  }
  return (
    <form onSubmit={submit} className="mt-3 space-y-3">
      <label className="block text-sm">
        Bookwarehouse server URL
        <Input
          type="url"
          required
          placeholder="https://books.example"
          value={url}
          onChange={(e) => setURL(e.target.value)}
          autoComplete="off"
        />
      </label>
      <label className="block text-sm">
        API key
        <Input
          type="password"
          required
          value={apiKey}
          onChange={(e) => setAPIKey(e.target.value)}
          autoComplete="new-password"
        />
      </label>
      <Button type="submit" disabled={configure.isPending || !url.trim() || !apiKey}>
        {configure.isPending ? "Connecting..." : "Save connection"}
      </Button>
    </form>
  );
}

/**
 * Storage plugins: sources an ebook library can read its books from instead
 * of folders. A source is used by choosing it when creating a library.
 */
export function StorageSourcesPanel() {
  const sources = useStorageSources(true);
  const artifacts = useStorageArtifacts(true);
  const [adding, setAdding] = useState(false);
  const [upgrading, setUpgrading] = useState<string | null>(null);
  const approved = artifacts.data ?? [];

  return (
    <section className={PANEL} aria-labelledby="storage-sources-heading">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 id="storage-sources-heading" className="text-[15px] font-semibold">
            Storage sources
          </h2>
          <p className="text-muted-foreground text-[13px]">
            Where an ebook library can read its books from instead of folders. Choose a source when
            you create the library.
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
      />

      {sources.data && sources.data.length > 0 ? (
        <ul className="mt-2 divide-y">
          {sources.data.map((source) => (
            <li key={source.source_key} className="py-2.5 text-[13.5px]">
              <div className="flex min-w-0 items-center gap-2.5">
                <HardDrive className="text-muted-foreground h-4 w-4 shrink-0" aria-hidden />
                <span className="min-w-0 flex-1 truncate">
                  <span className="font-medium">
                    {source.plugin_id === "bloem.storage.bookwarehouse"
                      ? "Bookwarehouse"
                      : source.provider_source_id}
                  </span>
                  <span className="text-muted-foreground"> · {source.plugin_id}</span>
                </span>
                <Badge variant={source.state === "attached" ? "secondary" : "outline"}>
                  {source.state}
                </Badge>
                {source.state === "attached" && (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      setUpgrading(upgrading === source.source_key ? null : source.source_key)
                    }
                  >
                    <ArrowUpCircle className="h-3.5 w-3.5" />
                    {upgrading === source.source_key ? "Cancel" : "Upgrade"}
                  </Button>
                )}
              </div>
              {!source.configured && source.plugin_id === "bloem.storage.bookwarehouse" && (
                <ConnectionForm source={source} />
              )}
              {upgrading === source.source_key && (
                <UpgradeForm
                  source={source}
                  artifacts={approved}
                  onDone={() => setUpgrading(null)}
                />
              )}
            </li>
          ))}
        </ul>
      ) : sources.isSuccess ? (
        <p className="text-muted-foreground mt-3 text-[13px]">No storage sources yet.</p>
      ) : null}
    </section>
  );
}
