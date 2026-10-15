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
const ACCESS_DENIED_MESSAGE =
  "The provider rejected these keys. Check the access key and secret key. Some providers also only allow connections from approved IP addresses — if yours does, ask them to approve this server.";
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

/** Gateways, MinIO and Ceph accept any region name; Amazon S3 must be told the real one. */
export function defaultRegionFor(endpoint: string): string {
  return isAmazonEndpoint(endpoint) ? "" : DEFAULT_REGION;
}

/** Only Amazon S3 versions objects; everything else is checked by ETag. */
export function defaultRevisionModeFor(endpoint: string): RevisionMode {
  return isAmazonEndpoint(endpoint) ? "strict" : "checked-etag";
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
export function browseErrorMessage(error: unknown): string {
  switch (codeOf(error)) {
    case "s3_endpoint_rejected":
      return ENDPOINT_REJECTED_MESSAGE;
    case "s3_access_denied":
      return ACCESS_DENIED_MESSAGE;
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
