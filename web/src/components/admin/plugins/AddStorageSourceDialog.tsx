import { useMemo, useState } from "react";
import { ArrowLeft } from "lucide-react";

import { PluginConfigForm } from "@/components/admin/plugins/PluginConfigForm";
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
  useDiscoverStorageSource,
  useInstallStorageSource,
  useStorageScope,
} from "@/hooks/queries/admin/storageSources";
import { randomUUID } from "@/lib/uuid";

import { newProviderSourceId, pruneConfigSchema, validateS3Connection } from "./storageSchema";

const S3_PLUGIN = "bloem.storage.s3";
const BOOKWAREHOUSE_PLUGIN = "bloem.storage.bookwarehouse";

// Server-managed or developer-only fields an administrator is never asked for.
const HIDDEN_S3_FIELDS = [
  "source_id",
  "state_path",
  "probe_key",
  "allow_loopback_http",
  "session_token",
];

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

/** What a new entry may carry: an emptied optional field is simply left out. */
function withoutEmpty(value: Record<string, unknown>): Record<string, unknown> {
  return Object.fromEntries(Object.entries(value).filter(([, v]) => v !== "" && v !== null));
}

/** The typed name, or the bucket (with the prefix, when set) so a source is never shown by its id. */
function sourceName(value: Record<string, unknown>): string {
  const text = (k: string) => (typeof value[k] === "string" ? (value[k] as string) : "");
  return (
    text("source_name").trim() || `${text("bucket")}${text("prefix") ? `/${text("prefix")}` : ""}`
  );
}

function S3AddForm({
  artifact,
  sources,
  onBack,
  onDone,
}: {
  artifact: StorageArtifact;
  sources: StorageSource[];
  onBack: () => void;
  onDone: () => void;
}) {
  const discover = useDiscoverStorageSource();
  const [errors, setErrors] = useState<string[]>([]);
  // Bumped after a save so the form remounts and the entered keys are discarded.
  const [formKey, setFormKey] = useState(0);
  // Only the two non-secret fields the duplicate warning compares are kept.
  const [bucket, setBucket] = useState("");
  const [prefix, setPrefix] = useState("");

  const source = artifact.config_schema[0];
  // Memoized: the form resets its values whenever it is handed a new schema.
  const schema = useMemo(
    () => (source ? pruneConfigSchema(source, HIDDEN_S3_FIELDS) : undefined),
    [source],
  );
  if (!schema) {
    return (
      <p className="text-muted-foreground text-sm">
        This S3 release does not describe its configuration, so it cannot be added here.
      </p>
    );
  }
  const labels = new Map((schema.admin_form?.fields ?? []).map((f) => [f.key, f.label || f.key]));

  const duplicate =
    bucket !== "" &&
    sources.some(
      (s) =>
        s.plugin_id === S3_PLUGIN &&
        s.connection_summary?.bucket === bucket &&
        (s.connection_summary?.prefix ?? "") === prefix,
    );

  function handleSave(key: string, value: Record<string, unknown>) {
    const problems = validateS3Connection(value);
    const messages = Object.entries(problems).map(
      ([field, message]) => `${labels.get(field) ?? field}: ${message}`,
    );
    setErrors(messages);
    if (messages.length > 0) return;
    const id = newProviderSourceId("s3");
    discover.mutate(
      {
        artifactKey: artifact.artifact_key,
        providerSourceId: id,
        config: {
          [key]: { ...withoutEmpty(value), source_name: sourceName(value), source_id: id },
        },
      },
      {
        onSuccess: () => {
          setFormKey((k) => k + 1);
          setBucket("");
          setPrefix("");
          onDone();
        },
      },
    );
  }

  return (
    <div className="space-y-3">
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={onBack}
        disabled={discover.isPending}
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back
      </Button>
      <PluginConfigForm
        key={formKey}
        bare
        idPrefix="s3-add"
        schema={schema}
        isSaving={discover.isPending}
        onSave={handleSave}
        onDraftChange={(_key, value) => {
          setBucket(typeof value.bucket === "string" ? value.bucket : "");
          setPrefix(typeof value.prefix === "string" ? value.prefix : "");
        }}
      />
      {duplicate ? (
        <p className="text-sm text-amber-600 dark:text-amber-400">
          A source with this bucket and prefix already exists. You can still add another.
        </p>
      ) : null}
      {errors.length > 0 ? (
        <ul role="alert" className="text-destructive list-disc space-y-1 pl-5 text-sm">
          {errors.map((message) => (
            <li key={message}>{message}</li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function DialogBody({
  artifacts,
  sources,
  error,
  onRetry,
  onClose,
}: {
  artifacts?: StorageArtifact[];
  sources: StorageSource[];
  error?: unknown;
  onRetry?: () => void;
  onClose: () => void;
}) {
  const { scope } = useStorageScope();
  const [choice, setChoice] = useState<"s3" | null>(null);

  if (!artifacts) {
    if (error) {
      return (
        <div role="alert" className="space-y-3 text-sm">
          <p className="text-destructive">
            Plugin catalog unavailable. The list of storage plugins could not be loaded.
          </p>
          {onRetry ? (
            <Button type="button" variant="outline" size="sm" onClick={onRetry}>
              Retry
            </Button>
          ) : null}
        </div>
      );
    }
    return <p className="text-muted-foreground text-sm">Loading storage plugins...</p>;
  }

  const s3 = scope === "platform" ? latestRelease(artifacts, S3_PLUGIN) : undefined;
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
 * for, comes from the verified catalog; S3 is only offered to platform admins.
 */
export function AddStorageSourceDialog({
  artifacts,
  sources,
  open,
  onOpenChange,
  error,
  onRetry,
}: {
  artifacts?: StorageArtifact[];
  sources: StorageSource[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  error?: unknown;
  onRetry?: () => void;
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
          onClose={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  );
}
