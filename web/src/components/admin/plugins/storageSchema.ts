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

/** Field-level problems with an S3 connection entry, keyed by field. Empty when it is valid. */
export function validateS3Connection(value: Record<string, unknown>): Record<string, string> {
  const errors: Record<string, string> = {};
  const text = (k: string) => (typeof value[k] === "string" ? (value[k] as string) : "");
  const bucket = text("bucket");
  if (!bucket) errors.bucket = "Enter the bucket name.";
  else if (!/^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/.test(bucket))
    errors.bucket = "Bucket names use 3–63 lowercase letters, digits, dots and hyphens.";
  else if (bucket.includes("..")) errors.bucket = "Bucket names must not contain consecutive dots.";
  else if (/^\d+\.\d+\.\d+\.\d+$/.test(bucket))
    errors.bucket = "Bucket names must not look like an IP address.";
  const prefix = text("prefix");
  if (prefix.startsWith("/")) errors.prefix = "The prefix must not start with a slash.";
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

/** The typed name, or the bucket (with the prefix, when set) so a source is never shown by its id. */
export function sourceName(value: Record<string, unknown>): string {
  const text = (k: string) => (typeof value[k] === "string" ? (value[k] as string) : "");
  return (
    text("source_name").trim() || `${text("bucket")}${text("prefix") ? `/${text("prefix")}` : ""}`
  );
}

/** A new provider source id matching the plugin's `<kind>_<32 hex>` pattern. */
export function newProviderSourceId(prefix: string): string {
  return `${prefix}_${randomUUID().replaceAll("-", "")}`;
}
