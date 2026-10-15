import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowLeft, CheckCircle2, ChevronDown, ChevronRight } from "lucide-react";
import { Link } from "react-router";

import type { PluginAdminForm } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import {
  type S3ConnectionInput,
  type StorageArtifact,
  type StorageSource,
  useDiscoverStorageSource,
  useListS3Buckets,
} from "@/hooks/queries/admin/storageSources";

import { adminFormForConfigSchema, formValuesFromConfig } from "./configSchemaAdminForm";
import { S3FolderBrowser } from "./S3FolderBrowser";
import {
  AMAZON_REGION_MESSAGE,
  DEFAULT_REGION,
  browseErrorMessage,
  defaultRegionFor,
  defaultRevisionModeFor,
  isAmazonEndpoint,
  isBrowseUnsupported,
  type RevisionMode,
} from "./s3Browse";
import { parseS3Details } from "./s3Paste";
import { SchemaForm } from "./SchemaForm";
import { buildSchemaValues } from "./schemaFormUtils";
import {
  newProviderSourceId,
  pruneConfigSchema,
  sourceName,
  validateS3Connection,
  withoutEmpty,
} from "./storageSchema";

const S3_PLUGIN = "bloem.storage.s3";

// Server-managed or developer-only fields an administrator is never asked for.
const HIDDEN_S3_FIELDS = ["source_id", "state_path", "probe_key", "allow_loopback_http"];
// What every administrator is given by a provider, and so all that is shown up front.
const PRIMARY_KEYS = ["endpoint", "access_key", "secret_key"];
// The order of the rest, under "Advanced options".
const ADVANCED_ORDER = ["bucket", "region", "prefix", "session_token", "source_name"];
const SECRET_KEYS = ["access_key", "secret_key", "session_token"];
// Changing any of these makes a bucket list loaded with the old values untrustworthy.
const CONNECTION_KEYS = ["endpoint", "region", "access_key", "secret_key", "session_token"];
const SAVE_OPTIONS = { explicitClears: true };

type Mode = "connect" | "connected" | "typed";
type Values = Record<string, unknown>;

interface Outcome {
  id: string;
  state: "waiting" | "adding" | "added" | "failed";
  message?: string;
  name?: string;
}

interface Problem {
  field: string;
  text: string;
}

const REVISION_OPTIONS: { value: RevisionMode; label: string }[] = [
  { value: "checked-etag", label: "Checked ETag" },
  { value: "strict", label: "Versioned (strict default)" },
];

function ordered(form: PluginAdminForm, first: string[]): PluginAdminForm {
  const rank = (key: string) => {
    const index = first.indexOf(key);
    return index === -1 ? first.length : index;
  };
  return {
    ...form,
    // Flat: the add form lays out its own groups, so the manifest's sections are not used.
    sections: undefined,
    fields: [...form.fields].sort((a, b) => rank(a.key) - rank(b.key)),
  };
}

function failureText(error: unknown): string {
  return error instanceof Error && error.message ? error.message : "Couldn't add this bucket.";
}

function plural(count: number, one: string, many: string): string {
  return count === 1 ? one : many;
}

/**
 * The S3 part of the Add dialog. An administrator pastes or types an endpoint
 * and a pair of keys, connects to list the buckets, picks one or more, and one
 * source is created for each. The keys live only in this component's state and
 * its password inputs, and are dropped as soon as the sources are added.
 */
