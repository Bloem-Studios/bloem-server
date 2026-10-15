import { useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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

function DialogBody({
  artifacts,
  sources,
  error,
  onRetry,
  isRetrying = false,
  onClose,
}: {
  artifacts?: StorageArtifact[];
  sources: StorageSource[];
  error?: unknown;
  onRetry?: () => void;
  isRetrying?: boolean;
  onClose: () => void;
}) {
  const [choice, setChoice] = useState<"s3" | null>(null);

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
      <S3AddForm artifact={s3} sources={sources} onBack={() => setChoice(null)} onDone={onClose} />
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
          description={`Amazon S3, MinIO, Ceph and similar · ${s3.version}`}
        >
          <Button variant="outline" onClick={() => setChoice("s3")}>
            S3
          </Button>
        </PluginRow>
      ) : null}
      {bookwarehouse ? <BookwarehouseRow artifact={bookwarehouse} onDone={onClose} /> : null}
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
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add storage source</DialogTitle>
          <DialogDescription>
            Choose where an ebook library can read its books from.
          </DialogDescription>
        </DialogHeader>
        <DialogBody
          artifacts={artifacts}
          sources={sources}
          error={error}
          onRetry={onRetry}
          isRetrying={isRetrying}
          onClose={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  );
}
