import { useEffect, useMemo, useRef, useState, type FocusEvent } from "react";
import { ArrowLeft, CheckCircle2, ChevronDown, ChevronRight } from "lucide-react";
import { Link } from "react-router";
import { toast } from "sonner";

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
  OUTCOME_UNKNOWN_MESSAGE,
  addErrorMessage,
  browseErrorMessage,
  defaultRegionFor,
  defaultRevisionModeFor,
  inferAmazonRegion,
  isAmazonEndpoint,
  isBrowseUnsupported,
  isOutcomeUnknown,
  withHttpsScheme,
  withTrailingSlash,
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
// Pasted or typed values often carry stray spaces and line breaks.
const TRIM_KEYS = [
  "endpoint",
  "region",
  "access_key",
  "secret_key",
  "session_token",
  "bucket",
  "prefix",
];
const PROBLEMS_ID = "s3-add-problems";
const BUCKET_GROUP_ID = "s3-add-bucket-group";

type Mode = "connect" | "connected" | "typed";
type Values = Record<string, unknown>;

interface Outcome {
  id: string;
  state: "waiting" | "adding" | "added" | "failed";
  message?: string;
  name?: string;
  /** The request may have reached the server: do not retry without checking the source list. */
  unknown?: boolean;
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
  onBusyChange,
}: {
  artifact: StorageArtifact;
  sources: StorageSource[];
  onBack: () => void;
  onDone: () => void;
  /** True while a batch of sources is being added, so the dialog is not dismissed part-way. */
  onBusyChange?: (busy: boolean) => void;
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
      onBusyChange={onBusyChange}
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
  onBusyChange,
}: {
  artifactKey: string;
  schemaKey: string;
  descriptor: PluginAdminForm;
  sources: StorageSource[];
  onBack: () => void;
  onDone: () => void;
  onBusyChange?: (busy: boolean) => void;
}) {
  const discover = useDiscoverStorageSource();
  const listBuckets = useListS3Buckets();
  const { reset: resetDiscover } = discover;
  const { reset: resetBuckets } = listBuckets;

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
  const [invalid, setInvalid] = useState<string[]>([]);
  const [listError, setListError] = useState<{ message: string } | null>(null);
  const [connecting, setConnecting] = useState(false);
  const [results, setResults] = useState<Record<string, Outcome>>({});
  const [saving, setSaving] = useState(false);
  const [added, setAdded] = useState<string[] | null>(null);
  // Bumped when an error or failed result appears / the bucket list arrives, to move focus there.
  const [attention, setAttention] = useState(0);
  const [bucketsArrived, setBucketsArrived] = useState(0);
  // Ignores a bucket listing that finishes after the connection details changed.
  const listSeq = useRef(0);
  // Guards against a second Connect or Save starting before the first has finished.
  const connectFlight = useRef(0);
  const saveFlight = useRef(false);
  const alive = useRef(true);
  const feedbackRef = useRef<HTMLDivElement>(null);
  const legendRef = useRef<HTMLLegendElement>(null);
  const successRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  // Leaving the form drops the keys the mutation observers still hold as variables.
  useEffect(
    () => () => {
      resetDiscover();
      resetBuckets();
    },
    [resetDiscover, resetBuckets],
  );
  useEffect(() => {
    onBusyChange?.(saving);
  }, [saving, onBusyChange]);
  useEffect(() => () => onBusyChange?.(false), [onBusyChange]);
  useEffect(() => {
    if (attention === 0) return;
    const region = feedbackRef.current;
    region?.scrollIntoView?.({ block: "nearest" });
    region?.focus();
  }, [attention]);
  useEffect(() => {
    if (bucketsArrived > 0) legendRef.current?.focus();
  }, [bucketsArrived]);
  useEffect(() => {
    if (added) successRef.current?.focus();
  }, [added]);

  // Fields a validation message refers to say so to assistive technology.
  useEffect(() => {
    const elements = invalid
      .map((field) =>
        document.getElementById(
          field === "bucket" && mode !== "typed" ? BUCKET_GROUP_ID : `s3-add-${field}`,
        ),
      )
      .filter((element): element is HTMLElement => element !== null);
    for (const element of elements) {
      element.setAttribute("aria-invalid", "true");
      element.setAttribute("aria-describedby", PROBLEMS_ID);
    }
    return () => {
      for (const element of elements) {
        element.removeAttribute("aria-invalid");
        element.removeAttribute("aria-describedby");
      }
    };
  }, [invalid, advanced, mode]);

  // What is sent and validated: trimmed, an address with a scheme, a prefix with its slash.
  const clean: Values = { ...values };
  for (const key of TRIM_KEYS) {
    if (typeof clean[key] === "string") clean[key] = clean[key].trim();
  }
  if (typeof clean.endpoint === "string") clean.endpoint = withHttpsScheme(clean.endpoint);
  if (typeof clean.prefix === "string") clean.prefix = withTrailingSlash(clean.prefix);
  if (clean.region === "" && typeof clean.endpoint === "string") {
    clean.region = inferAmazonRegion(clean.endpoint);
  }

  const labels = new Map(descriptor.fields.map((f) => [f.key, f.label || f.key]));
  const label = (key: string) => labels.get(key) ?? key;
  const text = (key: string) => (typeof clean[key] === "string" ? (clean[key] as string) : "");
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
    connectFlight.current = 0;
    setConnecting(false);
    setMode((current) => (current === "connected" ? "connect" : current));
    setBuckets([]);
    setSelected([]);
    setBrowsing(false);
    setListError(null);
    setProblems([]);
    setInvalid([]);
    setResults({});
  }

  function update(next: Values) {
    let merged = next;
    if (next.endpoint !== values.endpoint) {
      const endpoint =
        typeof next.endpoint === "string" ? withHttpsScheme(next.endpoint.trim()) : "";
      merged = { ...next };
      if (!touched.current.has("region")) merged.region = defaultRegionFor(endpoint);
      if (!touched.current.has("revision_mode"))
        merged.revision_mode = defaultRevisionModeFor(endpoint);
    }
    if (CONNECTION_KEYS.some((key) => merged[key] !== values[key])) dropConnection();
    // A browser opened for one typed bucket means nothing once the name changes.
    if (merged.bucket !== values.bucket) setBrowsing(false);
    setValues(merged);
  }

  function handleChange(next: Values) {
    if (next.region !== values.region) touched.current.add("region");
    update(next);
  }

  // Tidies the two fields people most often type loosely once they leave them.
  function handleBlur(event: FocusEvent<HTMLElement>) {
    const id = event.target.id;
    const key = id === "s3-add-endpoint" ? "endpoint" : id === "s3-add-prefix" ? "prefix" : "";
    const raw = key ? values[key] : undefined;
    if (!key || typeof raw !== "string") return;
    const tidy = key === "endpoint" ? withHttpsScheme(raw.trim()) : withTrailingSlash(raw.trim());
    if (tidy !== raw) update({ ...values, [key]: tidy });
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
    if (!text("region"))
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
    setInvalid([...new Set(found.map((p) => p.field))]);
    setListError(null);
    if (found.length > 0) setAttention((n) => n + 1);
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
    if (connectFlight.current !== 0) return;
    const found = connectionProblems();
    showProblems(found);
    if (found.length > 0) return;
    const id = ++listSeq.current;
    connectFlight.current = id;
    setConnecting(true);
    // Whatever was pasted and not read is not kept once the keys are in use.
    setPasteText("");
    try {
      const names = await listBuckets.mutateAsync(connection());
      if (id !== listSeq.current) return;
      setBuckets(names);
      // A single bucket is the obvious choice.
      setSelected(names.length === 1 ? names : []);
      setMode("connected");
      setBucketsArrived((n) => n + 1);
    } catch (error) {
      if (id !== listSeq.current) return;
      setListError({ message: browseErrorMessage(error) });
      setAttention((n) => n + 1);
      // The same answer would come back, so offer typing straight away.
      if (isBrowseUnsupported(error)) setAdvanced(true);
    } finally {
      if (connectFlight.current === id) connectFlight.current = 0;
      if (id === listSeq.current) setConnecting(false);
    }
  }

  function typeInstead() {
    setMode("typed");
    setAdvanced(true);
    setBrowsing(false);
    setListError(null);
    setProblems([]);
    setInvalid([]);
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
    const built = buildSchemaValues(descriptor, clean, undefined, SAVE_OPTIONS);
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
    if (saveFlight.current) return;
    const found = saveProblems();
    showProblems(found);
    if (found.length > 0) return;
    saveFlight.current = true;
    try {
      const only = targets.length === 1;
      // A bucket whose last attempt may have gone through is not tried again on its own.
      const pending = targets.filter((b) => results[b]?.state !== "added" && !results[b]?.unknown);
      // Every attempt gets a fresh id. The server does not recognise a repeated id,
      // so reusing one would not make a retry safe; unconfirmed attempts are held back instead.
      const progress: Record<string, Outcome> = { ...results };
      for (const bucket of pending) {
        progress[bucket] = { id: newProviderSourceId("s3"), state: "waiting" };
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
            // Reported inline and in one summary toast below, not once per bucket.
            silent: true,
          });
          progress[bucket] = { id, state: "added", name: String(entry.source_name) };
        } catch (error) {
          progress[bucket] = {
            id,
            state: "failed",
            message: addErrorMessage(error),
            unknown: isOutcomeUnknown(error),
          };
        }
        // The dialog was closed: do not go on adding sources nobody is watching.
        if (!alive.current) return;
        setResults({ ...progress });
      }
      setSaving(false);
      if (targets.every((b) => progress[b]?.state === "added")) {
        const names = targets.map((b) => progress[b]?.name ?? b);
        toast.success(
          names.length === 1 ? "Storage source added" : `Added ${names.length} storage sources`,
        );
        resetForm();
        setAdded(names);
      } else {
        const failedCount = targets.filter((b) => progress[b]?.state === "failed").length;
        toast.error(
          targets.length === 1
            ? "Couldn't add the storage source"
            : `${failedCount} of ${targets.length} storage sources couldn't be added`,
        );
        setAttention((n) => n + 1);
      }
    } finally {
      saveFlight.current = false;
    }
  }

  // Forgets every entry, the keys included, and what the mutations hold of them.
  function resetForm() {
    listSeq.current += 1;
    connectFlight.current = 0;
    touched.current.clear();
    resetDiscover();
    resetBuckets();
    setValues(initial);
    setMode("connect");
    setBuckets([]);
    setSelected([]);
    setBrowsing(false);
    setAdvanced(false);
    setPasteText("");
    setPasteNote("");
    setProblems([]);
    setInvalid([]);
    setListError(null);
    setConnecting(false);
    setResults({});
  }

  if (added) {
    return (
      <div ref={successRef} tabIndex={-1} className="space-y-3 outline-none" role="status">
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

  const pending = targets.filter((b) => results[b]?.state !== "added" && !results[b]?.unknown);
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
  const typedBucket = text("bucket");
  const canBrowseTyped =
    mode === "typed" &&
    typedBucket !== "" &&
    !validateS3Connection({ bucket: typedBucket }).bucket &&
    connectionProblems().length === 0;

  const folderControl = (bucket: string) =>
    browsing ? (
      <S3FolderBrowser
        key={bucket}
        connection={connection()}
        bucket={bucket}
        onUse={(chosen) => {
          setValues({ ...values, prefix: chosen });
          setBrowsing(false);
        }}
        onClose={() => setBrowsing(false)}
      />
    ) : (
      <div className="flex flex-wrap items-center gap-3">
        <Button type="button" variant="outline" size="sm" onClick={() => setBrowsing(true)}>
          Browse folders
        </Button>
        <span className="text-muted-foreground text-xs">
          Folder: {text("prefix") || "bucket root"}
        </span>
      </div>
    );

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

        <div onBlur={handleBlur}>
          <SchemaForm
            descriptor={primaryForm}
            values={values}
            onChange={handleChange}
            idPrefix="s3-add"
            hiddenKeys={primaryHidden}
          />
        </div>

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
              <div onBlur={handleBlur}>
                <SchemaForm
                  descriptor={advancedForm}
                  values={values}
                  onChange={handleChange}
                  idPrefix="s3-add"
                  hiddenKeys={advancedHidden}
                />
              </div>
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

        <p role="status" className="text-muted-foreground text-xs">
          {mode === "connected"
            ? `${buckets.length} ${plural(buckets.length, "bucket", "buckets")} found`
            : ""}
        </p>

        {mode === "connected" ? (
          <div className="space-y-3">
            <fieldset id={BUCKET_GROUP_ID} className="space-y-2">
              <legend ref={legendRef} tabIndex={-1} className="text-sm font-medium outline-none">
                Buckets
              </legend>
              {buckets.length > 1 ? (
                <div className="flex justify-end">
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
                </div>
              ) : null}
              {buckets.length === 0 ? (
                <div className="space-y-2">
                  <p className="text-muted-foreground text-sm">
                    No buckets were found for these keys. If your provider gave you a specific
                    bucket name, type it instead.
                  </p>
                  <Button type="button" variant="outline" size="sm" onClick={typeInstead}>
                    Type a bucket name instead
                  </Button>
                </div>
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

            {selected.length === 1 ? folderControl(selected[0]!) : null}
          </div>
        ) : null}

        {canBrowseTyped ? folderControl(typedBucket) : null}

        {mode !== "connect" ? (
          <Button type="button" disabled={pending.length === 0} onClick={() => void save()}>
            {saveLabel}
          </Button>
        ) : null}
      </fieldset>

      <div ref={feedbackRef} tabIndex={-1} className="space-y-3 outline-none">
        {duplicates.length > 0 ? (
          <p className="text-sm text-amber-600 dark:text-amber-400">
            A source with this bucket and prefix already exists
            {targets.length > 1 ? ` for ${duplicates.join(", ")}` : ""}. You can still add another.
          </p>
        ) : null}

        {problems.length > 0 ? (
          <ul
            id={PROBLEMS_ID}
            role="alert"
            className="text-destructive list-disc space-y-1 pl-5 text-sm"
          >
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

        <div role="status" aria-live="polite">
          {resultEntries.length > 0 ? (
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
                      ? outcome.unknown
                        ? OUTCOME_UNKNOWN_MESSAGE
                        : `failed — ${outcome.message}`
                      : outcome.state === "adding"
                        ? "adding..."
                        : "waiting"}
                  {outcome.state === "failed" && outcome.unknown && !busy ? (
                    <Button
                      type="button"
                      variant="link"
                      size="xs"
                      onClick={() =>
                        setResults({ ...results, [bucket]: { ...outcome, unknown: false } })
                      }
                    >
                      Retry {bucket} anyway
                    </Button>
                  ) : null}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </div>
    </div>
  );
}
