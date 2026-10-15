/**
 * Reads S3 connection details out of pasted text, such as a provider's welcome
 * email or an rclone config block. Pure and browser-only: nothing here sends,
 * stores or logs the text.
 */

export interface ParsedS3Details {
  endpoint?: string;
  accessKey?: string;
  secretKey?: string;
  region?: string;
}

type Field = keyof ParsedS3Details;

// A label, normalised to lowercase letters and digits, maps to a field when it
// ends with one of these. Longest first, so "secretaccesskey" is a secret key
// and not an access key that happens to end in "accesskey".
const LABELS: [string, Field][] = (
  [
    ["endpointurl", "endpoint"],
    ["endpoint", "endpoint"],
    ["secretaccesskey", "secretKey"],
    ["accesskeysecret", "secretKey"],
    ["secretkey", "secretKey"],
    ["keysecret", "secretKey"],
    ["accesskeyid", "accessKey"],
    ["accesskey", "accessKey"],
    ["region", "region"],
  ] as [string, Field][]
).sort((a, b) => b[0].length - a[0].length);

// A label that must match whole: "Secret: x" is a secret key, "Client secret" is not.
const EXACT_LABELS: Record<string, Field> = { secret: "secretKey" };

const PLACEHOLDERS = new Set([
  "not",
  "none",
  "n/a",
  "na",
  "null",
  "nil",
  "-",
  "--",
  "tbd",
  "unknown",
]);

// label, then ":" or "=", then one value that is quoted or has no whitespace.
// The label may carry spaces ("Access Key ID") and a bit of surrounding text.
const PAIR =
  /["'`]?([A-Za-z][A-Za-z0-9 ._-]{0,38}?)["'`]?\**\s*[:=]\**\s*("[^"]*"|“[^”]*”|'[^']*'|‘[^’]*’|`[^`]*`|\S+)/g;

function fieldForLabel(label: string): Field | undefined {
  const normal = label.toLowerCase().replace(/[^a-z0-9]/g, "");
  if (EXACT_LABELS[normal]) return EXACT_LABELS[normal];
  return LABELS.find(([suffix]) => normal.endsWith(suffix))?.[1];
}

function cleanValue(raw: string): string {
  let value = raw.trim();
  const opening = /^[“‘"'`<(*]+/;
  const closing = /[”’"'`>),.;*]+$/;
  value = value.replace(opening, "").replace(closing, "").trim();
  return value;
}

function usable(value: string): boolean {
  return value !== "" && !PLACEHOLDERS.has(value.toLowerCase());
}

function normalEndpoint(value: string): string {
  const withScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(value) ? value : `https://${value}`;
  return withScheme.replace(/^([a-z][a-z0-9+.-]*:\/\/[^/]+)\/+$/i, "$1");
}

function plausibleRegion(value: string): boolean {
  return /^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(value);
}

export function parseS3Details(text: string): ParsedS3Details {
  const found: ParsedS3Details = {};
  const lines = text.split(/\r?\n/);
  for (const line of lines) {
    for (const match of line.matchAll(PAIR)) {
      const field = fieldForLabel(match[1] ?? "");
      if (!field || found[field] !== undefined) continue;
      const value = cleanValue(match[2] ?? "");
      if (!usable(value)) continue;
      if (field === "region" && !plausibleRegion(value)) continue;
      found[field] = field === "endpoint" ? normalEndpoint(value) : value;
    }
  }
  if (found.endpoint === undefined) {
    // "... the endpoint https://s3.example.com ..." with no label separator.
    for (const line of lines) {
      if (!/endpoint/i.test(line)) continue;
      const url = /https?:\/\/[^\s"'`<>)]+/i.exec(line)?.[0];
      if (url) {
        found.endpoint = normalEndpoint(cleanValue(url));
        break;
      }
    }
  }
  return found;
}
