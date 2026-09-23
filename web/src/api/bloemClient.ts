// Bloem's JSON API client (legacy /api/v1 and native /api/bloem/v1 routes).
// Built on the Silo-owned session transport in client.ts so that file stays
// close to upstream.
import type { ApiError } from "./types";
import {
  ApiClientError,
  StaleApiRequestContextError,
  fetchWithSession,
  isProfileRequestContextCurrent,
  reportProfileUnverified,
  type ProfileRequestContextSnapshot,
} from "./client";
import { resolveRequestPolicy, type RequestPolicy } from "./bloemRequestPolicy";
import { storage } from "../utils/storage";

export type { RequestPolicy } from "./bloemRequestPolicy";

export function getProfileId(): string | null {
  return storage.get(storage.KEYS.PROFILE_ID);
}

function setHeader(headers: Record<string, string>, name: string, value: string): void {
  const target = name.toLowerCase();
  for (const key of Object.keys(headers)) {
    if (key.toLowerCase() === target) delete headers[key];
  }
  headers[name] = value;
}

interface ParsedApiError {
  /** Normalized error with guaranteed `error`/`message` fields. */
  apiErr: ApiError;
  /** Raw parsed JSON body, or undefined when the body wasn't JSON/empty. */
  raw?: unknown;
}

// Restored: b482c165f removed both of these as "unused legacy client helpers",
// but parseApiError below still calls normalizeApiError, so every non-2xx
// response raised a ReferenceError instead of an ApiClientError. The build did
// not catch it because `tsc -p tsconfig.json` resolves to a solution file with
// no `include` and checks nothing.
function fallbackApiErrorMessage(res: Response): string {
  const statusText = res.statusText.trim();
  if (statusText) {
    return statusText;
  }
  if (res.status === 401) {
    return "Authentication required.";
  }
  if (res.status === 403) {
    return "You do not have permission to perform this action.";
  }
  if (res.status === 404) {
    return "Requested resource was not found.";
  }
  if (res.status >= 500) {
    return "Request failed. Please try again.";
  }
  if (res.status > 0) {
    return `Request failed (${res.status}).`;
  }
  return "Request failed.";
}

function normalizeApiError(apiErr: Partial<ApiError> | null, res: Response): ApiError {
  const payload = apiErr && typeof apiErr === "object" ? apiErr : {};
  const code =
    typeof payload.error === "string" && payload.error.trim() ? payload.error : "unknown";
  const message =
    typeof payload.message === "string" && payload.message.trim()
      ? payload.message.trim()
      : fallbackApiErrorMessage(res);

  return {
    ...payload,
    error: code,
    message,
  };
}

async function parseApiError(res: Response): Promise<ParsedApiError> {
  let apiErr: Partial<ApiError> = {};
  let raw: unknown;
  try {
    raw = await res.json();
    if (raw && typeof raw === "object") {
      apiErr = raw as Partial<ApiError>;
    }
  } catch {
    // response wasn't JSON
  }
  return { apiErr: normalizeApiError(apiErr, res), raw };
}

/** Builds an ApiClientError from a parsed error response, attaching the raw body. */
function apiClientErrorFrom(status: number, parsed: ParsedApiError): ApiClientError {
  const err = new ApiClientError(status, parsed.apiErr.error, parsed.apiErr.message, parsed.apiErr);
  err.body = parsed.raw;
  return err;
}

async function readApiResponse<T>(res: Response): Promise<T> {
  // Handle empty successful responses.
  if (res.status === 204 || res.status === 205) {
    return undefined as T;
  }
  const text = await res.text();
  if (text.trim() === "") {
    return undefined as T;
  }
  return JSON.parse(text) as T;
}

export async function api<T>(
  path: string,
  options: RequestInit = {},
  policy?: RequestPolicy,
): Promise<T> {
  return readApiResponse<T>(await apiResponse(path, options, policy));
}

/** Native operational routes share the account client, but never replay writes by default. */
export async function nativeApi<T>(
  path: string,
  options: RequestInit = {},
  policy?: RequestPolicy,
): Promise<T> {
  const readOnly =
    !options.method || ["GET", "HEAD", "OPTIONS"].includes(options.method.toUpperCase());
  return readApiResponse<T>(
    await apiResponseInternal(
      path,
      options,
      policy ?? (readOnly ? "safe" : "none"),
      undefined,
      "/api/bloem/v1",
    ),
  );
}

/**
 * Sends a request with one captured account/profile authority. The explicit
 * headers cannot be replaced by the current session, and a stale snapshot is
 * rejected before fetch. Non-idempotent mutations pass policy "none" to avoid
 * replaying an operation whose response was lost; existing callers retain "safe".
 */
export function apiWithProfileRequestContext<T>(
  path: string,
  snapshot: ProfileRequestContextSnapshot,
  options: RequestInit = {},
  policy: RequestPolicy = "safe",
): Promise<T> {
  return apiForProfile<T>(path, snapshot, options, "/api/v1", policy);
}

/** Native viewer routes use the same authentication, profile binding and refresh flow. */
export function nativeApiWithProfileRequestContext<T>(
  path: string,
  snapshot: ProfileRequestContextSnapshot,
  options: RequestInit = {},
  policy: RequestPolicy = "safe",
): Promise<T> {
  return apiForProfile<T>(path, snapshot, options, "/api/bloem/v1", policy);
}

async function apiForProfile<T>(
  path: string,
  snapshot: ProfileRequestContextSnapshot,
  options: RequestInit,
  prefix: "/api/v1" | "/api/bloem/v1",
  policy: RequestPolicy = "safe",
): Promise<T> {
  if (!isProfileRequestContextCurrent(snapshot)) {
    throw new StaleApiRequestContextError();
  }
  const headers = { ...(options.headers as Record<string, string>) };
  setHeader(headers, "Authorization", `Bearer ${snapshot.accessToken}`);
  setHeader(headers, "X-Profile-Id", snapshot.profileId);
  setHeader(headers, "X-Profile-Token", snapshot.profileToken ?? "");
  const response = await apiResponseInternal(
    path,
    { ...options, headers },
    policy,
    snapshot,
    prefix,
  );
  if (!isProfileRequestContextCurrent(snapshot)) {
    throw new StaleApiRequestContextError();
  }
  return readApiResponse<T>(response);
}

/** Performs an authenticated API request while leaving the successful body unread. */
export async function apiResponse(
  path: string,
  options: RequestInit = {},
  policy?: RequestPolicy,
): Promise<Response> {
  return apiResponseInternal(path, options, resolveRequestPolicy(options, policy));
}

async function apiResponseInternal(
  path: string,
  options: RequestInit,
  policy: RequestPolicy,
  snapshot?: ProfileRequestContextSnapshot,
  prefix: "/api/v1" | "/api/bloem/v1" = "/api/v1",
): Promise<Response> {
  const { res, requestProfileId, requestProfileToken } = await fetchWithSession(
    `${prefix}${path}`,
    options,
    snapshot,
    policy !== "none",
    policy,
  );

  if (!res.ok) {
    const parsed = await parseApiError(res);
    if (res.status === 403 && parsed.apiErr.error === "profile_unverified") {
      reportProfileUnverified(requestProfileId, requestProfileToken, snapshot);
    }
    throw apiClientErrorFrom(res.status, parsed);
  }
  return res;
}
