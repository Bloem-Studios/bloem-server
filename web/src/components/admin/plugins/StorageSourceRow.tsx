import { useBloemT } from "@/bloem/i18n";
import { useMemo, useState } from "react";
import type { FormEvent } from "react";
import { ArrowUpCircle, HardDrive } from "lucide-react";

import { PluginConfigForm } from "@/components/admin/plugins/PluginConfigForm";
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
  useDeleteRemovedStorageSource,
  useRemoveStorageSource,
  useReplaceStorageConfiguration,
  useRotateStorageCredentials,
  useStorageScope,
  useTestStorageSource,
  useUpgradeStorageSource,
} from "@/hooks/queries/admin/storageSources";

import {
  pruneConfigSchema,
  s3BucketOptional,
  sourceName,
  validateS3Connection,
  withS3FieldCopy,
  withoutEmpty,
} from "./storageSchema";
import { type SourceStatus, sourceStatus } from "./storageStatus";

const S3_PLUGIN = "bloem.storage.s3";
const BOOKWAREHOUSE_PLUGIN = "bloem.storage.bookwarehouse";
/** The server refuses a whole-configuration replacement on a source a library uses. */
const IN_USE_ERROR_CODE = "configuration_namespace_unverified";
const IN_USE_NOTICE = "A library uses this source, so only its keys can be changed.";
// The connection summary omits the plain-http endpoint of such a source, so a whole-configuration
// replace built from it would silently re-point the source; only its keys can be changed.
const LOOPBACK_LOCKED_NOTICE =
  "This source uses a local address without https, so only its keys can be changed here.";

// Server-managed or developer-only fields an administrator is never asked for when editing.
const HIDDEN_EDIT_FIELDS = [
  "source_id",
  "state_path",
  "probe_key",
  "allow_loopback_http",
  "session_token",
];
// The non-secret facts the source list returns, which prefill the edit form.
const SUMMARY_FIELDS = ["source_name", "bucket", "prefix", "region", "endpoint", "revision_mode"];

/** The machine-readable code on an error, whatever class carries it. */
function errorCode(error: unknown): string | undefined {
  if (typeof error === "object" && error !== null && "code" in error) {
    const code = (error as { code?: unknown }).code;
    return typeof code === "string" ? code : undefined;
  }
  return undefined;
}

function artifactLabel(artifact: StorageArtifact): string {
  return `Version ${artifact.version} (${artifact.os}/${artifact.arch})`;
}

/** What kind of storage a source is, in words rather than its plugin ID. */
function pluginLabel(pluginId: string): string {
  if (pluginId === S3_PLUGIN) return "S3 storage";
  if (pluginId === BOOKWAREHOUSE_PLUGIN) return "Bookwarehouse";
  return pluginId;
}

const STATUS_LABEL: Record<SourceStatus, string> = {
  live: "Live",
  setup: "Needs setup",
  off: "Off",
  removed: "Removed",
};

const STATUS_STYLE: Record<SourceStatus, string> = {
  live: "border-transparent bg-emerald-600/15 text-emerald-700 dark:text-emerald-400",
  setup: "border-transparent bg-amber-500/15 text-amber-700 dark:text-amber-400",
  off: "",
  removed: "text-muted-foreground",
};

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
  const bt = useBloemT();
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger aria-label={label} className="sm:flex-1">
        <SelectValue placeholder={bt("bloem.admin.storage.sourceRow.chooseAVersion")} />
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
  const bt = useBloemT();
  const upgrade = useUpgradeStorageSource();
  // Only catalog releases of the source's own plugin: a static (uploaded) release of the same
  // plugin is listed too, but the server refuses to upgrade to it.
  const releases = artifacts.filter((a) => a.plugin_id === source.plugin_id && a.catalog);
  const [artifactKey, setArtifactKey] = useState("");

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    if (!artifactKey || upgrade.isPending) return;
    upgrade.mutate({ source, artifactKey }, { onSuccess: onDone });
  }

  if (releases.length === 0) {
    return (
      <p className="text-muted-foreground mt-2 text-[13px]">
        {bt("bloem.admin.storage.sourceRow.noOtherVersionsOfAre", {
          plugin: pluginLabel(source.plugin_id),
        })}
      </p>
    );
  }
  return (
    <form onSubmit={handleSubmit} className="mt-2 flex flex-col gap-2 sm:flex-row">
      <ArtifactSelect
        artifacts={releases}
        value={artifactKey}
        onChange={setArtifactKey}
        label={bt("bloem.admin.storage.sourceRow.versionToUpgradeTo")}
      />
      <Button type="submit" size="sm" disabled={!artifactKey || upgrade.isPending}>
        {upgrade.isPending
          ? bt("bloem.admin.storage.sourceRow.upgrading")
          : bt("bloem.admin.storage.sourceRow.upgrade")}
      </Button>
    </form>
  );
}

