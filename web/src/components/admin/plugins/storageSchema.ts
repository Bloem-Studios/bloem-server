import type { PluginConfigSchema } from "@/api/types";
import { bloemT } from "@/bloem/i18n";
import { randomUUID } from "@/lib/uuid";

/**
 * Removes fields from a config schema, in both the JSON schema and the admin
 * form, so a form built from it never shows them.
 */
export function pruneConfigSchema(
  schema: PluginConfigSchema,
  hidden: string[],
): PluginConfigSchema {
  const drop = new Set(hidden);
  let jsonSchema = schema.json_schema;
  try {
    const parsed = JSON.parse(schema.json_schema) as {
      properties?: Record<string, unknown>;
      required?: string[];
    };
    if (parsed.properties) {
      parsed.properties = Object.fromEntries(
        Object.entries(parsed.properties).filter(([k]) => !drop.has(k)),
      );
      if (parsed.required) parsed.required = parsed.required.filter((k) => !drop.has(k));
      jsonSchema = JSON.stringify(parsed);
    }
  } catch {
    /* keep the original schema text */
  }
  return {
    ...schema,
    json_schema: jsonSchema,
    admin_form: schema.admin_form
      ? { ...schema.admin_form, fields: schema.admin_form.fields.filter((f) => !drop.has(f.key)) }
      : undefined,
  };
}

/** The S3 request limits (0.5.0 and later): they decide how hard Bloem uses the service. */
export const S3_LIMIT_KEYS = [
  "auth_pause_seconds",
  "auth_pause_max_seconds",
  "auth_failure_budget",
  "auth_failure_window_seconds",
  "max_concurrent_requests",
  "max_listing_requests",
  "max_read_mbps",
];
/** The S3 proxy fields, which only a platform administrator may see or set. */
export const S3_PROXY_KEYS = ["http_proxy", "http_proxy_username", "http_proxy_password"];
/** The optional S3 settings an edit or add form keeps under "Advanced options". */
export const S3_ADVANCED_KEYS = [...S3_LIMIT_KEYS, ...S3_PROXY_KEYS];

/**
 * Groups the S3 limit and proxy fields the schema has into one collapsed
 * "Advanced options" section. A schema without them is returned unchanged.
 */
export function withS3AdvancedSection(schema: PluginConfigSchema): PluginConfigSchema {
  const form = schema.admin_form;
  if (!form) return schema;
  const keys = S3_ADVANCED_KEYS.filter((key) => form.fields.some((f) => f.key === key));
  if (keys.length === 0) return schema;
  return {
    ...schema,
    admin_form: {
      ...form,
      sections: [
        ...(form.sections ?? []),
        {
          key: "bloem-s3-advanced",
          title: bloemT("bloem.admin.storage.s3Add.advancedOptions"),
          collapsible: true,
          collapsed_default: true,
          field_keys: keys,
        },
      ],
    },
  };
}

/** The hint shown under every S3 folder (prefix) field. */
export function prefixHint(): string {
  return bloemT("bloem.admin.storage.s3Fields.prefixHint");
}

/**
 * True when the S3 release accepts a source with no bucket, which then covers
 * every bucket its keys can list: its schema lets the bucket be empty. Older
 * releases require one.
 */
export function s3BucketOptional(schema: PluginConfigSchema): boolean {
  try {
    const parsed = JSON.parse(schema.json_schema) as {
      properties?: Record<string, { minLength?: number }>;
    };
    const bucket = parsed.properties?.bucket;
    return bucket !== undefined && !(bucket.minLength && bucket.minLength > 0);
  } catch {
    return false;
  }
}

/** The S3 form copy Bloem shows whatever the release's manifest says: optional fields say so. */
export function withS3FieldCopy(schema: PluginConfigSchema): PluginConfigSchema {
  if (!schema.admin_form) return schema;
  const optional = s3BucketOptional(schema);
  return {
    ...schema,
    admin_form: {
      ...schema.admin_form,
      fields: schema.admin_form.fields.map((field) => {
        switch (field.key) {
          case "prefix":
            return {
              ...field,
              label: bloemT("bloem.admin.storage.s3Fields.prefixLabel"),
              description: prefixHint(),
            };
          case "source_name":
            return {
              ...field,
              label: bloemT("bloem.admin.storage.s3Fields.nameLabel"),
              description: bloemT("bloem.admin.storage.s3Fields.nameHint"),
            };
          case "endpoint":
            return {
              ...field,
              label: bloemT("bloem.admin.storage.s3Fields.addressLabel"),
              description: bloemT("bloem.admin.storage.s3Fields.addressHint"),
            };
          case "revision_mode":
            return {
              ...field,
              label: bloemT("bloem.admin.storage.s3Fields.revisionModeLabel"),
              description: bloemT("bloem.admin.storage.s3Fields.revisionModeHint"),
              options: field.options?.map((option) => ({
                ...option,
                label:
                  option.value === "checked-etag"
                    ? bloemT("bloem.admin.storage.s3Fields.revisionChecked")
                    : option.value === "versioned"
                      ? bloemT("bloem.admin.storage.s3Fields.revisionVersioned")
                      : option.label,
              })),
            };
          case "session_token":
            return { ...field, label: bloemT("bloem.admin.storage.s3Fields.sessionTokenLabel") };
          case "bucket":
            return optional
              ? {
                  ...field,
                  label: bloemT("bloem.admin.storage.s3Fields.bucketLabel"),
                  description: bloemT("bloem.admin.storage.s3Fields.bucketHint"),
                }
              : field;
          default:
            return field;
        }
      }),
    },
  };
}

