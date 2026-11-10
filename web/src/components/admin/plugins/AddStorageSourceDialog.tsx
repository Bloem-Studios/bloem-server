import { useBloemT } from "@/bloem/i18n";
import { type FormEvent, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  type StorageArtifact,
  type StorageSource,
  useInstallStorageSource,
} from "@/hooks/queries/admin/storageSources";
import { randomUUID } from "@/lib/uuid";

import { S3AddForm } from "./S3AddForm";

const S3_PLUGIN = "bloem.storage.s3";
const BOOKWAREHOUSE_PLUGIN = "bloem.storage.bookwarehouse";

function byVersion(a: StorageArtifact, b: StorageArtifact): number {
  return a.version.localeCompare(b.version, undefined, { numeric: true });
}

/** The newest catalog release of one plugin, if the catalog carries it. */
function latestRelease(
  artifacts: StorageArtifact[],
  pluginId: string,
): StorageArtifact | undefined {
  return artifacts
    .filter((a) => a.catalog && a.plugin_id === pluginId)
    .sort(byVersion)
    .at(-1);
}

function PluginRow({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col items-start justify-between gap-3 rounded-lg border p-3 sm:flex-row sm:items-center">
      <div>
        <p className="font-medium">{title}</p>
        <p className="text-muted-foreground text-sm">{description}</p>
      </div>
      {children}
    </div>
  );
}

function BookwarehouseRow({ artifact, onDone }: { artifact: StorageArtifact; onDone: () => void }) {
  const bt = useBloemT();
  const install = useInstallStorageSource();
  return (
    <PluginRow
      title={bt("bloem.admin.storage.addDialog.bookwarehouse")}
      description={bt("bloem.admin.storage.addDialog.epubAndPdfBooks", {
        version: artifact.version,
      })}
    >
      <Button
        disabled={install.isPending}
        onClick={() =>
          install.mutate(
            {
              artifactKey: artifact.artifact_key,
              providerSourceId: `bookwarehouse_${randomUUID().replaceAll("-", "")}`,
              rootEntryId: "root",
              config: {},
            },
            { onSuccess: onDone },
          )
        }
      >
        {install.isPending
          ? bt("bloem.admin.storage.addDialog.installing")
          : bt("bloem.admin.storage.addDialog.installBookwarehouse")}
      </Button>
    </PluginRow>
  );
}

/**
 * A Bookwarehouse audiobook source reads its feed from the moment it is
 * installed: the plugin only reports the audiobook root once configured, so
 * the connection goes with the installation rather than after it.
 */
function BookwarehouseAudioForm({
  artifact,
  onBack,
  onDone,
}: {
  artifact: StorageArtifact;
  onBack: () => void;
  onDone: () => void;
}) {
  const bt = useBloemT();
  const install = useInstallStorageSource();
  const [url, setURL] = useState("");
  const [apiKey, setAPIKey] = useState("");
  const ready = url.trim() !== "" && apiKey !== "" && !install.isPending;
  function submit(event: FormEvent) {
    event.preventDefault();
    if (!ready) return;
    const sourceID = `bookwarehouse_audio_${randomUUID().replaceAll("-", "")}`;
    install.mutate(
      {
        artifactKey: artifact.artifact_key,
        providerSourceId: sourceID,
        rootEntryId: "audio-root",
        config: {
          connection: {
            base_url: url.trim(),
            api_key: apiKey,
            content: "audiobooks",
            source_id: sourceID,
            source_name: "Bookwarehouse audiobooks",
          },
        },
      },
      { onSuccess: onDone },
    );
  }
  return (
    <form onSubmit={submit} className="space-y-3">
      <label className="block text-sm">
        {bt("bloem.admin.storage.addDialog.bookwarehouseServerUrl")}
        <Input
          type="url"
          required
          placeholder={bt("bloem.admin.storage.addDialog.httpsBooksExample")}
          value={url}
          onChange={(e) => setURL(e.target.value)}
          autoComplete="off"
        />
      </label>
      <label className="block text-sm">
        {bt("bloem.admin.storage.addDialog.apiKey")}
        <Input
          type="password"
          required
          value={apiKey}
          onChange={(e) => setAPIKey(e.target.value)}
          autoComplete="new-password"
        />
      </label>
      <div className="flex justify-between gap-2">
        <Button type="button" variant="ghost" onClick={onBack} disabled={install.isPending}>
          {bt("bloem.admin.storage.addDialog.back")}
        </Button>
        <Button type="submit" disabled={!ready}>
          {install.isPending
            ? bt("bloem.admin.storage.addDialog.installing")
            : bt("bloem.admin.storage.addDialog.installAudiobooks")}
        </Button>
      </div>
    </form>
  );
}

