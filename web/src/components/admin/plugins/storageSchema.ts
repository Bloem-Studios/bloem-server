import type { PluginConfigSchema } from "@/api/types";
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

/** The hint shown under every S3 prefix field. */
export const PREFIX_HINT =
  "Leave empty to use the whole bucket (recommended); choose folders per library";

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
            return { ...field, label: "Prefix (optional)", description: PREFIX_HINT };
          case "session_token":
            return { ...field, label: "Session token (optional)" };
          case "bucket":
            return optional
              ? {
                  ...field,
                  label: "Bucket (optional)",
                  description: "Leave empty to cover every bucket these keys can see.",
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
    if (!options.bucketOptional) errors.bucket = "Enter the bucket name.";
  } else if (!/^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/.test(bucket))
    errors.bucket = "Bucket names use 3–63 lowercase letters, digits, dots and hyphens.";
  else if (bucket.includes("..")) errors.bucket = "Bucket names must not contain consecutive dots.";
  else if (/^\d+\.\d+\.\d+\.\d+$/.test(bucket))
    errors.bucket = "Bucket names must not look like an IP address.";
  const prefix = text("prefix");
  if (prefix !== "" && !bucket && options.bucketOptional)
    errors.prefix = "A prefix needs a bucket. Leave it empty to use every bucket.";
  else if (prefix.startsWith("/")) errors.prefix = "The prefix must not start with a slash.";
  else if (prefix !== "" && !prefix.endsWith("/"))
    errors.prefix = "The prefix must end with a slash, for example movies/.";
  const endpoint = text("endpoint");
  if (endpoint) {
    try {
      const u = new URL(endpoint);
      if (u.protocol !== "https:") errors.endpoint = "The endpoint must use https.";
      // Checked on the raw text: an empty "?" or "#" leaves u.search / u.hash empty.
      else if (endpoint.includes("?") || endpoint.includes("#"))
        errors.endpoint = "The endpoint must not include a query or fragment.";
      else if (u.username || u.password)
        errors.endpoint = "The endpoint must not contain credentials.";
      else if (u.pathname !== "/" && u.pathname !== "")
        errors.endpoint = "The endpoint must not include a path.";
    } catch {
      errors.endpoint = "Enter a full URL such as https://s3.example.com.";
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
 * never shown by its id. A source with no bucket takes `fallback`.
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
