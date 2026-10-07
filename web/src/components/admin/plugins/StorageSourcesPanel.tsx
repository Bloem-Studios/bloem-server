import { useRef, useState } from "react";
import type { FormEvent } from "react";
import { ArrowUpCircle, HardDrive, Plus, Upload, X } from "lucide-react";

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
  useInstallStorageSource,
  useStorageArtifacts,
  useStorageSources,
  useUpgradeStorageSource,
} from "@/hooks/queries/admin/storageSources";

const PANEL = "rounded-xl border bg-card px-4 py-4";

function artifactLabel(artifact: StorageArtifact): string {
  return `${artifact.plugin_id} ${artifact.version} (${artifact.os}/${artifact.arch})`;
}

function FileField({
  label,
  file,
  onChange,
  disabled,
}: {
  label: string;
  file: File | null;
  onChange: (file: File | null) => void;
  disabled?: boolean;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  return (
    <label className="border-border hover:border-foreground/20 focus-within:ring-ring flex h-9 min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-lg border border-dashed px-3.5 text-sm transition-colors focus-within:ring-2">
      <Upload className="text-muted-foreground h-4 w-4 shrink-0" aria-hidden />
      <span className="text-muted-foreground truncate">{file ? file.name : `${label}...`}</span>
      <input
        ref={inputRef}
        type="file"
        aria-label={label}
        className="sr-only"
        disabled={disabled}
        onChange={(e) => onChange(e.target.files?.[0] ?? null)}
      />
    </label>
  );
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
        <SelectValue placeholder="Choose an approved release" />
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

/** Upgrades one source's plugin to another approved release of the same plugin. */
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
  const [binary, setBinary] = useState<File | null>(null);

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    if (!artifactKey || !binary || upgrade.isPending) return;
    upgrade.mutate({ source, artifactKey, binary }, { onSuccess: onDone });
  }

  if (releases.length === 0) {
    return (
      <p className="text-muted-foreground mt-2 text-[13px]">
        No approved releases of {source.plugin_id} are available.
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
      <FileField
        label="Plugin executable"
        file={binary}
        onChange={setBinary}
        disabled={upgrade.isPending}
      />
      <Button type="submit" size="sm" disabled={!artifactKey || !binary || upgrade.isPending}>
        {upgrade.isPending ? "Upgrading..." : "Upgrade"}
      </Button>
    </form>
  );
}

function parseConfig(text: string): Record<string, Record<string, unknown>> | null {
  try {
    const value: unknown = JSON.parse(text.trim() || "{}");
    if (!value || typeof value !== "object" || Array.isArray(value)) return null;
    for (const entry of Object.values(value)) {
      if (!entry || typeof entry !== "object" || Array.isArray(entry)) return null;
    }
    return value as Record<string, Record<string, unknown>>;
  } catch {
    return null;
  }
}

/** Installs an approved storage plugin release as a new storage source. */
function InstallForm({ artifacts, onDone }: { artifacts: StorageArtifact[]; onDone: () => void }) {
  const install = useInstallStorageSource();
  const [artifactKey, setArtifactKey] = useState("");
  const [providerSourceId, setProviderSourceId] = useState("");
  const [rootEntryId, setRootEntryId] = useState("");
  const [configText, setConfigText] = useState("");
  const [binary, setBinary] = useState<File | null>(null);
  const config = parseConfig(configText);
  const ready =
    artifactKey && providerSourceId.trim() && rootEntryId.trim() && config !== null && binary;

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    if (!ready || !config || !binary || install.isPending) return;
    install.mutate(
      {
        artifactKey,
        providerSourceId: providerSourceId.trim(),
        rootEntryId: rootEntryId.trim(),
        config,
        binary,
      },
      { onSuccess: onDone },
    );
  }

  return (
    <form onSubmit={handleSubmit} className="mt-3 space-y-2">
      <ArtifactSelect
        artifacts={artifacts}
        value={artifactKey}
        onChange={setArtifactKey}
        label="Storage plugin release"
      />
      <div className="flex flex-col gap-2 sm:flex-row">
        <Input
          value={providerSourceId}
          onChange={(e) => setProviderSourceId(e.target.value)}
          placeholder="Provider source ID"
          aria-label="Provider source ID"
        />
        <Input
          value={rootEntryId}
          onChange={(e) => setRootEntryId(e.target.value)}
          placeholder="Root entry ID"
          aria-label="Root entry ID"
        />
      </div>
      <textarea
        value={configText}
        onChange={(e) => setConfigText(e.target.value)}
        placeholder='Provider configuration as JSON, for example {"source": {"url": "..."}}'
        aria-label="Provider configuration"
        aria-invalid={config === null ? true : undefined}
        spellCheck={false}
        autoComplete="off"
        rows={4}
        className="border-input bg-background focus-visible:ring-ring w-full rounded-lg border px-3 py-2 font-mono text-[13px] focus-visible:ring-2 focus-visible:outline-none"
      />
      {config === null ? (
        <p className="text-destructive text-xs">Configuration must be a JSON object of objects.</p>
      ) : (
        <p className="text-muted-foreground text-xs">Stored encrypted and never shown again.</p>
      )}
      <div className="flex flex-col gap-2 sm:flex-row">
        <FileField
          label="Plugin executable"
          file={binary}
          onChange={setBinary}
          disabled={install.isPending}
        />
        <Button type="submit" size="sm" disabled={!ready || install.isPending}>
          {install.isPending ? "Installing..." : "Install"}
        </Button>
      </div>
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
  const [installing, setInstalling] = useState(false);
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
        <Button
          variant="outline"
          size="sm"
          onClick={() => setInstalling(!installing)}
          disabled={approved.length === 0}
        >
          {installing ? <X className="h-3.5 w-3.5" /> : <Plus className="h-3.5 w-3.5" />}
          {installing ? "Cancel" : "Add source"}
        </Button>
      </div>

      {sources.isError || artifacts.isError ? (
        <p role="alert" className="text-destructive mt-3 text-sm">
          Failed to load storage sources.
        </p>
      ) : null}
      {installing && <InstallForm artifacts={approved} onDone={() => setInstalling(false)} />}

      {sources.data && sources.data.length > 0 ? (
        <ul className="mt-2 divide-y">
          {sources.data.map((source) => (
            <li key={source.source_key} className="py-2.5 text-[13.5px]">
              <div className="flex min-w-0 items-center gap-2.5">
                <HardDrive className="text-muted-foreground h-4 w-4 shrink-0" aria-hidden />
                <span className="min-w-0 flex-1 truncate">
                  <span className="font-medium">{source.provider_source_id}</span>
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
