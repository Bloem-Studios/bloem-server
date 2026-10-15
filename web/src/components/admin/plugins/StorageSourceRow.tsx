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
  useDisableStorageSource,
  useRemoveStorageSource,
  useReplaceStorageConfiguration,
  useRotateStorageCredentials,
  useTestStorageSource,
  useUpgradeStorageSource,
} from "@/hooks/queries/admin/storageSources";

import { pruneConfigSchema, sourceName, validateS3Connection, withoutEmpty } from "./storageSchema";

const S3_PLUGIN = "bloem.storage.s3";
const BOOKWAREHOUSE_PLUGIN = "bloem.storage.bookwarehouse";
/** The server refuses a whole-configuration replacement on a source a library uses. */
const IN_USE_ERROR_CODE = "configuration_namespace_unverified";
const IN_USE_NOTICE = "This source is used by a library; only the keys can be changed.";
// The edit form cannot send allow_loopback_http, so a whole-configuration replace turns it off.
const LOOPBACK_NOTICE =
  "This source allows plain HTTP on loopback; saving keeps it off. Re-add the source to change.";

// Server-managed or developer-only fields an administrator is never asked for when editing.
const HIDDEN_EDIT_FIELDS = ["source_id", "state_path", "allow_loopback_http", "session_token"];
// The non-secret facts the source list returns, which prefill the edit form.
const SUMMARY_FIELDS = [
  "source_name",
  "bucket",
  "prefix",
  "region",
  "endpoint",
  "revision_mode",
  "probe_key",
];

/** The machine-readable code on an error, whatever class carries it. */
function errorCode(error: unknown): string | undefined {
  if (typeof error === "object" && error !== null && "code" in error) {
    const code = (error as { code?: unknown }).code;
    return typeof code === "string" ? code : undefined;
  }
  return undefined;
}

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

/** Replaces only the keys of a source; the rest of its configuration is untouched. */
function RotateKeysForm({ source, onDone }: { source: StorageSource; onDone: () => void }) {
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
        Access key
        <Input
          type="password"
          value={accessKey}
          onChange={(e) => setAccessKey(e.target.value)}
          autoComplete="new-password"
        />
      </label>
      <label className="block text-sm">
        Secret key
        <Input
          type="password"
          value={secretKey}
          onChange={(e) => setSecretKey(e.target.value)}
          autoComplete="new-password"
        />
      </label>
      <label className="block text-sm">
        Session token (optional)
        <Input
          type="password"
          value={sessionToken}
          onChange={(e) => setSessionToken(e.target.value)}
          autoComplete="new-password"
        />
      </label>
      <Button type="submit" disabled={rotate.isPending || !accessKey || !secretKey}>
        {rotate.isPending ? "Rotating..." : "Rotate keys"}
      </Button>
    </form>
  );
}