export function S3AddForm({
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
  const section = artifact.config_schema[0];
  // Memoized: a new schema would reset the form.
  const form = useMemo(() => {
    if (!section) return undefined;
    const schema = pruneConfigSchema(section, HIDDEN_S3_FIELDS);
    const descriptor = adminFormForConfigSchema(schema);
    if (!descriptor) return undefined;
    return {
      key: schema.key,
      descriptor: {
        ...descriptor,
        fields: descriptor.fields.map((field) =>
          SECRET_KEYS.includes(field.key)
            ? { ...field, control: "PASSWORD" as const, secret: true }
            : field,
        ),
      },
    };
  }, [section]);
  if (!form) {
    return (
      <p className="text-muted-foreground text-sm">
        This S3 release does not describe its configuration, so it cannot be added here.
      </p>
    );
  }
  return (
    <S3AddFormBody
      artifactKey={artifact.artifact_key}
      schemaKey={form.key}
      descriptor={form.descriptor}
      sources={sources}
      onBack={onBack}
      onDone={onDone}
    />
  );
}

function S3AddFormBody({
  artifactKey,
  schemaKey,
  descriptor,
  sources,
  onBack,
  onDone,
}: {
  artifactKey: string;
  schemaKey: string;
  descriptor: PluginAdminForm;
  sources: StorageSource[];
  onBack: () => void;
  onDone: () => void;
}) {
  const discover = useDiscoverStorageSource();
  const listBuckets = useListS3Buckets();

  const initial = useMemo<Values>(
    () => ({
      ...formValuesFromConfig(descriptor.fields),
      region: DEFAULT_REGION,
      revision_mode: defaultRevisionModeFor(""),
    }),
    [descriptor],
  );
  const [values, setValues] = useState<Values>(initial);
  // Region and revision mode follow the endpoint until the administrator sets them.
  const touched = useRef(new Set<string>());
  const [mode, setMode] = useState<Mode>("connect");
  const [buckets, setBuckets] = useState<string[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [browsing, setBrowsing] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [pasteText, setPasteText] = useState("");
  const [pasteNote, setPasteNote] = useState("");
  const [problems, setProblems] = useState<string[]>([]);
  const [listError, setListError] = useState<{ message: string } | null>(null);
  const [connecting, setConnecting] = useState(false);
  const [results, setResults] = useState<Record<string, Outcome>>({});
  const [saving, setSaving] = useState(false);
  const [added, setAdded] = useState<string[] | null>(null);
  // Ignores a bucket listing that finishes after the connection details changed.
  const listSeq = useRef(0);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const labels = new Map(descriptor.fields.map((f) => [f.key, f.label || f.key]));
  const label = (key: string) => labels.get(key) ?? key;
  const text = (key: string) => (typeof values[key] === "string" ? (values[key] as string) : "");
  const busy = connecting || saving;
  const revision: RevisionMode = values.revision_mode === "strict" ? "strict" : "checked-etag";

  const primaryForm = ordered(descriptor, PRIMARY_KEYS);
  const advancedForm = ordered(descriptor, ADVANCED_ORDER);
  const allKeys = descriptor.fields.map((f) => f.key);
  const primaryHidden = allKeys.filter((k) => !PRIMARY_KEYS.includes(k));
  const targets = mode === "typed" ? [text("bucket")] : selected;
  const single = targets.length <= 1;
  const prefix = single ? text("prefix") : "";
  const advancedHidden = [
    ...PRIMARY_KEYS,
    "revision_mode",
    ...(mode === "typed" ? [] : ["bucket"]),
    ...(single ? [] : ["prefix", "source_name"]),
  ];

  function connection(): S3ConnectionInput {
    return {
      endpoint: text("endpoint"),
      region: text("region"),
      accessKey: text("access_key"),
      secretKey: text("secret_key"),
      sessionToken: text("session_token"),
    };
  }

  // Drops everything learned with the old connection details.
  function dropConnection() {
    listSeq.current += 1;
    setConnecting(false);
    setMode((current) => (current === "connected" ? "connect" : current));
    setBuckets([]);
    setSelected([]);
    setBrowsing(false);
    setListError(null);
    setProblems([]);
    setResults({});
  }

  function update(next: Values) {
    let merged = next;
    if (next.endpoint !== values.endpoint) {
      const endpoint = typeof next.endpoint === "string" ? next.endpoint : "";
      merged = { ...next };
      if (!touched.current.has("region")) merged.region = defaultRegionFor(endpoint);
      if (!touched.current.has("revision_mode"))
        merged.revision_mode = defaultRevisionModeFor(endpoint);
    }
    if (CONNECTION_KEYS.some((key) => merged[key] !== values[key])) dropConnection();
    setValues(merged);
  }

  function handleChange(next: Values) {
    if (next.region !== values.region) touched.current.add("region");
    update(next);
  }

  function applyPaste(pasted: string) {
    const found = parseS3Details(pasted);
    const next: Values = { ...values };
    const filled: string[] = [];
    if (found.endpoint) {
      next.endpoint = found.endpoint;
      filled.push("address");
    }
    if (found.accessKey) {
      next.access_key = found.accessKey;
      filled.push("access key");
    }
    if (found.secretKey) {
      next.secret_key = found.secretKey;
      filled.push("secret key");
    }
    if (found.region) {
      next.region = found.region;
      touched.current.add("region");
      filled.push("region");
    }
    if (filled.length === 0) {
      setPasteNote("We couldn't find an address or keys in that text. Type them in below.");
      return;
    }
    update(next);
    // Cleared so the keys are not left on screen twice.
    setPasteText("");
    setPasteNote(`Filled in: ${filled.join(", ")}. Check them below, then connect.`);
  }

  function connectionProblems(): Problem[] {
    const found: Problem[] = [];
    const endpoint = text("endpoint");
    if (!endpoint)
      found.push({ field: "endpoint", text: `${label("endpoint")}: Enter the address.` });
    else {
      const bad = validateS3Connection({ endpoint }).endpoint;
      if (bad) found.push({ field: "endpoint", text: `${label("endpoint")}: ${bad}` });
    }
    if (!text("access_key"))
      found.push({ field: "access_key", text: `${label("access_key")}: Enter the access key.` });
    if (!text("secret_key"))
      found.push({ field: "secret_key", text: `${label("secret_key")}: Enter the secret key.` });
    if (!text("region").trim())
      found.push({
        field: "region",
        text: isAmazonEndpoint(endpoint)
          ? AMAZON_REGION_MESSAGE
          : `${label("region")}: Enter the region.`,
      });
    return found;
  }

  function showProblems(found: Problem[]) {
    setProblems(found.map((p) => p.text));
    setListError(null);
    if (
      found.some(
        (p) =>
          p.field === "region" ||
          p.field === "prefix" ||
          (p.field === "bucket" && mode === "typed"),
      )
    )
      setAdvanced(true);
  }

  async function connect() {
    const found = connectionProblems();
    showProblems(found);
    if (found.length > 0) return;
    const id = ++listSeq.current;
    setConnecting(true);
    try {
      const names = await listBuckets.mutateAsync(connection());
      if (id !== listSeq.current) return;
      setBuckets(names);
      // A single bucket is the obvious choice.
      setSelected(names.length === 1 ? names : []);
      setMode("connected");
    } catch (error) {
      if (id !== listSeq.current) return;
      setListError({ message: browseErrorMessage(error) });
      // The same answer would come back, so offer typing straight away.
      if (isBrowseUnsupported(error)) setAdvanced(true);
    } finally {
      if (id === listSeq.current) setConnecting(false);
    }
  }

  function typeInstead() {
    setMode("typed");
    setAdvanced(true);
    setBrowsing(false);
    setListError(null);
    setProblems([]);
  }

  function chooseBuckets(next: string[]) {
    // A folder chosen inside one bucket means nothing for another selection.
    if (selected.length > 0 && values.prefix) setValues({ ...values, prefix: "" });
    setBrowsing(false);
    setSelected(buckets.filter((b) => next.includes(b)));
  }

  function toggleBucket(bucket: string) {
    chooseBuckets(
      selected.includes(bucket) ? selected.filter((b) => b !== bucket) : [...selected, bucket],
    );
  }

  function buildEntry(bucket: string, id: string, only: boolean): Values {
    const built = buildSchemaValues(descriptor, values, undefined, SAVE_OPTIONS);
    const entryPrefix = only && typeof built.prefix === "string" ? built.prefix : "";
    const entry = withoutEmpty({
      ...built,
      bucket,
      prefix: entryPrefix,
      source_name: only ? built.source_name : "",
    });
    delete entry.revision_mode;
    if (revision === "checked-etag") entry.revision_mode = "checked-etag";
    entry.source_name = sourceName({ source_name: entry.source_name, bucket, prefix: entryPrefix });
    entry.source_id = id;
    return entry;
  }

  function saveProblems(): Problem[] {
    const found = connectionProblems();
    const only = targets.length <= 1;
    if (mode !== "typed" && targets.length === 0)
      found.push({ field: "bucket", text: `${label("bucket")}: Select at least one bucket.` });
    const badPrefix = validateS3Connection({ prefix: only ? text("prefix") : "" }).prefix;
    if (badPrefix) found.push({ field: "prefix", text: `${label("prefix")}: ${badPrefix}` });
    for (const bucket of targets) {
      const bad = validateS3Connection({ bucket }).bucket;
      if (bad)
        found.push({
          field: "bucket",
          text: targets.length > 1 ? `Bucket ${bucket}: ${bad}` : `${label("bucket")}: ${bad}`,
        });
    }
    return found;
  }

  async function save() {
    const found = saveProblems();
    showProblems(found);
    if (found.length > 0) return;
    const only = targets.length === 1;
    const pending = targets.filter((b) => results[b]?.state !== "added");
    // A retry keeps the id of the failed attempt, so an attempt whose outcome the
    // server could not confirm is reconciled rather than duplicated.
    const progress: Record<string, Outcome> = { ...results };
    for (const bucket of pending) {
      progress[bucket] = { id: results[bucket]?.id ?? newProviderSourceId("s3"), state: "waiting" };
    }
    setResults({ ...progress });
    setSaving(true);
    for (const bucket of pending) {
      const id = progress[bucket]!.id;
      progress[bucket] = { id, state: "adding" };
      setResults({ ...progress });
      const entry = buildEntry(bucket, id, only);
      try {
        await discover.mutateAsync({
          artifactKey,
          providerSourceId: id,
          config: { [schemaKey]: entry },
        });
        progress[bucket] = { id, state: "added", name: String(entry.source_name) };
      } catch (error) {
        progress[bucket] = { id, state: "failed", message: failureText(error) };
      }
      // The dialog was closed: do not go on adding sources nobody is watching.
      if (!alive.current) return;
      setResults({ ...progress });
    }
    setSaving(false);
    if (targets.every((b) => progress[b]?.state === "added")) {
      const names = targets.map((b) => progress[b]?.name ?? b);
      resetForm();
      setAdded(names);
    }
  }

  // Forgets every entry, the keys included.
  function resetForm() {
    listSeq.current += 1;
    touched.current.clear();
    setValues(initial);
    setMode("connect");
    setBuckets([]);
    setSelected([]);
    setBrowsing(false);
    setAdvanced(false);
    setPasteText("");
    setPasteNote("");
    setProblems([]);
    setListError(null);
    setConnecting(false);
    setResults({});
  }

  if (added) {
    return (
      <div className="space-y-3" role="status">
        <p className="flex items-start gap-2 text-sm font-medium">
          <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-emerald-600" aria-hidden />
          <span>
            Added {added.length} {plural(added.length, "source", "sources")}: {added.join(", ")}
          </span>
        </p>
        <div className="flex flex-wrap gap-2">
          <Button asChild>
            <Link to="/admin/libraries" onClick={onDone}>
              Add a library from these
            </Link>
          </Button>
          <Button type="button" variant="outline" onClick={() => setAdded(null)}>
            Add another
          </Button>
        </div>
      </div>
    );
  }

  const pending = targets.filter((b) => results[b]?.state !== "added");
  const failed = pending.filter((b) => results[b]?.state === "failed");
  const duplicates = targets.filter(
    (b) =>
      b !== "" &&
      results[b]?.state !== "added" &&
      sources.some(
        (s) =>
          s.plugin_id === S3_PLUGIN &&
          s.connection_summary?.bucket === b &&
          (s.connection_summary?.prefix ?? "") === prefix,
      ),
  );
  const saveLabel = saving
    ? "Adding..."
    : failed.length > 0 && failed.length === pending.length
      ? `Retry failed (${failed.length})`
      : pending.length > 1
        ? `Add ${pending.length} sources`
        : "Add source";
  const connectReady =
    text("endpoint") !== "" && text("access_key") !== "" && text("secret_key") !== "";
  const allSelected = buckets.length > 0 && selected.length === buckets.length;
  const resultEntries = Object.entries(results);

  return (
    <div className="space-y-3">
      <Button type="button" variant="ghost" size="sm" onClick={onBack} disabled={busy}>
        <ArrowLeft className="h-3.5 w-3.5" />
        Back
      </Button>

      <fieldset disabled={busy} className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="s3-add-paste">Paste your connection details</Label>
          <p className="text-muted-foreground text-xs">
            Paste what your provider sent you. The address and keys are read here in your browser;
            nothing is sent until you connect.
          </p>
          <textarea
            id="s3-add-paste"
            className="border-border bg-background min-h-20 w-full rounded-md border px-3 py-2 text-sm"
            rows={3}
            autoComplete="off"
            spellCheck={false}
            value={pasteText}
            onChange={(event) => {
              const next = event.target.value;
              setPasteText(next);
              // A paste adds many characters at once; typing one at a time is left alone.
              if (next.length - pasteText.length > 1) applyPaste(next);
            }}
            onBlur={() => {
              if (pasteText.trim()) applyPaste(pasteText);
            }}
          />
          {pasteNote ? (
            <p role="status" className="text-muted-foreground text-xs">
              {pasteNote}
            </p>
          ) : null}
        </div>

        <SchemaForm
          descriptor={primaryForm}
          values={values}
          onChange={handleChange}
          idPrefix="s3-add"
          hiddenKeys={primaryHidden}
        />

        <div className="space-y-2">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-expanded={advanced}
            aria-controls="s3-add-advanced"
            onClick={() => setAdvanced(!advanced)}
          >
            {advanced ? (
              <ChevronDown className="h-3.5 w-3.5" aria-hidden />
            ) : (
              <ChevronRight className="h-3.5 w-3.5" aria-hidden />
            )}
            Advanced options
          </Button>
          {advanced ? (
            <div id="s3-add-advanced" className="space-y-3 rounded-md border p-3">
              <SchemaForm
                descriptor={advancedForm}
                values={values}
                onChange={handleChange}
                idPrefix="s3-add"
                hiddenKeys={advancedHidden}
              />
              <div className="space-y-1.5">
                <Label htmlFor="s3-add-revision_mode">Revision mode</Label>
                <select
                  id="s3-add-revision_mode"
                  className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
                  value={revision}
                  onChange={(event) => {
                    touched.current.add("revision_mode");
                    setValues({ ...values, revision_mode: event.target.value });
                  }}
                >
                  {REVISION_OPTIONS.map((option) => (
                    <option key={option.value} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </select>
                <p className="text-muted-foreground text-xs">
                  Amazon S3 keeps object versions, so it defaults to Versioned (strict default);
                  other providers usually do not, so they default to Checked ETag.
                </p>
              </div>
              {mode === "typed" ? (
                <Button type="button" variant="link" size="sm" onClick={() => setMode("connect")}>
                  Find buckets instead
                </Button>
              ) : (
                <Button type="button" variant="link" size="sm" onClick={typeInstead}>
                  Type a bucket name instead
                </Button>
              )}
            </div>
          ) : null}
        </div>

        {mode === "connect" ? (
          <Button type="button" disabled={!connectReady} onClick={() => void connect()}>
            {connecting ? "Connecting..." : "Connect"}
          </Button>
        ) : null}

        {mode === "connected" ? (
          <div className="space-y-3">
            <fieldset className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <legend className="text-sm font-medium">Buckets</legend>
                {buckets.length > 1 ? (
                  <Button
                    type="button"
                    variant="ghost"
                    size="xs"
                    onClick={() =>
                      chooseBuckets(
                        allSelected
                          ? buckets.filter((b) => results[b]?.state === "added")
                          : buckets,
                      )
                    }
                  >
                    {allSelected ? "Select none" : "Select all"}
                  </Button>
                ) : null}
              </div>
              {buckets.length === 0 ? (
                <p className="text-muted-foreground text-sm">
                  No buckets were found for these keys.
                </p>
              ) : (
                <ul className="max-h-48 space-y-1.5 overflow-y-auto">
                  {buckets.map((bucket) => {
                    const done = results[bucket]?.state === "added";
                    return (
                      <li key={bucket} className="flex items-center gap-2">
                        <Checkbox
                          id={`s3-add-bucket-${bucket}`}
                          checked={selected.includes(bucket)}
                          disabled={done}
                          onCheckedChange={() => toggleBucket(bucket)}
                        />
                        <Label htmlFor={`s3-add-bucket-${bucket}`} className="font-normal">
                          {bucket}
                        </Label>
                      </li>
                    );
                  })}
                </ul>
              )}
            </fieldset>

            {selected.length >= 2 ? (
              <p className="text-muted-foreground text-sm">
                Each selected bucket is added from its root.
              </p>
            ) : null}

            {selected.length === 1 ? (
              browsing ? (
                <S3FolderBrowser
                  key={selected[0]}
                  connection={connection()}
                  bucket={selected[0]!}
                  onUse={(chosen) => {
                    setValues({ ...values, prefix: chosen });
                    setBrowsing(false);
                  }}
                  onClose={() => setBrowsing(false)}
                />
              ) : (
                <div className="flex flex-wrap items-center gap-3">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setBrowsing(true)}
                  >
                    Browse folders
                  </Button>
                  <span className="text-muted-foreground text-xs">
                    Folder: {text("prefix") || "bucket root"}
                  </span>
                </div>
              )
            ) : null}
          </div>
        ) : null}

        {mode !== "connect" ? (
          <Button type="button" disabled={pending.length === 0} onClick={() => void save()}>
            {saveLabel}
          </Button>
        ) : null}
      </fieldset>

      {duplicates.length > 0 ? (
        <p className="text-sm text-amber-600 dark:text-amber-400">
          A source with this bucket and prefix already exists
          {targets.length > 1 ? ` for ${duplicates.join(", ")}` : ""}. You can still add another.
        </p>
      ) : null}

      {problems.length > 0 ? (
        <ul role="alert" className="text-destructive list-disc space-y-1 pl-5 text-sm">
          {problems.map((message) => (
            <li key={message}>{message}</li>
          ))}
        </ul>
      ) : null}

      {listError ? (
        <div role="alert" className="text-destructive space-y-2 text-sm">
          <p>{listError.message}</p>
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={() => void connect()}
            >
              Try again
            </Button>
            <Button type="button" variant="ghost" size="sm" onClick={typeInstead}>
              Type a bucket name instead
            </Button>
          </div>
        </div>
      ) : null}

      {resultEntries.length > 0 ? (
        <div role="status" aria-live="polite">
          <ul className="space-y-0.5 text-sm">
            {resultEntries.map(([bucket, outcome]) => (
              <li
                key={bucket}
                className={
                  outcome.state === "failed" ? "text-destructive" : "text-muted-foreground"
                }
              >
                {bucket}:{" "}
                {outcome.state === "added"
                  ? "added"
                  : outcome.state === "failed"
                    ? `failed — ${outcome.message}`
                    : outcome.state === "adding"
                      ? "adding..."
                      : "waiting"}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  );
}
