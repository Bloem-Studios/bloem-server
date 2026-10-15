/**
 * Defaults and plain-language errors for the Add-source form's S3 browsing.
 * Pure helpers, kept apart from the component so each rule has its own test.
 */

export const DEFAULT_REGION = "us-east-1";

export type RevisionMode = "checked-etag" | "strict";

export const AMAZON_REGION_MESSAGE = "Amazon S3 needs a region";

/** Shown for an older server that has no bucket browsing; the form falls back to typing. */
export const BROWSE_UNSUPPORTED_MESSAGE =
  "This server does not support browsing buckets; type the bucket name instead";

const ENDPOINT_REJECTED_MESSAGE =
  "That address can't be used. It must be a public https:// address.";
const BUCKETS_DENIED_MESSAGE =
  "These keys can't list buckets. If your provider gave you a specific bucket name, type it instead.";
const FOLDERS_DENIED_MESSAGE =
  "These keys can't list folders here. You can type the folder (prefix) instead.";
const KEYS_REJECTED_MESSAGE =
  "The provider rejected these keys for this bucket. Check the access key, the secret key and the bucket name.";
const NO_PERMISSION_MESSAGE = "You don't have permission to add storage sources.";
const UNAVAILABLE_MESSAGE = "Storage isn't available on this server right now. Try again later.";
const PLUGIN_REJECTED_MESSAGE =
  "This storage plugin release can't be used. Ask your platform administrator.";
const ADD_FAILED_MESSAGE = "Couldn't add this bucket. Try again.";

/** Shown on a bucket whose add may have succeeded; retrying blindly could create a second source. */
export const OUTCOME_UNKNOWN_MESSAGE =
  "May have been created — check the source list before retrying.";
const UNREACHABLE_MESSAGE = "Couldn't reach the storage service. Check the address and try again.";
const RATE_LIMITED_MESSAGE = "Too many requests. Wait a minute and try again.";
const INCOMPLETE_MESSAGE = "Check the address and keys, then try again.";
const OTHER_MESSAGE = "Something went wrong. Check the details and try again.";

function hostOf(endpoint: string): string {
  const text = endpoint.trim();
  if (!text) return "";
  for (const candidate of [text, `https://${text}`]) {
    try {
      return new URL(candidate).hostname.toLowerCase();
    } catch {
      /* try the next form */
    }
  }
  return "";
}

/** True for an Amazon S3 address, which has object versioning and needs an explicit region. */
export function isAmazonEndpoint(endpoint: string): boolean {
  return hostOf(endpoint).endsWith(".amazonaws.com");
}

/** The region an Amazon S3 address names, such as eu-west-1 for s3.eu-west-1.amazonaws.com; else "". */
export function inferAmazonRegion(endpoint: string): string {
  const host = hostOf(endpoint);
  return (
    /(?:^|\.)s3[.-](?:dualstack[.-])?(?:fips[.-])?([a-z]{2}(?:-gov|-iso[a-z]?)?-[a-z]+-\d+)\.amazonaws\.com$/.exec(
      host,
    )?.[1] ?? ""
  );
}

/**
 * Gateways, MinIO and Ceph accept any region name. Amazon S3 must be told the
 * real one: taken from the address when it names one, else left for the user.
 */
export function defaultRegionFor(endpoint: string): string {
  return isAmazonEndpoint(endpoint) ? inferAmazonRegion(endpoint) : DEFAULT_REGION;
}

/** Only Amazon S3 versions objects; everything else is checked by ETag. */
export function defaultRevisionModeFor(endpoint: string): RevisionMode {
  return isAmazonEndpoint(endpoint) ? "strict" : "checked-etag";
}

/** An address typed without a scheme is taken to be https. */
export function withHttpsScheme(endpoint: string): string {
  return endpoint === "" || /^[a-z][a-z0-9+.-]*:\/\//i.test(endpoint)
    ? endpoint
    : `https://${endpoint}`;
}

/** A folder prefix typed without its trailing slash gets one; a leading slash is left to be reported. */
export function withTrailingSlash(prefix: string): string {
  return prefix === "" || prefix.startsWith("/") || prefix.endsWith("/") ? prefix : `${prefix}/`;
}

function codeOf(error: unknown): string | undefined {
  if (typeof error === "object" && error !== null && "code" in error) {
    const code = (error as { code?: unknown }).code;
    return typeof code === "string" ? code : undefined;
  }
  return undefined;
}

function statusOf(error: unknown): number | undefined {
  if (typeof error === "object" && error !== null && "status" in error) {
    const status = (error as { status?: unknown }).status;
    return typeof status === "number" ? status : undefined;
  }
  return undefined;
}

/**
 * What to tell the administrator when a bucket or folder listing fails. Chosen
 * from the server's error code and status only; the server's own text and
 * anything the backend said are never shown.
 */
export function browseErrorMessage(
  error: unknown,
  listing: "buckets" | "folders" = "buckets",
): string {
  switch (codeOf(error)) {
    case "s3_endpoint_rejected":
      return ENDPOINT_REJECTED_MESSAGE;
    case "s3_access_denied":
      return listing === "buckets" ? BUCKETS_DENIED_MESSAGE : FOLDERS_DENIED_MESSAGE;
    case "s3_backend_error":
      return UNREACHABLE_MESSAGE;
    case "invalid_request":
      return INCOMPLETE_MESSAGE;
  }
  const status = statusOf(error);
  if (status === 429) return RATE_LIMITED_MESSAGE;
  if (status === 404 || status === 405) return BROWSE_UNSUPPORTED_MESSAGE;
  // No status at all is a network failure (the request never got an answer).
  if (status === undefined || status >= 500) return UNREACHABLE_MESSAGE;
  return OTHER_MESSAGE;
}

/** True when the server has no bucket browsing, so the form should offer typing instead. */
export function isBrowseUnsupported(error: unknown): boolean {
  const status = statusOf(error);
  return (
    browseErrorMessage(error) === BROWSE_UNSUPPORTED_MESSAGE && (status === 404 || status === 405)
  );
}

/** True when the request may have reached the server and created the source: no answer, or an unconfirmed write. */
export function isOutcomeUnknown(error: unknown): boolean {
  return codeOf(error) === "mutation_outcome_unknown" || statusOf(error) === undefined;
}

/**
 * What to tell the administrator when adding a source fails. Chosen from the
 * server's error code and status only; the server's own text is never shown.
 */
export function addErrorMessage(error: unknown): string {
  switch (codeOf(error)) {
    case "mutation_outcome_unknown":
      return OUTCOME_UNKNOWN_MESSAGE;
    case "s3_endpoint_rejected":
      return ENDPOINT_REJECTED_MESSAGE;
    case "s3_access_denied":
      return KEYS_REJECTED_MESSAGE;
    case "s3_backend_error":
      return UNREACHABLE_MESSAGE;
    case "invalid_request":
      return INCOMPLETE_MESSAGE;
    case "artifact_rejected":
      return PLUGIN_REJECTED_MESSAGE;
    case "native_storage_unavailable":
      return UNAVAILABLE_MESSAGE;
  }
  const status = statusOf(error);
  if (status === undefined) return OUTCOME_UNKNOWN_MESSAGE;
  if (status === 429) return RATE_LIMITED_MESSAGE;
  if (status === 401 || status === 403) return NO_PERMISSION_MESSAGE;
  if (status === 502 || status === 504) return UNREACHABLE_MESSAGE;
  if (status === 503) return UNAVAILABLE_MESSAGE;
  return ADD_FAILED_MESSAGE;
}