/**
 * Edits an unused S3 source by replacing its whole configuration. The keys are
 * not kept anywhere, so they must be entered again.
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
  const replace = useReplaceStorageConfiguration();
  const [errors, setErrors] = useState<string[]>([]);
  const section = artifact.config_schema[0];
  // Memoized: the form resets its values whenever it is handed a new schema or value.
  const schema = useMemo(
    () => (section ? pruneConfigSchema(section, HIDDEN_EDIT_FIELDS) : undefined),
    [section],
  );
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
        This S3 release does not describe its configuration, so the source cannot be edited here.
      </p>
    );
  }
  const labels = new Map((schema.admin_form?.fields ?? []).map((f) => [f.key, f.label || f.key]));

  function handleSave(key: string, entry: Record<string, unknown>) {
    const problems = validateS3Connection(entry);
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
      {summary?.allow_loopback_http === "true" ? (
        <p className="text-muted-foreground text-[13px]">{LOOPBACK_NOTICE}</p>
      ) : null}
      <PluginConfigForm
        bare
        idPrefix={`s3-edit-${source.source_key}`}
        schema={schema}
        value={value}
        isSaving={replace.isPending}
        onSave={handleSave}
      />
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

function RemoveConfirmation({
  source,
  name,
  onDone,
}: {
  source: StorageSource;
  name: string;
  onDone: () => void;
}) {
  const remove = useRemoveStorageSource();
  return (
    <div className="mt-2 space-y-2 rounded-md border border-dashed p-3 text-[13px]">
      <p>
        Remove {name}? The source&apos;s settings are retained, but libraries that read from it can
        no longer reach their books until it is set up again.
      </p>
      <div className="flex gap-2">
        <Button
          variant="destructive"
          size="sm"
          disabled={remove.isPending}
          onClick={() => remove.mutate({ source }, { onSuccess: onDone })}
        >
          {remove.isPending ? "Removing..." : "Confirm remove"}
        </Button>
        <Button variant="ghost" size="sm" disabled={remove.isPending} onClick={onDone}>
          Keep
        </Button>
      </div>
    </div>
  );
}

function displayName(source: StorageSource): string {
  if (source.plugin_id === BOOKWAREHOUSE_PLUGIN) return "Bookwarehouse";
  return source.connection_summary?.source_name || source.provider_source_id;
}

function SummaryLine({ source }: { source: StorageSource }) {
  const summary = source.connection_summary ?? {};
  const parts = [
    summary.bucket ? `Bucket ${summary.bucket}` : "",
    summary.prefix ? `Prefix ${summary.prefix}` : "",
    summary.region ? `Region ${summary.region}` : "",
    summary.endpoint ? `Endpoint ${summary.endpoint}` : "",
  ].filter(Boolean);
  if (parts.length === 0) return null;
  return (
    <p className="text-muted-foreground mt-1 flex flex-wrap gap-x-3 pl-[26px] text-[12.5px]">
      {parts.map((part) => (
        <span key={part}>{part}</span>
      ))}
    </p>
  );
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
  const test = useTestStorageSource();
  const disable = useDisableStorageSource();
  const [panel, setPanel] = useState<Panel>(null);
  const [serverSaysInUse, setServerSaysInUse] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null);

  const isS3 = source.plugin_id === S3_PLUGIN;
  const isBookwarehouse = source.plugin_id === BOOKWAREHOUSE_PLUGIN;
  const locked = inUse || serverSaysInUse;
  const manageable = source.installation_id !== null;
  const name = displayName(source);
  const sourceDisabled = !source.enabled || source.state === "disabled";
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
          <span className="text-muted-foreground"> · {source.plugin_id}</span>
        </span>
        <Badge variant={source.state === "attached" ? "secondary" : "outline"}>
          {source.state}
        </Badge>
        {!source.enabled ? <Badge variant="outline">disabled</Badge> : null}
      </div>

      {isS3 ? <SummaryLine source={source} /> : null}

      <div className="mt-1.5 flex flex-wrap gap-1 pl-[26px]">
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
              {test.isPending ? "Testing..." : "Test"}
            </Button>
            {sourceDisabled ? (
              <span id={testHintId} className="sr-only">
                Enable the source to test its connection.
              </span>
            ) : null}
          </>
        ) : null}
        {isS3 && !locked ? (
          <Button variant="ghost" size="sm" onClick={() => toggle("edit")}>
            {panel === "edit" ? "Cancel" : "Edit"}
          </Button>
        ) : null}
        {isS3 ? (
          <Button variant="ghost" size="sm" onClick={() => toggle("rotate")}>
            {panel === "rotate" ? "Cancel" : "Rotate keys"}
          </Button>
        ) : null}
        {source.state === "attached" ? (
          <Button variant="ghost" size="sm" onClick={() => toggle("upgrade")}>
            <ArrowUpCircle className="h-3.5 w-3.5" />
            {panel === "upgrade" ? "Cancel" : "Upgrade"}
          </Button>
        ) : null}
        {manageable && source.enabled ? (
          <Button
            variant="ghost"
            size="sm"
            disabled={disable.isPending}
            onClick={() => disable.mutate({ source }, { onSuccess: () => setPanel(null) })}
          >
            Disable
          </Button>
        ) : null}
        {manageable ? (
          <Button variant="ghost" size="sm" onClick={() => setRemoving(true)} disabled={removing}>
            Remove
          </Button>
        ) : null}
      </div>

      {testResult ? (
        <p
          role="status"
          className={`mt-1 pl-[26px] text-[13px] ${testResult.ok ? "text-muted-foreground" : "text-destructive"}`}
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
            The plugin catalog has no release of {source.plugin_id}, so the source cannot be edited
            here.
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
        <RemoveConfirmation source={source} name={name} onDone={() => setRemoving(false)} />
      ) : null}
    </li>
  );
}
