/**
 * Defaults and plain-language errors for the Add-source form's S3 browsing.
 * Pure helpers, kept apart from the component so each rule has its own test.
 */
import { bloemT, type BloemMessageKey } from "@/bloem/i18n";

export const DEFAULT_REGION = "us-east-1";

export type RevisionMode = "checked-etag" | "strict";

/** The messages the S3 errors are chosen from; the server's own text is never one of them. */
const MESSAGES = {
  amazonRegion: "bloem.admin.storage.s3Errors.amazonRegion",
  browseUnsupported: "bloem.admin.storage.s3Errors.browseUnsupported",
  endpointRejected: "bloem.admin.storage.s3Errors.endpointRejected",
  bucketsDenied: "bloem.admin.storage.s3Errors.bucketsDenied",
  foldersDenied: "bloem.admin.storage.s3Errors.foldersDenied",
  keysRejected: "bloem.admin.storage.s3Errors.keysRejected",
  noPermission: "bloem.admin.storage.s3Errors.noPermission",
  unavailable: "bloem.admin.storage.s3Errors.unavailable",
  pluginRejected: "bloem.admin.storage.s3Errors.pluginRejected",
  addFailed: "bloem.admin.storage.s3Errors.addFailed",
  outcomeUnknown: "bloem.admin.storage.s3Errors.outcomeUnknown",
  unreachable: "bloem.admin.storage.s3Errors.unreachable",
  rateLimited: "bloem.admin.storage.s3Errors.rateLimited",
  incomplete: "bloem.admin.storage.s3Errors.incomplete",
  other: "bloem.admin.storage.s3Errors.other",
  sourceChanged: "bloem.admin.storage.s3Errors.sourceChanged",
  sourceGone: "bloem.admin.storage.s3Errors.sourceGone",
  sourceKeysOnly: "bloem.admin.storage.s3Errors.sourceKeysOnly",
  sourceInUse: "bloem.admin.storage.s3Errors.sourceInUse",
  sourceKeysRefused: "bloem.admin.storage.s3Errors.sourceKeysRefused",
} as const satisfies Record<string, BloemMessageKey>;

type ErrorKind = keyof typeof MESSAGES;

function message(kind: ErrorKind): string {
  return bloemT(MESSAGES[kind]);
}

/** Shown when an Amazon S3 address has no region. */
export function amazonRegionMessage(): string {
  return message("amazonRegion");
}

/** Shown for an older server that has no bucket browsing; the form falls back to typing. */
export function browseUnsupportedMessage(): string {
  return message("browseUnsupported");
}

/** Shown on a bucket whose add may have succeeded; retrying blindly could create a second source. */
export function outcomeUnknownMessage(): string {
  return message("outcomeUnknown");
}

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

/** True for an Amazon S3 address, which needs an explicit region. */
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

/**
 * Checked ETag for every provider. It still reads exact versions when a bucket
 * keeps them, while strict mode refuses every file of a bucket without
 * versioning, which is how Amazon S3 buckets start. Strict is an explicit choice.
 */
export const DEFAULT_REVISION_MODE: RevisionMode = "checked-etag";

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
  return message(browseErrorKind(error, listing));
}

function browseErrorKind(error: unknown, listing: "buckets" | "folders"): ErrorKind {
  switch (codeOf(error)) {
    case "s3_endpoint_rejected":
      return "endpointRejected";
    case "s3_access_denied":
      return listing === "buckets" ? "bucketsDenied" : "foldersDenied";
    case "s3_backend_error":
      return "unreachable";
    case "invalid_request":
      return "incomplete";
  }
  const status = statusOf(error);
  if (status === 429) return "rateLimited";
  if (status === 404 || status === 405) return "browseUnsupported";
  // No status at all is a network failure (the request never got an answer).
  if (status === undefined || status >= 500) return "unreachable";
  return "other";
}

/** True when the server has no bucket browsing, so the form should offer typing instead. */
export function isBrowseUnsupported(error: unknown): boolean {
  const status = statusOf(error);
  return (
    browseErrorKind(error, "buckets") === "browseUnsupported" && (status === 404 || status === 405)
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
  return message(addErrorKind(error));
}

function addErrorKind(error: unknown): ErrorKind {
  switch (codeOf(error)) {
    case "mutation_outcome_unknown":
      return "outcomeUnknown";
    case "s3_endpoint_rejected":
      return "endpointRejected";
    case "s3_access_denied":
      return "keysRejected";
    case "s3_backend_error":
      return "unreachable";
    case "invalid_request":
      return "incomplete";
    case "artifact_rejected":
      return "pluginRejected";
    case "native_storage_unavailable":
      return "unavailable";
  }
  const status = statusOf(error);
  if (status === undefined) return "outcomeUnknown";
  if (status === 429) return "rateLimited";
  if (status === 401 || status === 403) return "noPermission";
  if (status === 502 || status === 504) return "unreachable";
  if (status === 503) return "unavailable";
  return "addFailed";
}

/**
 * What to tell the administrator when testing, changing, disabling or removing
 * an existing source fails. Like the add messages, it is chosen from the
 * server's error code and status only and never shows the server's own text.
 */
export function sourceActionErrorMessage(error: unknown): string {
  switch (codeOf(error)) {
    case "revision_conflict":
      return message("sourceChanged");
    case "not_found":
      return message("sourceGone");
    case "configuration_namespace_unverified":
      return message("sourceKeysOnly");
    case "source_in_use":
      return message("sourceInUse");
    case "s3_access_denied":
      return message("sourceKeysRefused");
  }
  const kind = addErrorKind(error);
  return message(kind === "addFailed" ? "other" : kind);
}