function ConnectionForm({ source }: { source: StorageSource }) {
  const bt = useBloemT();
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
        {bt("bloem.admin.storage.sourceRow.bookwarehouseServerUrl")}
        <Input
          type="url"
          required
          placeholder={bt("bloem.admin.storage.sourceRow.httpsBooksExample")}
          value={url}
          onChange={(e) => setURL(e.target.value)}
          autoComplete="off"
        />
      </label>
      <label className="block text-sm">
        {bt("bloem.admin.storage.sourceRow.apiKey")}
        <Input
          type="password"
          required
          value={apiKey}
          onChange={(e) => setAPIKey(e.target.value)}
          autoComplete="new-password"
        />
      </label>
      <Button type="submit" disabled={configure.isPending || !url.trim() || !apiKey}>
        {configure.isPending
          ? bt("bloem.admin.storage.sourceRow.connecting")
          : bt("bloem.admin.storage.sourceRow.saveConnection")}
      </Button>
    </form>
  );
}

/** Replaces only the keys of a source; the rest of its configuration is untouched. */
function RotateKeysForm({ source, onDone }: { source: StorageSource; onDone: () => void }) {
  const bt = useBloemT();
  const rotate = useRotateStorageCredentials();
  const [accessKey, setAccessKey] = useState("");
  const [secretKey, setSecretKey] = useState("");
  const [sessionToken, setSessionToken] = useState("");

  function submit(event: FormEvent) {
    event.preventDefault();
    if (!accessKey || !secretKey || rotate.isPending) return;
    rotate.mutate(
      { source, accessKey, secretKey, sessionToken },
      {
        onSuccess: () => {
          setAccessKey("");
          setSecretKey("");
          setSessionToken("");
          onDone();
        },
      },
    );
  }

  return (
    <form onSubmit={submit} className="mt-3 space-y-3">
      <label className="block text-sm">
        {bt("bloem.admin.storage.sourceRow.accessKey")}
        <Input
          type="password"
          value={accessKey}
          onChange={(e) => setAccessKey(e.target.value)}
          autoComplete="new-password"
        />
      </label>
      <label className="block text-sm">
        {bt("bloem.admin.storage.sourceRow.secretKey")}
        <Input
          type="password"
          value={secretKey}
          onChange={(e) => setSecretKey(e.target.value)}
          autoComplete="new-password"
        />
      </label>
      <label className="block text-sm">
        {bt("bloem.admin.storage.sourceRow.sessionTokenOptional")}
        <Input
          type="password"
          value={sessionToken}
          onChange={(e) => setSessionToken(e.target.value)}
          autoComplete="new-password"
        />
      </label>
      <Button type="submit" disabled={rotate.isPending || !accessKey || !secretKey}>
        {rotate.isPending
          ? bt("bloem.admin.storage.sourceRow.saving")
          : bt("bloem.admin.storage.sourceRow.saveNewKeys")}
      </Button>
    </form>
  );
}

/**
 * Edits an unused S3 source by replacing its whole configuration. The keys are
 * not kept anywhere, so they must be entered again. A release that allows it
 * takes an empty bucket as every bucket.
 */
