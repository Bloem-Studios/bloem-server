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
  const install = useInstallStorageSource();
  return (
    <PluginRow title="Bookwarehouse" description={`EPUB and PDF books · ${artifact.version}`}>
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
        {install.isPending ? "Installing..." : "Install Bookwarehouse"}
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
      <div className="flex justify-between gap-2">
        <Button type="button" variant="ghost" onClick={onBack} disabled={install.isPending}>
          Back
        </Button>
        <Button type="submit" disabled={!ready}>
          {install.isPending ? "Installing..." : "Install audiobooks"}
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
  const [choice, setChoice] = useState<"s3" | "bookwarehouse-audio" | null>(null);

  if (!artifacts) {
    if (error) {
      return (
        <div role="alert" className="space-y-3 text-sm">
          <p className="text-destructive">
            Plugin catalog unavailable. The list of storage plugins could not be loaded.
          </p>
          {onRetry ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={isRetrying}
              onClick={onRetry}
            >
              {isRetrying ? "Retrying…" : "Retry"}
            </Button>
          ) : null}
        </div>
      );
    }
    return <p className="text-muted-foreground text-sm">Loading storage plugins...</p>;
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
    return <p className="text-muted-foreground text-sm">No storage plugins are available.</p>;
  }
  return (
    <div className="space-y-3">
      {s3 ? (
        <PluginRow
          title="S3-compatible storage"
          description={`Backblaze B2, Wasabi, Cloudflare R2, Amazon S3 and more · ${s3.version}`}
        >
          <Button variant="outline" onClick={() => setChoice("s3")}>
            S3
          </Button>
        </PluginRow>
      ) : null}
      {bookwarehouse ? <BookwarehouseRow artifact={bookwarehouse} onDone={onClose} /> : null}
      {bookwarehouse ? (
        <PluginRow
          title="Bookwarehouse audiobooks"
          description={`Audiobook tracks · ${bookwarehouse.version}`}
        >
          <Button variant="outline" onClick={() => setChoice("bookwarehouse-audio")}>
            Audiobooks
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
  const [locked, setLocked] = useState(false);
  return (
    // Sources are being added one after another: closing now would leave a half-finished batch
    // that nobody is watching, so Escape, the overlay and the X are all ignored until it ends.
    <Dialog open={open} onOpenChange={(next) => (locked && !next ? undefined : onOpenChange(next))}>
      <DialogContent showCloseButton={!locked}>
        <DialogHeader>
          <DialogTitle>Add storage source</DialogTitle>
          <DialogDescription>
            Choose where a library can read its books, movies or audiobooks from.
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