function DialogBody({
  artifacts,
  sources,
  error,
  onRetry,
  isRetrying = false,
  onClose,
  onBusyChange,
}: {
  artifacts?: StorageArtifact[];
  sources: StorageSource[];
  error?: unknown;
  onRetry?: () => void;
  isRetrying?: boolean;
  onClose: () => void;
  onBusyChange: (busy: boolean) => void;
}) {
  const bt = useBloemT();
  const [choice, setChoice] = useState<"s3" | "bookwarehouse-audio" | null>(null);

  if (!artifacts) {
    if (error) {
      return (
        <div role="alert" className="space-y-3 text-sm">
          <p className="text-destructive">
            {bt("bloem.admin.storage.addDialog.theListOfStorageTypes")}
          </p>
          {onRetry ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={isRetrying}
              onClick={onRetry}
            >
              {isRetrying
                ? bt("bloem.admin.storage.addDialog.retrying")
                : bt("bloem.admin.storage.addDialog.retry")}
            </Button>
          ) : null}
        </div>
      );
    }
    return (
      <p className="text-muted-foreground text-sm">
        {bt("bloem.admin.storage.addDialog.loadingStorageTypes")}
      </p>
    );
  }

  // Offered in every administrative scope: an organization administrator adds
  // sources owned by their own organization.
  const s3 = latestRelease(artifacts, S3_PLUGIN);
  const bookwarehouse = latestRelease(artifacts, BOOKWAREHOUSE_PLUGIN);

  if (choice === "s3" && s3) {
    return (
      <S3AddForm
        artifact={s3}
        sources={sources}
        onBack={() => setChoice(null)}
        onDone={onClose}
        onBusyChange={onBusyChange}
      />
    );
  }

  if (choice === "bookwarehouse-audio" && bookwarehouse) {
    return (
      <BookwarehouseAudioForm
        artifact={bookwarehouse}
        onBack={() => setChoice(null)}
        onDone={onClose}
      />
    );
  }

  if (!s3 && !bookwarehouse) {
    return (
      <p className="text-muted-foreground text-sm">
        {bt("bloem.admin.storage.addDialog.noStorageTypesAreAvailable")}
      </p>
    );
  }
  return (
    <div className="space-y-3">
      {s3 ? (
        <PluginRow
          title={bt("bloem.admin.storage.addDialog.s3CompatibleStorage")}
          description={bt("bloem.admin.storage.addDialog.backblazeB2WasabiCloudflareR2", {
            version: s3.version,
          })}
        >
          <Button variant="outline" onClick={() => setChoice("s3")}>
            {bt("bloem.admin.storage.addDialog.setUpS3")}
          </Button>
        </PluginRow>
      ) : null}
      {bookwarehouse ? <BookwarehouseRow artifact={bookwarehouse} onDone={onClose} /> : null}
      {bookwarehouse ? (
        <PluginRow
          title={bt("bloem.admin.storage.addDialog.bookwarehouseAudiobooks")}
          description={bt("bloem.admin.storage.addDialog.audiobookTracks", {
            version: bookwarehouse.version,
          })}
        >
          <Button variant="outline" onClick={() => setChoice("bookwarehouse-audio")}>
            {bt("bloem.admin.storage.addDialog.setUpAudiobooks")}
          </Button>
        </PluginRow>
      ) : null}
    </div>
  );
}

/**
 * Adds a storage source. Which plugins are offered, and what an S3 source asks
 * for, comes from the verified catalog.
 */
export function AddStorageSourceDialog({
  artifacts,
  sources,
  open,
  onOpenChange,
  error,
  onRetry,
  isRetrying,
}: {
  artifacts?: StorageArtifact[];
  sources: StorageSource[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  error?: unknown;
  onRetry?: () => void;
  /** True while the catalog is being refetched after Retry. */
  isRetrying?: boolean;
}) {
  const bt = useBloemT();
  const [locked, setLocked] = useState(false);
  return (
    // Sources are being added one after another: closing now would leave a half-finished batch
    // that nobody is watching, so Escape, the overlay and the X are all ignored until it ends.
    <Dialog open={open} onOpenChange={(next) => (locked && !next ? undefined : onOpenChange(next))}>
      <DialogContent showCloseButton={!locked}>
        <DialogHeader>
          <DialogTitle>{bt("bloem.admin.storage.addDialog.addStorageSource")}</DialogTitle>
          <DialogDescription>
            {bt("bloem.admin.storage.addDialog.chooseWhereALibraryCan")}
          </DialogDescription>
        </DialogHeader>
        <DialogBody
          artifacts={artifacts}
          sources={sources}
          error={error}
          onRetry={onRetry}
          isRetrying={isRetrying}
          onClose={() => onOpenChange(false)}
          onBusyChange={setLocked}
        />
      </DialogContent>
    </Dialog>
  );
}