function S3EditForm({
  source,
  artifact,
  onSaved,
  onInUse,
}: {
  source: StorageSource;
  artifact: StorageArtifact;
  onSaved: () => void;
  onInUse: () => void;
}) {
  const bt = useBloemT();
  const replace = useReplaceStorageConfiguration();
  const [errors, setErrors] = useState<string[]>([]);
  const section = artifact.config_schema[0];
  // Memoized: the form resets its values whenever it is handed a new schema or value.
  const schema = useMemo(
    () => (section ? withS3FieldCopy(pruneConfigSchema(section, HIDDEN_EDIT_FIELDS)) : undefined),
    [section],
  );
  const bucketOptional = section ? s3BucketOptional(section) : false;
  const summary = source.connection_summary;
  const value = useMemo(
    () =>
      Object.fromEntries(
        SUMMARY_FIELDS.filter((k) => typeof summary?.[k] === "string").map((k) => [k, summary[k]]),
      ),
    [summary],
  );
  if (!schema) {
    return (
      <p className="text-muted-foreground mt-2 text-[13px]">
        {bt("bloem.admin.storage.sourceRow.thisVersionOfTheS3")}
      </p>
    );
  }
  const labels = new Map((schema.admin_form?.fields ?? []).map((f) => [f.key, f.label || f.key]));

  function handleSave(key: string, entry: Record<string, unknown>) {
    const problems = validateS3Connection(entry, { bucketOptional });
    const messages = Object.entries(problems).map(
      ([field, message]) => `${labels.get(field) ?? field}: ${message}`,
    );
    const filled = (k: string) => typeof entry[k] === "string" && entry[k] !== "";
    if (!filled("access_key") || !filled("secret_key"))
      messages.push("Enter the keys again to save.");
    setErrors(messages);
    if (messages.length > 0) return;
    replace.mutate(
      {
        source,
        config: {
          [key]: {
            ...withoutEmpty(entry),
            source_name: sourceName(entry),
            source_id: source.provider_source_id,
          },
        },
      },
      {
        onSuccess: onSaved,
        onError: (error) => {
          if (errorCode(error) === IN_USE_ERROR_CODE) onInUse();
        },
      },
    );
  }

  return (
    <div className="mt-3 space-y-3">
      <p className="text-muted-foreground text-[13px]">
        {bt("bloem.admin.storage.sourceRow.enterTheAccessKeyAnd")}
      </p>
      <PluginConfigForm
        bare
        idPrefix={`s3-edit-${source.source_key}`}
        schema={schema}
        value={value}
        isSaving={replace.isPending}
        onSave={handleSave}
      />
      {errors.length > 0 ? (
        <ul role="alert" className="text-destructive list-disc space-y-1 ps-5 text-sm">
          {errors.map((message) => (
            <li key={message}>{message}</li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function DeleteConfirmation({
  source,
  name,
  onDone,
}: {
  source: StorageSource;
  name: string;
  onDone: () => void;
}) {
  const bt = useBloemT();
  const remove = useDeleteRemovedStorageSource();
  return (
    <div className="mt-2 space-y-2 rounded-md border border-dashed p-3 text-[13px]">
      <p>{bt("bloem.admin.storage.sourceRow.deleteFromThisListNothing", { name })}</p>
      <div className="flex gap-2">
        <Button
          variant="destructive"
          size="sm"
          disabled={remove.isPending}
          onClick={() => remove.mutate({ source }, { onSuccess: onDone })}
        >
          {remove.isPending
            ? bt("bloem.admin.storage.sourceRow.deleting")
            : bt("bloem.admin.storage.sourceRow.deleteSource")}
        </Button>
        <Button variant="ghost" size="sm" disabled={remove.isPending} onClick={onDone}>
          {bt("bloem.admin.storage.sourceRow.keep")}
        </Button>
      </div>
    </div>
  );
}

function RemoveConfirmation({
  source,
  name,
  onDone,
}: {
  source: StorageSource;
  name: string;
  onDone: () => void;
}) {
  const bt = useBloemT();
  const remove = useRemoveStorageSource();
  return (
    <div className="mt-2 space-y-2 rounded-md border border-dashed p-3 text-[13px]">
      <p>{bt("bloem.admin.storage.sourceRow.removeThisCanAposT", { name })}</p>
      <div className="flex gap-2">
        <Button
          variant="destructive"
          size="sm"
          disabled={remove.isPending}
          onClick={() => remove.mutate({ source }, { onSuccess: onDone })}
        >
          {remove.isPending
            ? bt("bloem.admin.storage.sourceRow.removing")
            : bt("bloem.admin.storage.sourceRow.removeSource")}
        </Button>
        <Button variant="ghost" size="sm" disabled={remove.isPending} onClick={onDone}>
          {bt("bloem.admin.storage.sourceRow.keep")}
        </Button>
      </div>
    </div>
  );
}

function displayName(source: StorageSource): string {
  if (source.plugin_id === BOOKWAREHOUSE_PLUGIN) {
    return source.root_entry_id === "audio-root" ? "Bookwarehouse audiobooks" : "Bookwarehouse";
  }
  return source.connection_summary?.source_name || source.provider_source_id;
}

function SummaryLine({ source }: { source: StorageSource }) {
  const summary = source.connection_summary ?? {};
  // A configured source with no bucket covers every bucket its keys can see.
  const allBuckets = !summary.bucket && Object.keys(summary).length > 0;
  const parts = [
    allBuckets ? "All buckets" : "",
    summary.bucket ? `Bucket ${summary.bucket}` : "",
    summary.prefix ? `Folder ${summary.prefix}` : "",
    summary.region ? `Region ${summary.region}` : "",
    summary.endpoint ? `Address ${summary.endpoint}` : "",
    summary.base_url_host ? `Server ${summary.base_url_host}` : "",
    summary.revision_mode === "checked-etag"
      ? "Checks each file"
      : summary.revision_mode
        ? "Exact versions only"
        : "",
  ].filter(Boolean);
  if (parts.length === 0) return null;
  return (
    <p className="text-muted-foreground mt-1 flex flex-wrap gap-x-3 ps-[26px] text-[12.5px]">
      {parts.map((part) => (
        <span key={part}>{part}</span>
      ))}
    </p>
  );
}

/** Which libraries read from the source, or what its status means when none do. */
function UsageLine({ source, status }: { source: StorageSource; status: SourceStatus }) {
  const names = (source.libraries ?? []).map((library) => library.name);
  let text: string;
  if (names.length > 0) {
    text = `Used by ${names.join(", ")}. A source a library uses can't be removed.`;
  } else if (source.in_use) {
    text = "Used by a library in another organization.";
  } else if (status === "removed") {
    text =
      "Its plugin was removed, so it can't be turned back on. Add a new source to use this storage again.";
  } else if (status === "setup") {
    text = "Not connected yet. Enter its connection details below.";
  } else {
    text = "Not used by any library.";
  }
  return <p className="text-muted-foreground mt-1 ps-[26px] text-[12.5px]">{text}</p>;
}

type Panel = "edit" | "rotate" | "upgrade" | null;

/**
 * One storage source in the list: its non-secret summary and the actions that
 * apply to it. `inUse` is true when a library is known to use the source; when
 * that is unknown the server's refusal to replace its configuration moves the
 * row to the rotate-only view.
 */
export function StorageSourceRow({
  source,
  artifact,
  artifacts = [],
  inUse = false,
}: {
  source: StorageSource;
  /** The release whose configuration schema describes this source's plugin. */
  artifact?: StorageArtifact;
  /** Catalog releases an upgrade may switch to. */
  artifacts?: StorageArtifact[];
  inUse?: boolean;
}) {
  const bt = useBloemT();
  const test = useTestStorageSource();
  const { scope } = useStorageScope();
  const [panel, setPanel] = useState<Panel>(null);
  const [serverSaysInUse, setServerSaysInUse] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null);

  const isS3 = source.plugin_id === S3_PLUGIN;
  const isBookwarehouse = source.plugin_id === BOOKWAREHOUSE_PLUGIN;
  const usedByLibrary = inUse || source.in_use === true;
  const locked = usedByLibrary || serverSaysInUse;
  const status = sourceStatus(source);
  const manageable = source.installation_id !== null;
  const name = displayName(source);
  const kind = pluginLabel(source.plugin_id);
  const sourceDisabled = !source.enabled || source.state === "disabled";
  // The server refuses to change a platform source from an organization scope.
  const platformBlocked = scope === "organization" && source.owner_kind === "platform";
  const canManage = manageable && !sourceDisabled && !platformBlocked;
  // S3 releases are approved and upgraded by the platform; an organization never upgrades them.
  const canUpgrade = !platformBlocked && !(isS3 && scope === "organization");
  const loopbackLocked = source.connection_summary?.allow_loopback_http === "true";
  const testHintId = `test-hint-${source.source_key}`;

  function toggle(next: Exclude<Panel, null>) {
    setPanel(panel === next ? null : next);
  }

  return (
    <li className="py-2.5 text-[13.5px]">
      <div className="flex min-w-0 flex-wrap items-center gap-2.5">
        <HardDrive className="text-muted-foreground h-4 w-4 shrink-0" aria-hidden />
        <span className="min-w-0 flex-1 truncate">
          <span className="font-medium">{name}</span>
          {kind !== name ? (
            <span className="text-muted-foreground">
              {bt("bloem.admin.storage.sourceRow.kind", { kind })}
            </span>
          ) : null}
        </span>
        <Badge variant="outline" className={STATUS_STYLE[status]}>
          {STATUS_LABEL[status]}
        </Badge>
      </div>

      <SummaryLine source={source} />
      <UsageLine source={source} status={status} />

      <div className="mt-1.5 flex flex-wrap gap-1 ps-[26px]">
        {source.configured ? (
          <>
            <Button
              variant="ghost"
              size="sm"
              disabled={test.isPending || sourceDisabled}
              aria-describedby={sourceDisabled ? testHintId : undefined}
              onClick={() => {
                setTestResult(null);
                test.mutate({ source }, { onSuccess: setTestResult });
              }}
            >
              {test.isPending
                ? bt("bloem.admin.storage.sourceRow.testing")
                : bt("bloem.admin.storage.sourceRow.test")}
            </Button>
            {sourceDisabled ? (
              <span id={testHintId} className="sr-only">
                {bt("bloem.admin.storage.sourceRow.aDisabledSourceCanT")}
              </span>
            ) : null}
          </>
        ) : null}
        {isS3 && canManage && !locked && !loopbackLocked ? (
          <Button variant="ghost" size="sm" onClick={() => toggle("edit")}>
            {panel === "edit"
              ? bt("bloem.admin.storage.sourceRow.cancel")
              : bt("bloem.admin.storage.sourceRow.edit")}
          </Button>
        ) : null}
        {isS3 && canManage ? (
          <Button variant="ghost" size="sm" onClick={() => toggle("rotate")}>
            {panel === "rotate"
              ? bt("bloem.admin.storage.sourceRow.cancel")
              : bt("bloem.admin.storage.sourceRow.replaceKeys")}
          </Button>
        ) : null}
        {source.state === "attached" && canUpgrade ? (
          <Button variant="ghost" size="sm" onClick={() => toggle("upgrade")}>
            <ArrowUpCircle className="h-3.5 w-3.5" />
            {panel === "upgrade"
              ? bt("bloem.admin.storage.sourceRow.cancel")
              : bt("bloem.admin.storage.sourceRow.upgrade")}
          </Button>
        ) : null}
        {status === "removed" && !platformBlocked && !usedByLibrary ? (
          <Button variant="ghost" size="sm" onClick={() => setRemoving(true)} disabled={removing}>
            {bt("bloem.admin.storage.sourceRow.delete")}
          </Button>
        ) : null}
        {manageable && !platformBlocked && !usedByLibrary ? (
          <Button variant="ghost" size="sm" onClick={() => setRemoving(true)} disabled={removing}>
            {bt("bloem.admin.storage.sourceRow.remove")}
          </Button>
        ) : null}
      </div>

      {isS3 && canManage && loopbackLocked ? (
        <p className="text-muted-foreground mt-1 ps-[26px] text-[13px]">{LOOPBACK_LOCKED_NOTICE}</p>
      ) : null}

      {testResult ? (
        <p
          role="status"
          className={`mt-1 ps-[26px] text-[13px] ${testResult.ok ? "text-muted-foreground" : "text-destructive"}`}
        >
          {testResult.message}
        </p>
      ) : null}

      {!source.configured && isBookwarehouse ? <ConnectionForm source={source} /> : null}

      {isS3 && panel === "edit" && !locked ? (
        artifact ? (
          <S3EditForm
            source={source}
            artifact={artifact}
            onSaved={() => {
              setTestResult(null);
              setPanel(null);
            }}
            onInUse={() => {
              setServerSaysInUse(true);
              setPanel("rotate");
            }}
          />
        ) : (
          <p className="text-muted-foreground mt-2 text-[13px]">
            {bt("bloem.admin.storage.sourceRow.thePluginCatalogHasNo", {
              plugin: pluginLabel(source.plugin_id),
            })}
          </p>
        )
      ) : null}

      {isS3 && panel === "rotate" ? (
        <>
          {locked ? (
            <p className="text-muted-foreground mt-2 text-[13px]">{IN_USE_NOTICE}</p>
          ) : null}
          <RotateKeysForm
            source={source}
            onDone={() => {
              setTestResult(null);
              setPanel(null);
            }}
          />
        </>
      ) : null}

      {panel === "upgrade" ? (
        <UpgradeForm source={source} artifacts={artifacts} onDone={() => setPanel(null)} />
      ) : null}

      {removing ? (
        status === "removed" ? (
          <DeleteConfirmation source={source} name={name} onDone={() => setRemoving(false)} />
        ) : (
          <RemoveConfirmation source={source} name={name} onDone={() => setRemoving(false)} />
        )
      ) : null}
    </li>
  );
}