/**
 * Field-level problems with an S3 connection entry, keyed by field. Empty when
 * it is valid. With `bucketOptional` an empty bucket means every bucket, which
 * takes no prefix.
 */
export function validateS3Connection(
  value: Record<string, unknown>,
  options: { bucketOptional?: boolean } = {},
): Record<string, string> {
  const errors: Record<string, string> = {};
  const text = (k: string) => (typeof value[k] === "string" ? (value[k] as string) : "");
  const bucket = text("bucket");
  if (!bucket) {
    if (!options.bucketOptional) errors.bucket = bloemT("bloem.admin.storage.s3Fields.enterBucket");
  } else if (!/^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/.test(bucket))
    errors.bucket = bloemT("bloem.admin.storage.s3Fields.bucketCharacters");
  else if (bucket.includes(".."))
    errors.bucket = bloemT("bloem.admin.storage.s3Fields.bucketConsecutiveDots");
  else if (/^\d+\.\d+\.\d+\.\d+$/.test(bucket))
    errors.bucket = bloemT("bloem.admin.storage.s3Fields.bucketLooksLikeIp");
  const prefix = text("prefix");
  if (prefix !== "" && !bucket && options.bucketOptional)
    errors.prefix = bloemT("bloem.admin.storage.s3Fields.folderNeedsBucket");
  else if (prefix.startsWith("/"))
    errors.prefix = bloemT("bloem.admin.storage.s3Fields.folderLeadingSlash");
  else if (prefix !== "" && !prefix.endsWith("/"))
    errors.prefix = bloemT("bloem.admin.storage.s3Fields.folderTrailingSlash");
  const endpoint = text("endpoint");
  if (endpoint) {
    try {
      const u = new URL(endpoint);
      if (u.protocol !== "https:")
        errors.endpoint = bloemT("bloem.admin.storage.s3Fields.addressHttps");
      // Checked on the raw text: an empty "?" or "#" leaves u.search / u.hash empty.
      else if (endpoint.includes("?") || endpoint.includes("#"))
        errors.endpoint = bloemT("bloem.admin.storage.s3Fields.addressQueryOrFragment");
      else if (u.username || u.password)
        errors.endpoint = bloemT("bloem.admin.storage.s3Fields.addressCredentials");
      else if (u.pathname !== "/" && u.pathname !== "")
        errors.endpoint = bloemT("bloem.admin.storage.s3Fields.addressPath");
    } catch {
      errors.endpoint = bloemT("bloem.admin.storage.s3Fields.addressInvalid");
    }
  }
  return errors;
}

/** What a saved entry may carry: an emptied optional field is simply left out. */
export function withoutEmpty(value: Record<string, unknown>): Record<string, unknown> {
  return Object.fromEntries(Object.entries(value).filter(([, v]) => v !== "" && v !== null));
}

/**
 * The typed name, or the bucket (with the prefix, when set) so a source is
 * never shown by its id. A source with no bucket takes `fallback`. The result
 * is saved as the source's name, so the default fallback is data and stays in
 * English.
 */
export function sourceName(value: Record<string, unknown>, fallback = "All buckets"): string {
  const text = (k: string) => (typeof value[k] === "string" ? (value[k] as string) : "");
  if (text("source_name").trim()) return text("source_name").trim();
  if (!text("bucket")) return fallback;
  return `${text("bucket")}${text("prefix") ? `/${text("prefix")}` : ""}`;
}

/** A new provider source id matching the plugin's `<kind>_<32 hex>` pattern. */
export function newProviderSourceId(prefix: string): string {
  return `${prefix}_${randomUUID().replaceAll("-", "")}`;
}
